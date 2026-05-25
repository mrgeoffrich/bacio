package inputs

// RepoCreateInput is the payload for `POST /repos`. There is no CLI
// equivalent — `bacio init` infers path from CWD, which has no analogue on
// an HTTP server.
type RepoCreateInput struct {
	Prefix    string `json:"prefix,omitempty"`
	Name      string `json:"name"`
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

// RepoLinkInput is the payload for `bacio repo link --json` (BACI-112):
// bind a synced-but-pathless phantom repo to a local git working tree.
// Prefix identifies the phantom row; Path is the absolute path to the
// existing working tree on this machine. The backend resolves the
// owning sync repo by walking the sync_remotes registry, runs
// UpgradePhantomRepo, then writes .bacio/config.yaml with the sync
// remote URL so subsequent bacio runs from inside the working tree
// pick up the registered sync remote automatically.
//
// Idempotent: re-linking to the same Path is a no-op (the result
// carries AlreadyLinked=true and no audit row is written).
type RepoLinkInput struct {
	Prefix string `json:"prefix"`
	Path   string `json:"path"`
}
