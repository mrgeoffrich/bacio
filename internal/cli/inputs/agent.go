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
	SessionID   string `json:"session_id"`
	Actor       string `json:"actor"`
	Agent       string `json:"agent,omitempty"`
	NewIdentity bool   `json:"new_identity,omitempty"`
	Model       string `json:"model,omitempty"`
	Host        string `json:"host,omitempty"`
	Branch      string `json:"branch,omitempty"`
	// ClaudePID is optional: the pid of the `claude` process driving this
	// session. When non-zero it enables duplicate-registration dedupe
	// (BACI-100) — a register call whose claude_pid already has a live
	// session row under a different session_id reconciles the extras away
	// rather than minting another phantom "live" row. Zero (no walkable
	// `claude` ancestor, or a caller that doesn't supply it) skips the
	// dedupe and behaves as before.
	ClaudePID int64 `json:"claude_pid,omitempty"`
}

// AgentHeartbeatInput is the payload for `bacio agent heartbeat --json`.
// Bumps last_seen_at on an already-registered session. Same shape as
// register so a long-lived agent can send the same payload whether
// it's the first call or the hundredth.
type AgentHeartbeatInput struct {
	SessionID string `json:"session_id"`
	Model     string `json:"model,omitempty"`
	Branch    string `json:"branch,omitempty"`
}

// AgentEndInput is the payload for `bacio agent end --json`. Reason
// must be one of: stop, clear, logout, crash, other, presumed_dead.
// `presumed_dead` is normally written by the bacio idle-pinger
// reaper (BACI-57) rather than by an agent, but the CLI accepts it
// for symmetry with the model layer.
type AgentEndInput struct {
	SessionID string `json:"session_id"`
	Reason    string `json:"reason"`
}

// AgentClaimInput is the payload for `bacio agent claim --json`.
// IssueKey must be canonical (PREFIX-N). Prompt is the optional
// instruction/dispatch text the agent is working from — stored on the
// claim so the issue carries a record of who worked it and why.
type AgentClaimInput struct {
	SessionID string `json:"session_id"`
	IssueKey  string `json:"issue_key"`
	Prompt    string `json:"prompt,omitempty"`
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
// ("plan", "design", "implement", "review", "ship", "fix_review", or
// "" for untyped — or any user-created template slug); Message is an optional
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

// AgentCancelInput is the payload for `bacio agent cancel --json`. ID
// is the dispatch id. Cancelling an already-acked dispatch is an error
// (the work was acknowledged); cancelling an already-cancelled
// dispatch is a no-op. If the dispatch targets an issue, the issue's
// waiting_for_claim flag is cleared in the same transaction.
type AgentCancelInput struct {
	ID int64 `json:"id"`
}

// IssueDispatchInput is the payload for the state-gated auto-pick
// dispatch verb (BACI-40): `POST /repos/{prefix}/issues/{key}/dispatch`
// and the target-less `bacio agent dispatch <key> --mode <stage>`. Mode
// is the only field — the server resolves the issue from the URL/positional,
// re-checks the stage's state-gate against the issue's current state,
// and auto-picks the most-recently-active free agent.
type IssueDispatchInput struct {
	Mode string `json:"mode"`
}

// AgentQuestionsListInput is the payload for
// `bacio agent questions list --json`. SessionID scopes the list to
// one session id (defaults to $CLAUDE_CODE_SESSION_ID on the CLI
// flag path). When empty AND AllSessions is false, the call errors
// — the caller has to be explicit about scope to avoid surprising
// cross-session reads. States is the lifecycle filter; an empty
// list returns open questions only (the most useful default for an
// agent or skill checking "is the user being asked anything
// right now"). Pass {"open","answered","cancelled","abandoned"}
// for "every state".
type AgentQuestionsListInput struct {
	SessionID   string   `json:"session_id,omitempty"`
	AllSessions bool     `json:"all_sessions,omitempty"`
	States      []string `json:"states,omitempty"`
}

// AgentQuestionsShowInput is the payload for
// `bacio agent questions show --json`. ID is the row's primary key
// (as printed by `bacio agent questions list`).
type AgentQuestionsShowInput struct {
	ID int64 `json:"id"`
}

// AgentQuestionsAnswerInput is the payload for
// `bacio agent questions answer --json`. ID is the question row's
// primary key; Answers maps question text -> answer label (or
// []string for a multi-select question — JSON arrays decode into
// []any which the validator accepts). The store re-validates the
// shape against the stored payload at the boundary.
type AgentQuestionsAnswerInput struct {
	ID      int64          `json:"id"`
	Answers map[string]any `json:"answers"`
}

// AgentQuestionsCancelInput is the payload for
// `bacio agent questions cancel --json`. Cancelling an
// already-cancelled / already-answered question is an error.
type AgentQuestionsCancelInput struct {
	ID int64 `json:"id"`
}
