package inputs

// WorktreeInitInput is the payload for `bacio worktree init --json`.
// All fields are optional — Slug defaults to the worktree basename,
// Port is auto-allocated from the registry, and DBPath defaults to
// `.bacio/db.sqlite` (relative to the worktree root). Force=true
// allows overwriting an existing manifest in place.
type WorktreeInitInput struct {
	Slug   string `json:"slug,omitempty"`
	Port   int    `json:"port,omitempty"`
	DBPath string `json:"db_path,omitempty"`
	Force  bool   `json:"force,omitempty"`
}

// WorktreeRmInput is the payload for `bacio worktree rm --json`. Path
// defaults to the current working tree when empty. Confirm must equal
// the manifest's slug — same pattern as `bacio repo rm`. PurgeDB drops
// the worktree's SQLite DB alongside the manifest.
type WorktreeRmInput struct {
	Path     string `json:"path,omitempty"`
	Confirm  string `json:"confirm"`
	PurgeDB  bool   `json:"purge_db,omitempty"`
}
