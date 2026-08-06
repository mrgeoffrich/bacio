package inputs

// RepoCreateInput is the payload for `POST /repos`. There is no CLI
// equivalent — `bacio init` infers path from CWD, which has no analogue on
// an HTTP server.
type RepoCreateInput struct {
	Prefix string `json:"prefix,omitempty"`
	Name   string `json:"name"`
	// Kind is "git" (the default when absent) or "workspace". A
	// workspace has no checkout on disk, so `path` and `remote_url` must
	// be empty for it and are required for a git repo — the two kinds
	// are genuinely different payloads sharing one route. Callers that
	// only ever make workspaces should prefer POST /workspaces, which
	// takes just {name, prefix?}.
	Kind      string `json:"kind,omitempty"`
	Path      string `json:"path"`
	RemoteURL string `json:"remote_url,omitempty"`
}

// RepoRmInput is the payload for `bacio repo rm --json`. The `confirm`
// field must equal `prefix` (case-insensitive) before the backend will
// actually delete the repo — without it the call returns an impact
// preview as an error so an LLM agent stops and asks the user before
// re-running.
type RepoRmInput struct {
	Prefix  string `json:"prefix"`
	Confirm string `json:"confirm,omitempty"`
}

// RepoLinkInput is the payload for `bacio repo link --json` / `POST
// /repos/{prefix}/link` (BACI-112). Binds a phantom repo (a sync_clone-
// imported row with `repos.path == ''`) to a local working tree at
// `path`. The backend resolves the owning sync repo by walking the
// sync_remotes registry, runs `UpgradePhantomRepo`, then writes the
// project's `.bacio/config.yaml` pointing at the sync remote.
type RepoLinkInput struct {
	Prefix string `json:"prefix"`
	Path   string `json:"path"`
}
