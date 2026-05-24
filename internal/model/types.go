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
	// ArchivedAt (BACI-68) is non-nil iff the feature is archived —
	// hidden from default lists, but the row and its audit history
	// remain. The auto-sweep stamps it when every child issue is
	// archived; manual `bacio feature archive` / `unarchive` writes or
	// clears it on demand.
	ArchivedAt *time.Time `json:"archived_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

type Issue struct {
	ID          int64     `json:"id"`
	UUID        string    `json:"uuid"`
	RepoID      int64     `json:"repo_id"`
	Number      int64     `json:"number"`
	Key         string    `json:"key"` // e.g. "MINI-42"
	FeatureID   *int64    `json:"feature_id,omitempty"`
	FeatureSlug string    `json:"feature_slug,omitempty"`
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
	// history are retained. The auto-sweep stamps it for issues older
	// than 4 days in a terminal state; manual `bacio issue archive` /
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
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
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
