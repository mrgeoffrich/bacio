package model

import (
	"embed"
	"fmt"
	"math/rand/v2"
	"os"
	"regexp"
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
	ID         int64  `json:"id"`
	SessionID  string `json:"session_id"`
	RepoID     int64  `json:"repo_id"`
	RepoPrefix string `json:"repo_prefix,omitempty"`
	AgentID    *int64 `json:"agent_id,omitempty"`
	AgentName  string `json:"agent_name,omitempty"`
	Actor      string `json:"actor"`
	Model      string `json:"model,omitempty"`
	Host       string `json:"host,omitempty"`
	Branch     string `json:"branch,omitempty"`
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
	// RegisteredAt is set when the agent calls the bacio channel's
	// `register` tool — distinguishes a fully-registered session from a
	// SessionStart-stub waiting to be enriched. UI agent lists default
	// to filtering on this being non-nil.
	RegisteredAt *time.Time `json:"registered_at,omitempty"`
	// ChannelVersion is the bacio binary version reported by the channel
	// the agent talked to at register time — for spotting stale channel
	// processes still running after a binary upgrade. Empty until the
	// agent registers (or for sessions older than the version-reporting
	// change).
	ChannelVersion string `json:"channel_version,omitempty"`
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

// TodoStatus is the lifecycle marker on each TodoWrite item — the
// agent's plan-list status vocabulary. Matches the Claude Code
// PostToolUse payload verbatim.
type TodoStatus string

const (
	TodoPending    TodoStatus = "pending"
	TodoInProgress TodoStatus = "in_progress"
	TodoCompleted  TodoStatus = "completed"
)

var allTodoStatuses = []TodoStatus{TodoPending, TodoInProgress, TodoCompleted}

// ParseTodoStatus accepts the canonical lowercase form. Anything else
// fails — the hook handler drops the whole batch on an unknown status
// rather than mirror a partial list (a misleading mirror is worse than
// none). New statuses surface here as a deliberate update.
func ParseTodoStatus(s string) (TodoStatus, error) {
	s = strings.TrimSpace(s)
	for _, st := range allTodoStatuses {
		if string(st) == s {
			return st, nil
		}
	}
	names := make([]string, len(allTodoStatuses))
	for i, st := range allTodoStatuses {
		names[i] = string(st)
	}
	return "", fmt.Errorf("unknown todo status %q (valid: %s)", s, strings.Join(names, ", "))
}

// SessionTodo is one row from the agent's current task list, mirrored
// into bacio by the post-tool-use hook (driven by TaskCreate +
// TaskUpdate events; see internal/cli/hook.go). Read-only mirror —
// bacio does not edit the agent's plan. Position is 0-indexed and
// matches the order tasks were created. TaskID is the Claude Code-
// assigned id (empty string on legacy TodoWrite rows, which can't be
// updated in place). IssueKey is the canonical issue key the session
// was claiming when the row was inserted (empty when zero or many
// claims were open at hook time, or for rows written before BACI-62);
// the Agents view filters per-(session, issue) so a session that
// handles two dispatches back-to-back doesn't show the first job's
// completed rows on the second job's card.
type SessionTodo struct {
	Position  int        `json:"position"`
	Content   string     `json:"content"`
	Status    TodoStatus `json:"status"`
	TaskID    string     `json:"task_id,omitempty"`
	IssueKey  string     `json:"issue_key,omitempty"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// MaxSessionTodos caps how many todo rows a single agent session may
// mirror. Generous for any realistic plan; tight enough that a runaway
// agent writing nonsense gets rejected wholesale at the store boundary
// rather than silently truncated (a partial mirror is worse than no
// mirror — the UI suggests "this is the agent's plan" when actually
// it's a clipped subset).
const MaxSessionTodos = 200

// AgentClaim is a "this agent is focused on this issue" intent
// record. Distinct from issues.assignee (which is ownership) — multiple
// agents can claim the same issue concurrently (pairing/review).
// ReleasedAt == nil means the claim is still active.
type AgentClaim struct {
	// ID / SessionPK are server-time fields — `omitempty` so dry-run
	// projections (which can't know them yet) emit the same JSON shape
	// as real calls minus the unknown fields.
	ID        int64  `json:"id,omitempty"`
	SessionPK int64  `json:"session_pk,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	IssueID   int64  `json:"issue_id"`
	IssueKey  string `json:"issue_key,omitempty"`
	// AgentName is the persistent identity slug behind the claiming
	// session, joined in by the per-issue claim list so a reader can see
	// who worked an issue without a second lookup. Empty for sessions
	// registered before the identity layer existed.
	AgentName string `json:"agent_name,omitempty"`
	// Prompt is the instruction/dispatch text the agent was working from
	// when it claimed the issue. Empty for claims made without one.
	Prompt     string     `json:"prompt,omitempty"`
	ClaimedAt  time.Time  `json:"claimed_at"`
	ReleasedAt *time.Time `json:"released_at,omitempty"`
}

// AnyOpenClaim reports whether claims has at least one entry with
// ReleasedAt == nil — the derived "taken" signal used by per-issue
// views. Mirrors the bulk shape that store.OpenClaimsBySession produces
// (already pre-filtered to open), but operates on raw
// store.ListClaimsForIssue results where the slice may include released
// entries. A nil entry in the slice is tolerated; nil claims are
// ignored. Use this anywhere a "did any agent claim this issue?" check
// is needed; the inline `c.ReleasedAt == nil` pattern was reimplemented
// in five sites before this helper landed.
func AnyOpenClaim(claims []*AgentClaim) bool {
	for _, c := range claims {
		if c != nil && c.ReleasedAt == nil {
			return true
		}
	}
	return false
}

// SessionBusy reports whether a session is actively holding a job —
// busy iff it has at least one open (unreleased) claim. issueKey is the
// most-recently-claimed open issue, for a "busy (working BACI-12)"
// label. Busy is orthogonal to SessionLiveness: a session can be
// active+busy or idle+busy. openClaims must already be filtered to open
// claims for one session.
func SessionBusy(openClaims []*AgentClaim) (busy bool, issueKey string) {
	var newest *AgentClaim
	for _, c := range openClaims {
		if c == nil || c.ReleasedAt != nil {
			continue
		}
		if newest == nil || c.ClaimedAt.After(newest.ClaimedAt) {
			newest = c
		}
	}
	if newest == nil {
		return false, ""
	}
	return true, newest.IssueKey
}

// SessionWaiting reports whether a session is parked waiting on the
// user — true iff it holds an open claim on an issue whose state is in
// needsActionKeys. The Stop hook auto-flips claimed in_progress issues
// to needs_action, so this is the derived "parked" signal for the
// Agents UI. issueKey is the most-recently-claimed waiting issue, for a
// "waiting · BACI-12" badge. openClaims must already be filtered to
// open claims for one session; needsActionKeys is a set of issue keys
// (PREFIX-N) currently in state needs_action.
func SessionWaiting(openClaims []*AgentClaim, needsActionKeys map[string]bool) (waiting bool, issueKey string) {
	var newest *AgentClaim
	for _, c := range openClaims {
		if c == nil || c.ReleasedAt != nil {
			continue
		}
		if !needsActionKeys[c.IssueKey] {
			continue
		}
		if newest == nil || c.ClaimedAt.After(newest.ClaimedAt) {
			newest = c
		}
	}
	if newest == nil {
		return false, ""
	}
	return true, newest.IssueKey
}

// EndReason values reported by `bacio agent end --reason`. Mirrors the
// Claude Code SessionEnd.end_reason set, plus "stop" for explicit
// shutdowns, "crash" for inferred ones (`agent list` flags stale
// sessions an operator might `end --reason crash` after the fact), and
// "presumed_dead" for sessions the bacio idle-pinger reaped after they
// failed to ack an idle-check ping within AgentPingNoAckTimeout (BACI-57).
type EndReason string

const (
	EndReasonStop         EndReason = "stop"
	EndReasonClear        EndReason = "clear"
	EndReasonLogout       EndReason = "logout"
	EndReasonCrash        EndReason = "crash"
	EndReasonOther        EndReason = "other"
	EndReasonPresumedDead EndReason = "presumed_dead"
)

var allEndReasons = []EndReason{
	EndReasonStop, EndReasonClear, EndReasonLogout, EndReasonCrash, EndReasonOther,
	EndReasonPresumedDead,
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

// DispatchStatus tracks a dispatch through its lifecycle. queued: an
// auto-pick dispatch waiting for the background matcher to bind it to
// a free agent (BACI-51 — only the auto-pick path queues; targeted
// dispatches go straight to pending). pending: bound to a target,
// not yet seen by the agent. delivered: drained into a session (by a
// hook) or pushed (by a channel) but not acted on. acked: the agent
// reported back via `bacio agent ack`. cancelled: the supervisor
// withdrew it (valid from queued, pending, or delivered).
type DispatchStatus string

const (
	DispatchQueued    DispatchStatus = "queued"
	DispatchPending   DispatchStatus = "pending"
	DispatchDelivered DispatchStatus = "delivered"
	DispatchAcked     DispatchStatus = "acked"
	DispatchCancelled DispatchStatus = "cancelled"
)

// allDispatchStatuses lists the statuses in lifecycle order, with
// `queued` first so JSON enumerations and CLI help text lead with the
// initial state.
var allDispatchStatuses = []DispatchStatus{
	DispatchQueued, DispatchPending, DispatchDelivered, DispatchAcked, DispatchCancelled,
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

// DispatchMode is the slug of the prompt template a dispatch was queued
// against — for the built-in templates that ship with bacio it's one of
// "plan", "design", "implement", "review", "ship", or "fix_review"; for
// a user-created template it's whatever the user named it. "" = untyped (the
// pre-Mode default; delivery treats it as unspecified). The mode is the
// per-dispatch snapshot of which template was used; it deliberately
// outlives the template (a deleted template's historical dispatches
// keep the slug verbatim — renderers should treat an unrecognised slug
// as "removed", not error out). The shape rule is a slug
// (^[a-z0-9][a-z0-9-_]*$) capped at 60 chars; the registered set lives
// in the prompt_templates store table, not this enum.
type DispatchMode string

// Built-in template slugs that ship with bacio. They have embedded
// default bodies at prompttemplates/<slug>.txt and entries in
// builtinPromptStates / builtinTemplateLabels. Users can edit or delete
// any of them; `bacio settings template restore-defaults` re-seeds the
// missing ones. Order matches a job's lifecycle, which is also the
// seed order so the built-ins lead the list on a fresh install.
//
// BuiltinTemplatePreamble is a reserved slug (leading underscore) that
// is NOT pickable as a dispatch mode — its body is the BACI-52
// delegation wrapper, prepended to every other template's payload at
// dispatch compose time. It has no state-gate, which is also why the
// per-card mode picker (which filters by state-gate match) excludes
// it naturally.
const (
	BuiltinTemplatePreamble  = "_dispatch_preamble"
	BuiltinTemplatePlan      = "plan"
	BuiltinTemplateDesign    = "design"
	BuiltinTemplateImplement = "implement"
	BuiltinTemplateReview    = "review"
	BuiltinTemplateShip      = "ship"
	BuiltinTemplateFixReview = "fix_review"
)

// DispatchModePlan etc. are deprecated aliases — kept so older callers
// that still reference the typed constants keep compiling. New code
// should use the BuiltinTemplate* string constants above and the
// runtime slug values from the prompt_templates table.
const (
	DispatchModePlan      DispatchMode = BuiltinTemplatePlan
	DispatchModeImplement DispatchMode = BuiltinTemplateImplement
	DispatchModeReview    DispatchMode = BuiltinTemplateReview
	DispatchModeShip      DispatchMode = BuiltinTemplateShip
	DispatchModeFixReview DispatchMode = BuiltinTemplateFixReview
)

// builtinTemplateSlugs lists the bundled built-in template slugs in
// canonical lifecycle order — used by the migration's first-time seed
// step and by `restore-defaults` to know what to re-create. The
// reserved BuiltinTemplatePreamble row leads the list so it lands at
// the top of the Settings UI on a fresh install (the list is ordered
// by created_at, id).
var builtinTemplateSlugs = []string{
	BuiltinTemplatePreamble,
	BuiltinTemplatePlan, BuiltinTemplateDesign, BuiltinTemplateImplement,
	BuiltinTemplateReview, BuiltinTemplateShip, BuiltinTemplateFixReview,
}

// BuiltinTemplateSlugs returns the bundled template slugs in canonical
// lifecycle order. Copy of the package-private list so callers can
// iterate without mutating shared state.
func BuiltinTemplateSlugs() []string {
	return append([]string(nil), builtinTemplateSlugs...)
}

// builtinTemplateLabels gives each built-in slug a human display label
// — used by the seed step (the table's `name` column) and as a fallback
// when a UI surfaces a slug whose stored row is missing.
var builtinTemplateLabels = map[string]string{
	BuiltinTemplatePreamble:  "Dispatch preamble (prepended to every job)",
	BuiltinTemplatePlan:      "Planning",
	BuiltinTemplateDesign:    "Designing",
	BuiltinTemplateImplement: "Implementing",
	BuiltinTemplateReview:    "Reviewing",
	BuiltinTemplateShip:      "Shipping",
	BuiltinTemplateFixReview: "Fixing a review",
}

// BuiltinTemplateLabel returns the human display label for a built-in
// template slug, or "" if the slug isn't a built-in. Callers should
// fall back to the row's stored Name (or the slug itself) when this
// returns empty.
func BuiltinTemplateLabel(slug string) string {
	return builtinTemplateLabels[slug]
}

// BuiltinTemplateShipConcurrency is the per-(repo, mode) cap the matcher
// applies to ship-it by default (BACI-51). 1 = at most one ship-it
// dispatch in flight per repo, so merging is serialised — the user
// doesn't need to remember to wait between ships. Users can change it
// via `bacio settings template set-concurrency ship <n>` or the
// equivalent UIs.
const BuiltinTemplateShipConcurrency = 1

// DefaultConcurrencyLimit is the built-in concurrency_limit seed for a
// template slug — what the schema migration writes for a freshly-seeded
// built-in. 0 (unlimited) for everything except the BACI-51 ship-it
// guard. Returns 0 for any non-built-in slug (a user-created template
// defaults to unlimited).
func DefaultConcurrencyLimit(slug string) int {
	if slug == BuiltinTemplateShip {
		return BuiltinTemplateShipConcurrency
	}
	return 0
}

// dispatchModeSlugRule allows a slightly broader shape than feature
// slugs: kebab- AND snake-case both work, so the built-in `fix_review`
// is a valid slug. A leading underscore is also allowed — it's the
// "reserved / internal" convention used by built-ins that aren't
// pickable as a dispatch mode (e.g. `_dispatch_preamble`, the wrapper
// prepended to every other template's payload). Length cap is enforced
// separately by validators.
var dispatchModeSlugRule = regexp.MustCompile(`^[a-z0-9_][a-z0-9_-]*$`)

// ParseDispatchMode accepts "" (untyped — valid) or any slug-shaped
// string ([a-z0-9_][a-z0-9_-]*, max 60 chars). It does NOT check whether
// the slug is registered in the prompt_templates table — that's a store
// concern (and a dispatch may legitimately carry a slug for a template
// the user has since deleted). Renderers should treat an unrecognised
// but slug-shaped value as "removed", not error out.
func ParseDispatchMode(s string) (DispatchMode, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	if len(s) > 60 {
		return "", fmt.Errorf("dispatch mode %q is too long (max 60 chars)", s)
	}
	if !dispatchModeSlugRule.MatchString(s) {
		return "", fmt.Errorf("dispatch mode %q must be a slug (lowercase letters, digits, hyphens, underscores; the first character may also be an underscore for reserved/internal slugs)", s)
	}
	return DispatchMode(s), nil
}

// PromptTemplateTokens lists the placeholder tokens RenderPromptTemplate
// substitutes, in display order. Surfaced in the desktop Settings panel
// so users know what they can interpolate into a custom template.
var PromptTemplateTokens = []string{"issue_id", "issue_title", "repo_prefix"}

// promptTemplateFS embeds the shipped default dispatch templates, one
// plain-text file per stage (prompttemplates/<mode>.txt). Editing those
// files is how you change a built-in default — no Go change needed.
//
//go:embed prompttemplates/*.txt
var promptTemplateFS embed.FS

// defaultPromptBodies is the per-slug built-in template body, loaded
// once from promptTemplateFS at package init. Keyed by built-in slug
// (a non-built-in slug has no entry and returns "" via the lookups).
var defaultPromptBodies = loadDefaultPromptBodies()

// loadDefaultPromptBodies reads prompttemplates/<slug>.txt for every
// built-in slug. A missing or blank file is a packaging error, so it
// panics — the files are embedded, so this can only fail at build time.
func loadDefaultPromptBodies() map[string]string {
	out := make(map[string]string, len(builtinTemplateSlugs))
	for _, slug := range builtinTemplateSlugs {
		b, err := promptTemplateFS.ReadFile("prompttemplates/" + slug + ".txt")
		if err != nil {
			panic(fmt.Sprintf("model: missing built-in prompt template for slug %q: %v", slug, err))
		}
		t := strings.TrimRight(string(b), "\r\n")
		if strings.TrimSpace(t) == "" {
			panic(fmt.Sprintf("model: built-in prompt template for slug %q is empty", slug))
		}
		out[slug] = t
	}
	return out
}

// DefaultPromptBodyForBuiltinSlug returns the embedded default body for
// a built-in template slug, or "" for a non-built-in slug. Used by the
// store migration's seed step and by `bacio settings template reset`
// (which is only meaningful for built-ins).
func DefaultPromptBodyForBuiltinSlug(slug string) string {
	return defaultPromptBodies[slug]
}

// DefaultPromptTemplate is a legacy alias for
// DefaultPromptBodyForBuiltinSlug, kept so older typed-DispatchMode
// callers (TUI / desktop seed paths, audit-row comparisons) keep
// compiling during the BACI-31 transition.
//
// Deprecated: use DefaultPromptBodyForBuiltinSlug with a slug string.
func DefaultPromptTemplate(mode DispatchMode) string {
	return DefaultPromptBodyForBuiltinSlug(string(mode))
}

// builtinPromptStates is the built-in "this prompt is valid to run from
// these issue states" gate, per built-in slug. It mirrors a job's
// lifecycle: planning/implementing start from a todo issue; reviewing,
// shipping, and fixing-a-review happen once the work is in review.
// Users override these per-template; the override lives in the
// prompt_templates table.
var builtinPromptStates = map[string][]State{
	BuiltinTemplatePlan:      {StateTodo},
	BuiltinTemplateDesign:    {StateTodo},
	BuiltinTemplateImplement: {StateTodo},
	BuiltinTemplateReview:    {StateInReview},
	BuiltinTemplateShip:      {StateInReview},
	BuiltinTemplateFixReview: {StateInReview},
}

// DefaultPromptStatesForBuiltinSlug returns the built-in set of issue
// states a built-in template's prompt is valid to run from. A non-
// built-in slug has no entry (returns an empty slice). The returned
// slice is a copy — callers may mutate it freely.
func DefaultPromptStatesForBuiltinSlug(slug string) []State {
	return append([]State(nil), builtinPromptStates[slug]...)
}

// DefaultPromptStates is a legacy alias for
// DefaultPromptStatesForBuiltinSlug, kept so older typed-DispatchMode
// callers keep compiling during the BACI-31 transition.
//
// Deprecated: use DefaultPromptStatesForBuiltinSlug with a slug string.
func DefaultPromptStates(mode DispatchMode) []State {
	return DefaultPromptStatesForBuiltinSlug(string(mode))
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

// ComposeDispatchPayload builds a dispatch instruction body in the
// shape the agent receives it: the optional preamble (BACI-52 wrapper
// instructing the parent session to delegate to a subagent), a "---"
// separator, the template rendered against vars, and an optional
// free-form note after a blank line. Each part is independently
// optional:
//
//   - preamble == "": skip the preamble + separator (used today by the
//     channel's setup-register dispatch and by note-only dispatches,
//     since there's no work brief there worth delegating).
//   - template == "" (untyped dispatch): payload is just the note, no
//     preamble — the agent isn't being handed a job to delegate.
//   - note == "": payload is preamble + body, no trailing freeform line.
//
// preamble is rendered through RenderPromptTemplate too so future
// preamble bodies can interpolate {{issue_id}} etc. without changing
// the call shape.
func ComposeDispatchPayload(preamble, template string, vars map[string]string, note string) string {
	preamble = strings.TrimSpace(RenderPromptTemplate(preamble, vars))
	body := strings.TrimSpace(RenderPromptTemplate(template, vars))
	note = strings.TrimSpace(note)

	if body == "" {
		// No template body = no work brief to delegate = no preamble.
		// Untyped dispatch — just the freeform note (which may itself
		// be empty, in which case the payload is "").
		return note
	}

	parts := make([]string, 0, 4)
	if preamble != "" {
		parts = append(parts, preamble, "---", body)
	} else {
		parts = append(parts, body)
	}
	if note != "" {
		parts = append(parts, note)
	}
	return strings.Join(parts, "\n\n")
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

// AgentIdlePingThreshold is the gap after a session's last heartbeat
// at which the controlling UI starts asking the agent "are you still
// alive?" via a channel-pushed ping dispatch (BACI-57). Longer than
// AgentLivenessThreshold (which only flips the UI's "idle" badge)
// because the ping is a real action — we want a high-confidence
// "this looks dead" signal before disturbing live sessions.
const AgentIdlePingThreshold = 1 * time.Hour

// AgentPingNoAckTimeout is how long after a ping is enqueued the
// reaper waits before declaring the session presumed-dead and
// force-ending it. The channel pushes the ping inside one drain tick
// (sub-second under normal load); 2 minutes is generous enough to
// absorb a single network blip or a parked-but-alive turn while
// keeping the dead-session cleanup window tight.
const AgentPingNoAckTimeout = 2 * time.Minute

// SetupDispatchCreator marks dispatches that the bacio channel itself
// enqueued to ask the agent to call the `register` tool. The BACI-51
// matcher excludes these from its concurrency-count query so a setup
// nudge never blocks a real ship-it dispatch from binding. Lives in
// model so the store can reference it without importing client.
const SetupDispatchCreator = "bacio-channel"

// IdlePingDispatchCreator marks dispatches the bacio idle-pinger
// (BACI-57) enqueued to probe whether a long-idle session is still
// alive. Mirrors the SetupDispatchCreator pattern so EnsurePingDispatch
// stays idempotent (skip when a pending|delivered ping already exists
// for the session) and `bacio history` can distinguish reaper-driven
// pings from human dispatches.
const IdlePingDispatchCreator = "bacio-channel-ping"

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
