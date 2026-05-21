package inputs

// WorktreeInitInput is the payload for `bacio worktree init --json`.
// All fields are optional — Slug defaults to the worktree basename and
// Port is auto-allocated from the registry. By default the manifest is
// written with an empty db_path, so DB resolution stays on the shared
// ~/.bacio/db.sqlite (port isolation only). IsolateDB=true opts into a
// per-worktree DB at `.bacio/db.sqlite`; DBPath pins an explicit path
// and overrides IsolateDB. Force=true allows overwriting an existing
// manifest in place.
type WorktreeInitInput struct {
	Slug      string `json:"slug,omitempty"`
	Port      int    `json:"port,omitempty"`
	DBPath    string `json:"db_path,omitempty"`
	IsolateDB bool   `json:"isolate_db,omitempty"`
	Force     bool   `json:"force,omitempty"`
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
