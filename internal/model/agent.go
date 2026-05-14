package model

import (
	"fmt"
	"math/rand/v2"
	"os"
	"strings"
	"time"
)

// Agent is the persistent identity layer above sessions. Name is the
// free-form slug the agent picks (typically "verb-animal@harness.host"
// per the SKILL.md convention, but bacio doesn't enforce a shape). One
// agent racks up many sessions over its lifetime; the join is what
// lets `bacio agent show` reconstruct cross-session activity.
type Agent struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
}

// AgentSession is one running instance of an AI agent (typically a
// Claude Code session) talking to a bacio repo. The registry is
// local-only — never synced to GitHub — so it's safe to record
// machine-specific fields like host and branch.
//
// SessionID is the external id (e.g. CLAUDE_CODE_SESSION_ID); ID is
// the bacio store's autoincrement PK. EndedAt == nil means the session
// is still alive (the agent never called `bacio agent end`). AgentID
// points at the persistent identity row in `agents`; nil for sessions
// registered before the identity layer existed.
type AgentSession struct {
	ID             int64      `json:"id"`
	SessionID      string     `json:"session_id"`
	RepoID         int64      `json:"repo_id"`
	RepoPrefix     string     `json:"repo_prefix,omitempty"`
	AgentID        *int64     `json:"agent_id,omitempty"`
	AgentName      string     `json:"agent_name,omitempty"`
	Actor          string     `json:"actor"`
	Model          string     `json:"model,omitempty"`
	PermissionMode string     `json:"permission_mode,omitempty"`
	Host           string     `json:"host,omitempty"`
	Branch         string     `json:"branch,omitempty"`
	StartedAt      time.Time  `json:"started_at"`
	LastSeenAt     time.Time  `json:"last_seen_at"`
	EndedAt        *time.Time `json:"ended_at,omitempty"`
	EndReason      string     `json:"end_reason,omitempty"`
	// ClaudePID is the pid of the `claude` process driving this session,
	// resolved by the `bacio hook` handlers. ChannelSeenAt is bumped
	// whenever a hook finds a live `bacio channel` row keyed on the same
	// (host, claude_pid) — nil/zero when no channel has ever been linked.
	ClaudePID     int64      `json:"claude_pid,omitempty"`
	ChannelSeenAt *time.Time `json:"channel_seen_at,omitempty"`
}

