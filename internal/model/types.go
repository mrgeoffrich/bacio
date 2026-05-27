package model

import "time"

type Repo struct {
	ID              int64     `json:"id"`
	UUID            string    `json:"uuid"`
	Prefix          string    `json:"prefix"`
	Name            string    `json:"name"`
	Path            string    `json:"path"`
	RemoteURL       string    `json:"remote_url,omitempty"`
	NextIssueNumber int64     `json:"next_issue_number"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type Feature struct {
	ID          int64  `json:"id"`
	UUID        string `json:"uuid"`
	RepoID      int64  `json:"repo_id"`
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	// Emoji (BACI-172) is the per-feature single-glyph brand rendered
	// in the top-left of every kanban card whose issue belongs to this
	// feature. Empty when no glyph is set — the card just renders
	// nothing in the slot. Validated at the store boundary to be either
	// empty or exactly one grapheme cluster.
	Emoji string `json:"emoji,omitempty"`
	// BranchName (BACI-225) is the integration branch this feature
	// ships against. nil = ship straight to main (the legacy default
	// — single global ship-it concurrency slot). A non-nil value is
	// the foundation for the BACI-226+ follow-ons that scope ship
	// concurrency per-branch, base the worker on the right ref, and
	// merge the branch back to main at feature-done. Validated at the
	// store boundary by ValidateBranchName (git ref rules: no
	// whitespace, no control chars, no `..`, `@{`, etc.). Pointer
	// matches ArchivedAt's nullable shape.
	BranchName *string `json:"branch_name,omitempty"`
	// ArchivedAt (BACI-68) is non-nil iff the feature is archived —
	// hidden from default lists, but the row and its audit history
	// remain. The auto-sweep stamps it when every child issue is
	// archived; manual `bacio feature archive` / `unarchive` writes or
	// clears it on demand.
	ArchivedAt *time.Time `json:"archived_at,omitempty"`
	// State (BACI-199) is the three-state column on the feature row:
	// `active` (default — work in flight), `done` (delivered) or
	// `cancelled` (abandoned). Manual writes via `bacio feature state`
	// flip this column and set StateManual = true; the leader-elected
	// archive sweep's auto-completion pass promotes
	// `active`-with-every-child-terminal features but only when
	// StateManual is false, so a user-set value is never overwritten
	// without explicit action. Archive (ArchivedAt) stays orthogonal:
	// a `done` feature is visible by default until explicitly archived.
	// No omitempty — the field is always visible in JSON (existing
	// features default to `active`) so the React Features view reads
	// the column directly.
	State FeatureState `json:"state"`
	// StateManual (BACI-199) is the sticky-bit set whenever a user
	// explicitly flips State via `bacio feature state`. The sweep's
	// auto-completion pass skips rows with StateManual = true so a
	// manually-cancelled feature whose children later finish doesn't
	// silently flip back to `done`. No omitempty — same reason as
	// State.
	StateManual bool      `json:"state_manual"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	// HiddenOnBoard (BACI-177) reflects the per-feature "Show on
	// board" toggle exposed on the Features screen — true iff every
	// kanban card belonging to this feature is hidden from the board.
	// The flag lives in the per-repo `tui_settings` KV (not on the
	// features row); this field is derived post-scan by callers that
	// opt in via FeatureFilter.WithHiddenOnBoard. Defaults to false
	// on reads that don't ask — the field never lies, it just always
	// reads false when the caller didn't request enrichment. No
	// omitempty: the toggle is visible (including when false) in JSON
	// output so the React component reads it without an `?? false`.
	HiddenOnBoard bool `json:"hidden_on_board"`
}

type Issue struct {
	ID          int64     `json:"id"`
	UUID        string    `json:"uuid"`
	RepoID      int64     `json:"repo_id"`
	Number      int64     `json:"number"`
	Key         string    `json:"key"` // e.g. "MINI-42"
	FeatureID    *int64 `json:"feature_id,omitempty"`
	FeatureSlug  string `json:"feature_slug,omitempty"`
	// FeatureEmoji (BACI-172) is the per-feature glyph denormalised
	// onto the issue row via the feature join in issueSelect. Empty
	// when the issue has no feature, or when the feature has no
	// emoji set. The kanban / BoardCard render reads this directly
	// so the card row can decorate without a second lookup.
	FeatureEmoji string `json:"feature_emoji,omitempty"`
	Title       string    `json:"title"`
	Description string    `json:"description,omitempty"`
	State       State     `json:"state"`
	Assignee    string    `json:"assignee,omitempty"`
	// WaitingForClaim is true between a dispatch being queued against
	// this issue and an agent recording an open claim on it. Ephemeral
	// runtime state — set by store.AddDispatch, cleared by
	// store.AddAgentClaim / store.CancelDispatch. No omitempty: the
	// field must be visible (including when false) in JSON output.
	WaitingForClaim bool `json:"waiting_for_claim"`
	// Taken is true iff this issue currently has at least one open
	// (unreleased) agent claim held by an alive session — the derived
	// "an agent is actively holding this" signal also surfaced on the
	// show/brief views. Computed at read time via the same join the
	// desktop's ListOpenClaims runs, so list responses don't need a
	// second round trip. No omitempty: the field must be visible
	// (including when false) in JSON output.
	Taken bool     `json:"taken"`
	Tags  []string `json:"tags"`
	// ArchivedAt (BACI-68) is non-nil iff the issue is archived —
	// hidden from default lists / boards, but the row and its audit
	// history are retained. The auto-sweep stamps it for issues whose
	// terminal_at is older than the configured retention window
	// (BACI-162; default 7 days, editable via `bacio settings archive`
	// or the desktop Settings panel); manual `bacio issue archive` /
	// `unarchive` writes or clears it on demand. Reopening an archived
	// issue (state -> todo/...) does NOT auto-unarchive — the user must
	// unarchive explicitly.
	ArchivedAt *time.Time `json:"archived_at,omitempty"`
	// TerminalAt (BACI-138) is non-nil iff the issue is currently in
	// a terminal state (done / cancelled), and carries the timestamp
	// of the most recent transition INTO that state. Cleared whenever
	// state moves out of a terminal value. Drives the kanban Done /
	// Cancelled column ordering (newest-first) without joining the
	// audit log. Omitempty so non-terminal rows don't carry a JSON
	// `terminal_at: null` field. Replaces the pre-BACI-138 sort that
	// proxied via UpdatedAt — that proxy was wrong because tag /
	// title / description edits bump UpdatedAt without changing state.
	TerminalAt *time.Time `json:"terminal_at,omitempty"`
	// UserActionReasonType (BACI-220) carries the typed reason an issue
	// is parked in `needs_action`. The bucket today screams "agent is
	// waiting on you" uniformly; this column lets the UI tell the two
	// cases apart — `user_question` (the agent asked a clarifying
	// question via `mcp__bacio__ask_user_question`) vs.
	// `user_manual_review` (the agent finished and wants a sanity
	// check, wired in a follow-up). Empty / NULL whenever the issue is
	// not in `needs_action`. Omitempty so non-parked rows stay compact
	// on the wire — readers that care must check `state == needs_action`
	// first.
	UserActionReasonType UserActionReasonType `json:"user_action_reason_type,omitempty"`
	CreatedAt            time.Time            `json:"created_at"`
	UpdatedAt            time.Time            `json:"updated_at"`
}

type Comment struct {
	ID      int64  `json:"id"`
	UUID    string `json:"uuid"`
	IssueID int64  `json:"issue_id"`
	Author  string `json:"author"`
	Body    string `json:"body"`
	// Eval (BACI-131) is true when the row was posted from the kanban
	// card's quick-eval composer; the (AgentSessionID, DispatchID,
	// Mode) triple is captured server-side at write time. All four
	// fields are zero values on normal (non-eval) comments. Eval has
	// no `omitempty` so a `bacio comment list -o json` consumer never
	// has to guess whether the row is an eval note.
	Eval           bool   `json:"eval"`
	AgentSessionID string `json:"agent_session_id,omitempty"`
	DispatchID     *int64 `json:"dispatch_id,omitempty"`
	Mode           string `json:"mode,omitempty"`
	// TranscriptEventRef (BACI-141) pins an eval comment to a specific
	// event inside a `.jsonl` transcript: `tool_use_id:<id>` (durable
	// across re-renders, matches both the assistant tool_use event and
	// the matching user-tool-result event) or `line_index:<n>` (fallback
	// for events without a tool_use_id; `.jsonl` transcripts are
	// append-only so line indices are durable). Empty = unanchored, the
	// dispatch-card-level note the transcript viewer renders pinned to
	// the prompt card. Omitempty so non-eval / unanchored rows stay
	// compact on the wire.
	TranscriptEventRef string `json:"transcript_event_ref,omitempty"`
	// AgentName (BACI-131) is the persistent agent identity slug
	// resolved from AgentSessionID at read time — not persisted on the
	// comment row. Populated by the brief / issue-detail JOIN so the
	// timeline footer can render "during: planning · vivid-finch"
	// without a second client-side fetch. Empty when the session id
	// is empty or the session has no agent identity attached.
	AgentName string    `json:"agent_name,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// FeatureComment is the feature-scoped chronological-handoff scratchpad
// row (BACI-124). Same shape as Comment but parented to a feature
// instead of an issue, used by dispatched implement-mode workers to
// post a close-out handoff note (files of context, deviations from
// plan, work deferred) so the next worker on a sibling issue in the
// same feature inherits the context.
type FeatureComment struct {
	ID        int64     `json:"id"`
	UUID      string    `json:"uuid"`
	FeatureID int64     `json:"feature_id"`
	Author    string    `json:"author"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

type RelationType string

const (
	RelBlocks      RelationType = "blocks"
	RelRelatesTo   RelationType = "relates_to"
	RelDuplicateOf RelationType = "duplicate_of"
)

type Relation struct {
	ID        int64        `json:"id"`
	FromIssue string       `json:"from_issue"` // key form
	ToIssue   string       `json:"to_issue"`
	Type      RelationType `json:"type"`
	CreatedAt time.Time    `json:"created_at"`
}

type PullRequest struct {
	ID        int64     `json:"id"`
	IssueID   int64     `json:"issue_id"`
	URL       string    `json:"url"`
	CreatedAt time.Time `json:"created_at"`
}

// LatestPR is the per-issue PR denorm consumed by the kanban board
// (BACI-239) — the most-recently-attached PR for a given issue, plus
// the total PR count so the card chip can surface "N attached" in its
// tooltip without a second round-trip. Sibling of LatestPlan: same
// shape, same purpose (one optional field on BoardCard).
//
// "Most recently attached" follows `ORDER BY created_at DESC, id DESC`
// — the freshly-attached PR after a review iteration is the link the
// user clicks. If a future ticket disagrees, the rule lives in one
// place (Store.LatestPRByIssue) and is cheap to flip.
type LatestPR struct {
	URL       string    `json:"url"`
	Count     int       `json:"count"`
	CreatedAt time.Time `json:"created_at"`
}

// SyncState records that a record (issue/feature/document/comment/repo)
// has participated in a git-backed sync pass. Presence-of-row is the
// "previously synced" flag — absence means "local-only, never
// exported". The hash is the canonical sync-side content hash at the
// time of the last sync, used to detect on-disk edits between runs.
type SyncState struct {
	UUID           string    `json:"uuid"`
	Kind           string    `json:"kind"`
	LastSyncedAt   time.Time `json:"last_synced_at"`
	LastSyncedHash string    `json:"last_synced_hash"`
}

// SyncRemote records, for one canonical remote URL, where this user
// has the matching sync repo cloned locally. The remote URL is the
// shared truth (it also appears in every project's .bacio/config.yaml);
// the local path is per-machine and lives only in this DB. LastSyncAt
// is bumped at the end of every successful `bacio sync`.
type SyncRemote struct {
	RemoteURL string    `json:"remote_url"`
	LocalPath string    `json:"local_path"`
	ClonedAt  time.Time `json:"cloned_at"`
	// LastSyncAt is the time of the last successful sync, or nil if
	// none has succeeded yet.
	LastSyncAt *time.Time `json:"last_sync_at,omitempty"`
	// LastSyncError carries the failure message from the last sync
	// run, or nil when the last run succeeded (BACI-89). Written by the
	// background sync ticker; cleared on the next success.
	LastSyncError *string `json:"last_sync_error,omitempty"`
}
