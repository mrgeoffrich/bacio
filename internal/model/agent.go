package model

import (
	"fmt"
	"strings"
	"time"
)

// AgentSession is one running instance of an AI agent (typically a
// Claude Code session) talking to a bacio repo. The registry is
// local-only — never synced to GitHub — so it's safe to record
// machine-specific fields like host and branch.
//
// SessionID is the external id (e.g. CLAUDE_CODE_SESSION_ID); ID is
// the bacio store's autoincrement PK. EndedAt == nil means the session
// is still alive (the agent never called `bacio agent end`).
type AgentSession struct {
	ID             int64      `json:"id"`
	SessionID      string     `json:"session_id"`
	RepoID         int64      `json:"repo_id"`
	RepoPrefix     string     `json:"repo_prefix,omitempty"`
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
