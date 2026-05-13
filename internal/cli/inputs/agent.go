package inputs

// AgentRegisterInput is the payload for `bacio agent register --json`.
// session_id, actor are required. Mutable fields (model, mode, host,
// branch) are overwritten on every register/heartbeat — pass them only
// when they apply.
type AgentRegisterInput struct {
	SessionID      string `json:"session_id"`
	Actor          string `json:"actor"`
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
