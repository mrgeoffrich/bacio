package model

import "time"

// HistoryEntry records a single mutation made via bacio. Reads are not logged.
// Fields are snapshots of state at the time of the op — no foreign keys —
// so entries survive deletion of the entity they describe.
type HistoryEntry struct {
	ID          int64     `json:"id"`
	RepoID      *int64    `json:"repo_id,omitempty"`
	RepoPrefix  string    `json:"repo_prefix,omitempty"`
	Actor       string    `json:"actor"`
	Op          string    `json:"op"`
	Kind        string    `json:"kind,omitempty"`
	TargetID    *int64    `json:"target_id,omitempty"`
	TargetLabel string    `json:"target_label,omitempty"`
	Details     string    `json:"details,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}
