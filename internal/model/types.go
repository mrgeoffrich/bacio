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
	ID          int64     `json:"id"`
	UUID        string    `json:"uuid"`
	RepoID      int64     `json:"repo_id"`
	Slug        string    `json:"slug"`
	Title       string    `json:"title"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
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
	WaitingForClaim bool      `json:"waiting_for_claim"`
	Tags            []string  `json:"tags"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type Comment struct {
	ID        int64     `json:"id"`
	UUID      string    `json:"uuid"`
	IssueID   int64     `json:"issue_id"`
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
	RemoteURL  string     `json:"remote_url"`
	LocalPath  string     `json:"local_path"`
	ClonedAt   time.Time  `json:"cloned_at"`
	LastSyncAt *time.Time `json:"last_sync_at,omitempty"`
}
