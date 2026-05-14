package inputs

// AgentRegisterInput is the payload for `bacio agent register --json`.
// session_id, actor are required. Mutable fields (model, mode, host,
// branch) are overwritten on every register/heartbeat — pass them only
// when they apply.
//
// Agent is the persistent identity slug (e.g. "cheerful-otter@claude.shiny");
// optional but recommended — without it the session has no link to a
// long-lived identity, so cross-session correlation falls back to actor
// matching. Set NewIdentity true on the first register of a freshly
// generated slug — bacio will then error with "agent name taken" if it
// clashes with another agent's, prompting the agent to regenerate.
// Leave NewIdentity false on subsequent registers of a known slug; the
// upsert is then idempotent. (With hooks installed, the session-start
// hook handles all of this — see SKILL.md.)
type AgentRegisterInput struct {
	SessionID      string `json:"session_id"`
	Actor          string `json:"actor"`
	Agent          string `json:"agent,omitempty"`
	NewIdentity    bool   `json:"new_identity,omitempty"`
	Model          string `json:"model,omitempty"`
	PermissionMode string `json:"permission_mode,omitempty"`
	Host           string `json:"host,omitempty"`
	Branch         string `json:"branch,omitempty"`
}

// AgentHeartbeatInput is the payload for `bacio agent heartbeat --json`.
// Bumps last_seen_at on an already-registered session. Same shape as
// register so a long-lived agent can send the same payload whether
// it's the first call or the hundredth.
type AgentHeartbeatInput struct {
	SessionID      string `json:"session_id"`
	Model          string `json:"model,omitempty"`
	PermissionMode string `json:"permission_mode,omitempty"`
	Branch         string `json:"branch,omitempty"`
}

// AgentEndInput is the payload for `bacio agent end --json`. Reason
// must be one of: stop, clear, logout, crash, other.
type AgentEndInput struct {
	SessionID string `json:"session_id"`
	Reason    string `json:"reason"`
}

// AgentClaimInput is the payload for `bacio agent claim --json`.
// IssueKey must be canonical (PREFIX-N).
type AgentClaimInput struct {
	SessionID string `json:"session_id"`
	IssueKey  string `json:"issue_key"`
}

// AgentReleaseInput is the payload for `bacio agent release --json`.
type AgentReleaseInput struct {
	SessionID string `json:"session_id"`
	IssueKey  string `json:"issue_key"`
}

// AgentDispatchInput is the payload for `bacio agent dispatch --json`.
// A dispatch must name a target: TargetAgent (a persistent identity
// slug), TargetSession (a session id), or both. IssueKey is the issue
// the dispatch concerns, when there is one. Mode is the dispatch intent
// ("plan", "implement", or "" for untyped); Message is an optional
// free-form note. The instruction body the agent sees is the mode's
// canned text plus the note.
type AgentDispatchInput struct {
	TargetAgent   string `json:"target_agent,omitempty"`
	TargetSession string `json:"target_session,omitempty"`
	IssueKey      string `json:"issue_key,omitempty"`
	Mode          string `json:"mode,omitempty"`
	Message       string `json:"message,omitempty"`
}

// AgentAckInput is the payload for `bacio agent ack --json`. ID is the
// dispatch id (as printed by `bacio agent inbox`). Note is an optional
// free-form reply recorded against the dispatch.
type AgentAckInput struct {
	ID   int64  `json:"id"`
	Note string `json:"note,omitempty"`
}
