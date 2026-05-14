package model

import (
	"fmt"
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

// AgentDispatch is one unit of supervisor->agent work. It targets an
// agent identity (TargetAgentID), a specific session (TargetSessionID),
// or both — the drain query matches on either. IssueID is the issue the
// dispatch is about, when there is one; Payload carries free-form
// instructions. Local-only, like the rest of the agent registry.
type AgentDispatch struct {
	ID              int64          `json:"id"`
	RepoID          int64          `json:"repo_id"`
	RepoPrefix      string         `json:"repo_prefix,omitempty"`
	TargetAgentID   *int64         `json:"target_agent_id,omitempty"`
	TargetAgentName string         `json:"target_agent_name,omitempty"`
	TargetSessionID string         `json:"target_session_id,omitempty"`
	IssueID         *int64         `json:"issue_id,omitempty"`
	IssueKey        string         `json:"issue_key,omitempty"`
	Payload         string         `json:"payload,omitempty"`
	Status          DispatchStatus `json:"status"`
	CreatedBy       string         `json:"created_by"`
	CreatedAt       time.Time      `json:"created_at"`
	DeliveredAt     *time.Time     `json:"delivered_at,omitempty"`
	AckedAt         *time.Time     `json:"acked_at,omitempty"`
	AckNote         string         `json:"ack_note,omitempty"`
}
