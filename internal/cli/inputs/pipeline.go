package inputs

// IssueReorderInput is the payload for `bacio issue reorder --json` and
// the PUT …/issues/{key}/reorder endpoint. Position is 1-based within the
// issue's (repo, state) ordering band — position 1 is the top of the
// column (next to go).
type IssueReorderInput struct {
	Key      string `json:"key"`
	Position int    `json:"position"`
}

// IssueProcessInput is the payload for `bacio issue process set --json`
// and POST …/issues/{key}/process. Exactly one of Process or Stages is
// required (they are mutually exclusive). Process is a preset slug
// (model.PipelineProcesses) — e.g. "plan-implement-ship". Stages is an
// explicit ordered list of job modes (e.g. ["design","plan_large",
// "implement","ship"]) for an arbitrary chain the named presets don't
// enumerate — validated via model.ProcessFromStages.
type IssueProcessInput struct {
	Key     string   `json:"key"`
	Process string   `json:"process,omitempty"`
	Stages  []string `json:"stages,omitempty"`
}

// IssueEngineModeInput is the payload for the engine drive-mode toggle
// (PUT …/issues/{key}/engine-mode). Mode is "off" or "auto".
type IssueEngineModeInput struct {
	Key  string `json:"key"`
	Mode string `json:"mode"`
}

// IssueShipInput is the payload for `bacio issue ship --json` — the
// in_pipeline → to_be_shipped hand-off. Key-only.
type IssueShipInput struct {
	Key string `json:"key"`
}

// RepoAutoShipInput is the payload for `bacio issue auto-ship --json` and
// PUT …/auto-ship — the per-repo Shipping-column auto-ship toggle.
type RepoAutoShipInput struct {
	Enabled bool `json:"enabled"`
}
