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
