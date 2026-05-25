package inputs

// SyncSetupInput is the payload for `POST /repos/{prefix}/sync/setup`
// (BACI-110). It is HTTP-only — there is no `bacio sync setup` CLI verb
// because the CLI already covers the same ground with `bacio sync init`
// and `bacio sync clone`. The schema is published anyway so an LLM
// driving the API has the same discover-via-`bacio schema` ergonomics
// as every other mutating endpoint.
//
// Mode picks the bootstrap path:
//
//   - "init"   — InitSyncRepo: write the project's .bacio/config.yaml,
//     create/attach a sync repo at LocalPath, export, commit, push.
//     LocalPath is required; Remote is optional when LocalPath already
//     has an `origin` configured (matches `bacio sync init`).
//   - "clone"  — CloneSyncRepo: git-clone the sync repo at Remote into
//     LocalPath (default ~/.bacio/sync/<derived-label>), import its data,
//     write the project's .bacio/config.yaml. Honours AllowRenumber for
//     the BACI-4 renumber-collision preview gate.
//   - "attach" — Join an existing registry entry (a sync_remotes row
//     already on this machine). Looks the row up by Remote, then runs the
//     CloneSyncRepo path against the registry's local_path — which
//     short-circuits via `openOrCloneSyncRepo` (no git clone) and just
//     re-runs an additive import + writes the project's config. LocalPath
//     must NOT be set on this mode; passing it is rejected so the caller
//     can't accidentally point at a directory that doesn't match the
//     registry row's local_path.
//
// AllowRenumber gates the renumber-collision preview produced by
// CloneSyncRepo when the local DB has rows that would be renumbered
// or renamed by the imported data. Without it set, the handler returns
// 409 + the preview in the body; the caller re-POSTs with
// AllowRenumber=true to confirm. Ignored by mode="init" (which is
// additive and never renumbers in attach-via-init mode).
type SyncSetupInput struct {
	Mode          string `json:"mode"`
	Remote        string `json:"remote,omitempty"`
	LocalPath     string `json:"local_path,omitempty"`
	AllowRenumber bool   `json:"allow_renumber,omitempty"`
}