// AgentChannel is one live `bacio channel` subprocess. Claude Code never
// hands a channel its session id, so the channel keys itself on the
// `claude` process it descends from; the `bacio hook` handlers join
// (Host, ClaudePID) back onto a session. Pure liveness state — see the
// agent_channels schema comment.
type AgentChannel struct {
	ID         int64     `json:"id"`
	RepoID     int64     `json:"repo_id"`
	AgentID    *int64    `json:"agent_id,omitempty"`
	Host       string    `json:"host,omitempty"`
	ClaudePID  int64     `json:"claude_pid"`
	ChannelPID int64     `json:"channel_pid,omitempty"`
	StartedAt  time.Time `json:"started_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
}

// ChannelLive reports whether a channel's heartbeat is fresh enough to
// count as live, reusing AgentLivenessThreshold — a channel that polls
// every few seconds refreshes well inside the window, so a longer gap
// means the channel process is gone. now should be UTC.
func (ch *AgentChannel) ChannelLive(now time.Time) bool {
	return ch != nil && now.Sub(ch.LastSeenAt) <= AgentLivenessThreshold
}

// AgentClaim is a "this agent is focused on this issue" intent
// record. Distinct from issues.assignee (which is ownership) — multiple
// agents can claim the same issue concurrently (pairing/review).
// ReleasedAt == nil means the claim is still active.
type AgentClaim struct {
	// ID / SessionPK are server-time fields — `omitempty` so dry-run
	// projections (which can't know them yet) emit the same JSON shape
	// as real calls minus the unknown fields.
	ID         int64      `json:"id,omitempty"`
	SessionPK  int64      `json:"session_pk,omitempty"`
	SessionID  string     `json:"session_id,omitempty"`
	IssueID    int64      `json:"issue_id"`
	IssueKey   string     `json:"issue_key,omitempty"`
	ClaimedAt  time.Time  `json:"claimed_at"`
	ReleasedAt *time.Time `json:"released_at,omitempty"`
}

// EndReason values reported by `bacio agent end --reason`. Mirrors the
// Claude Code SessionEnd.end_reason set, plus "stop" for explicit
// shutdowns and "crash" for inferred ones (`agent list` flags stale
// sessions an operator might `end --reason crash` after the fact).
type EndReason string

const (
	EndReasonStop   EndReason = "stop"
	EndReasonClear  EndReason = "clear"
	EndReasonLogout EndReason = "logout"
	EndReasonCrash  EndReason = "crash"
	EndReasonOther  EndReason = "other"
)

var allEndReasons = []EndReason{
	EndReasonStop, EndReasonClear, EndReasonLogout, EndReasonCrash, EndReasonOther,
}

func AllEndReasons() []EndReason { return append([]EndReason(nil), allEndReasons...) }

// ParseEndReason accepts the canonical lowercase form and rejects
// unknown values. No dash/space normalisation — these are short
// identifiers, the agent should send them verbatim.
func ParseEndReason(s string) (EndReason, error) {
	s = strings.TrimSpace(s)
	for _, r := range allEndReasons {
		if string(r) == s {
			return r, nil
		}
	}
	names := make([]string, len(allEndReasons))
	for i, r := range allEndReasons {
		names[i] = string(r)
	}
	return "", fmt.Errorf("unknown end_reason %q (valid: %s)", s, strings.Join(names, ", "))
}

// DispatchStatus tracks a dispatch through its lifecycle. pending: not
// yet seen by the agent. delivered: drained into a session (by a hook)
// or pushed (by a channel) but not acted on. acked: the agent reported
// back via `bacio agent ack`. cancelled: the supervisor withdrew it.
type DispatchStatus string

const (
	DispatchPending   DispatchStatus = "pending"
	DispatchDelivered DispatchStatus = "delivered"
	DispatchAcked     DispatchStatus = "acked"
	DispatchCancelled DispatchStatus = "cancelled"
)

var allDispatchStatuses = []DispatchStatus{
	DispatchPending, DispatchDelivered, DispatchAcked, DispatchCancelled,
}

func AllDispatchStatuses() []DispatchStatus {
	return append([]DispatchStatus(nil), allDispatchStatuses...)
}

// ParseDispatchStatus accepts the canonical lowercase form and rejects
// unknown values. Used when a status arrives as a filter argument.
func ParseDispatchStatus(s string) (DispatchStatus, error) {
	s = strings.TrimSpace(s)
	for _, st := range allDispatchStatuses {
		if string(st) == s {
			return st, nil
		}
	}
	names := make([]string, len(allDispatchStatuses))
	for i, st := range allDispatchStatuses {
		names[i] = string(st)
	}
	return "", fmt.Errorf("unknown dispatch status %q (valid: %s)", s, strings.Join(names, ", "))
}

// DispatchMode marks the intent of a dispatch — one per stage of working
// a job. plan: investigate the issue and produce an implementation plan,
// don't change code. implement: carry the work through end-to-end.
// review: assess finished work, don't change code. ship: final checks,
// commit, open/update the PR. fix_review: address review feedback and
// push the fixes. "" = untyped (the pre-Mode default; delivery treats
// it as unspecified).
type DispatchMode string

const (
	DispatchModePlan      DispatchMode = "plan"
	DispatchModeImplement DispatchMode = "implement"
	DispatchModeReview    DispatchMode = "review"
	DispatchModeShip      DispatchMode = "ship"
	DispatchModeFixReview DispatchMode = "fix_review"
)

var allDispatchModes = []DispatchMode{
	DispatchModePlan, DispatchModeImplement, DispatchModeReview,
	DispatchModeShip, DispatchModeFixReview,
}

func AllDispatchModes() []DispatchMode {
	return append([]DispatchMode(nil), allDispatchModes...)
}

// ParseDispatchMode accepts "" (untyped — valid) or one of the canonical
// stage names, and rejects anything else.
func ParseDispatchMode(s string) (DispatchMode, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	for _, m := range allDispatchModes {
		if string(m) == s {
			return m, nil
		}
	}
	names := make([]string, len(allDispatchModes))
	for i, m := range allDispatchModes {
		names[i] = string(m)
	}
	return "", fmt.Errorf("unknown dispatch mode %q (valid: %s, or empty)", s, strings.Join(names, ", "))
}

// PromptTemplateTokens lists the placeholder tokens RenderPromptTemplate
// substitutes, in display order. Surfaced in the desktop Settings panel
// so users know what they can interpolate into a custom template.
var PromptTemplateTokens = []string{"issue_id", "issue_title", "repo_prefix"}

// DefaultPromptTemplate returns the built-in dispatch instruction
// template for a stage. These are the shipped defaults users edit from;
// an untyped or unknown mode has no template (returns "").
func DefaultPromptTemplate(mode DispatchMode) string {
	switch mode {
	case DispatchModePlan:
		return "Run a planning pass on {{issue_id}}: produce an implementation plan, don't write code yet."
	case DispatchModeImplement:
		return "Implement {{issue_id}} end-to-end."
	case DispatchModeReview:
		return "Review the work on {{issue_id}}: check correctness, tests, and adherence to the issue's acceptance criteria. Report findings, don't change code."
	case DispatchModeShip:
		return "Ship {{issue_id}}: run the final checks, commit, and open or update the PR."
	case DispatchModeFixReview:
		return "Address the review feedback on {{issue_id}} and push the fixes."
	default:
		return ""
	}
}

// RenderPromptTemplate substitutes {{token}} placeholders in tmpl from
// vars. Unknown {{...}} tokens are left untouched — a typo surfaces in
// the prompt rather than failing a dispatch. Whitespace inside the
// braces is tolerated ({{ issue_id }} resolves the same as {{issue_id}}).
func RenderPromptTemplate(tmpl string, vars map[string]string) string {
	var b strings.Builder
	for {
		i := strings.Index(tmpl, "{{")
		if i < 0 {
			b.WriteString(tmpl)
			break
		}
		end := strings.Index(tmpl[i:], "}}")
		if end < 0 {
			b.WriteString(tmpl)
			break
		}
		end += i
		key := strings.TrimSpace(tmpl[i+2 : end])
		if val, ok := vars[key]; ok {
			b.WriteString(tmpl[:i])
			b.WriteString(val)
		} else {
			b.WriteString(tmpl[:end+2]) // leave the {{...}} verbatim
		}
		tmpl = tmpl[end+2:]
	}
	return b.String()
}

// ComposeDispatchPayload builds a dispatch instruction body: the
// template rendered against vars, then an optional free-form note after
// a blank line. Either part may be empty — an empty template with no
// note yields "".
func ComposeDispatchPayload(template string, vars map[string]string, note string) string {
	body := strings.TrimSpace(RenderPromptTemplate(template, vars))
	note = strings.TrimSpace(note)
	switch {
	case body != "" && note != "":
		return body + "\n\n" + note
	case body != "":
		return body
	default:
		return note
	}
}

// AgentDispatch is one unit of supervisor->agent work. It targets an
// agent identity (TargetAgentID), a specific session (TargetSessionID),
// or both — the drain query matches on either. IssueID is the issue the
// dispatch is about, when there is one; Mode marks plan vs implement
// intent; Payload carries the (mode-derived + note) instruction body.
// Local-only, like the rest of the agent registry.
type AgentDispatch struct {
	ID              int64          `json:"id"`
	RepoID          int64          `json:"repo_id"`
	RepoPrefix      string         `json:"repo_prefix,omitempty"`
	TargetAgentID   *int64         `json:"target_agent_id,omitempty"`
	TargetAgentName string         `json:"target_agent_name,omitempty"`
	TargetSessionID string         `json:"target_session_id,omitempty"`
	IssueID         *int64         `json:"issue_id,omitempty"`
	IssueKey        string         `json:"issue_key,omitempty"`
	Mode            DispatchMode   `json:"mode,omitempty"`
	Payload         string         `json:"payload,omitempty"`
	Status          DispatchStatus `json:"status"`
	CreatedBy       string         `json:"created_by"`
	CreatedAt       time.Time      `json:"created_at"`
	DeliveredAt     *time.Time     `json:"delivered_at,omitempty"`
	AckedAt         *time.Time     `json:"acked_at,omitempty"`
	AckNote         string         `json:"ack_note,omitempty"`
}

// AgentLivenessThreshold is the gap after a session's last heartbeat
// past which it's considered idle rather than active. Heartbeats fire
// on every prompt and on the Stop hook, so a working session refreshes
// well inside this window; a longer gap means the agent is between
// turns or the harness is closed.
const AgentLivenessThreshold = 10 * time.Minute

// SessionLiveness classifies a session as "ended", "active", or "idle"
// relative to now. Shared by the TUI agent cards and the desktop Agents
// screen so both render the same status vocabulary.
func SessionLiveness(s *AgentSession, now time.Time) string {
	if s == nil || s.EndedAt != nil {
		return "ended"
	}
	if now.Sub(s.LastSeenAt) <= AgentLivenessThreshold {
		return "active"
	}
	return "idle"
}

// slug word pools for GenerateAgentSlug. Kept deliberately generic and
// G-rated; the pools just need enough combinations that two agents on
// the same host rarely collide (the store's UNIQUE on agents.name plus
// the EnsureAgentIdentity retry loop handle the rare clash that slips
// through).
var slugAdjectives = []string{
	"cheerful", "quiet", "swift", "bold", "clever", "calm", "brave", "bright",
	"eager", "gentle", "happy", "jolly", "keen", "lively", "merry", "nimble",
	"polite", "proud", "ready", "shiny", "spry", "sturdy", "sunny", "tidy",
	"witty", "zesty", "amber", "azure", "crisp", "dapper", "fleet", "frosty",
	"golden", "humble", "ivory", "lucky", "mellow", "noble", "plucky", "rapid",
	"rugged", "scarlet", "silent", "smooth", "snappy", "stout", "trusty", "vivid",
}

var slugAnimals = []string{
	"otter", "gorilla", "panda", "falcon", "lynx", "badger", "heron", "marten",
	"beaver", "bison", "cobra", "dingo", "eagle", "ferret", "gecko", "hawk",
	"ibex", "jaguar", "koala", "lemur", "mole", "newt", "owl", "puffin",
	"quail", "raven", "seal", "tapir", "urchin", "viper", "walrus", "yak",
	"zebra", "bobcat", "crane", "dolphin", "egret", "finch", "gibbon", "hare",
	"impala", "jackal", "kestrel", "leopard", "manta", "narwhal", "osprey", "weasel",
}

// GenerateAgentSlug mints a fresh identity slug of the SKILL.md shape:
// <adjective>-<animal>@claude.<short-hostname>. The host suffix keeps
// cross-machine identities apart; the adjective-animal pair is the
// random part. Callers MUST treat the result as a candidate — the
// store's UNIQUE constraint is the real collision guard.
func GenerateAgentSlug() string {
	adj := slugAdjectives[rand.IntN(len(slugAdjectives))]
	animal := slugAnimals[rand.IntN(len(slugAnimals))]
	host := "local"
	if hn, err := os.Hostname(); err == nil && hn != "" {
		// Short hostname: first label only ("shiny.local" -> "shiny").
		if i := strings.IndexByte(hn, '.'); i > 0 {
			hn = hn[:i]
		}
		host = hn
	}
	return fmt.Sprintf("%s-%s@claude.%s", adj, animal, host)
}
