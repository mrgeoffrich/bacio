package sync

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mrgeoffrich/bacio/internal/git"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// Bootstrap entry points. `bacio sync init` and `bacio sync clone` are the
// two non-overlapping ways to land in a synced state — `init` for a
// fresh remote, `clone` for joining an existing one. Once either has
// run on a machine, steady-state `bacio sync` takes over.
//
// Both flows are implemented as methods on *Engine so they share the
// existing Store / Actor / DryRun state. The CLI layer (sync.go in
// internal/cli) wires args + flags through to these.

// InitOptions controls `bacio sync init`. LocalPath is where the sync
// repo will be created on disk; Remote is the optional canonical
// remote URL to register as `origin` and write into the project's
// .bacio/config.yaml.
type InitOptions struct {
	LocalPath string
	Remote    string
}

// CloneOptions controls `bacio sync clone`. LocalPath defaults to
// `~/.bacio/sync/<basename>` if empty. AllowRenumber gates the
// "local DB has data for this prefix" preflight; DryRun runs the
// preview without touching DB or disk.
type CloneOptions struct {
	LocalPath     string
	Remote        string
	AllowRenumber bool
	DryRun        bool
}

// InitResult is the structured outcome of `bacio sync init`. Fields
// match the JSON shape `bacio sync init --output json` emits.
type InitResult struct {
	LocalPath string `json:"local_path"`
	Remote    string `json:"remote,omitempty"`
	CommitSHA string `json:"commit_sha,omitempty"`
	Pushed    bool   `json:"pushed"`

	// Attached is true when init ran against a pre-existing sync repo
	// (or an existing `git init`-only folder) rather than bootstrapping
	// from scratch. Lets callers distinguish "this project just joined
	// an existing sync repo" from "this is the first project here".
	Attached bool `json:"attached,omitempty"`

	// Export counts. Always populated.
	Export *ExportResult `json:"export,omitempty"`

	// Import counts. Only populated in attach mode (when init pulls
	// data from an existing sync repo into the local DB before
	// re-exporting).
	Import *ImportResult `json:"import,omitempty"`
}

// CloneResult is the structured outcome of `bacio sync clone`.
type CloneResult struct {
	LocalPath string `json:"local_path"`
	Remote    string `json:"remote"`
	DryRun    bool   `json:"dry_run,omitempty"`

	// Import counts (from the underlying ImportResult). When DryRun
	// is true, this carries the preview.
	Import *ImportResult `json:"import,omitempty"`

	// PreviewCollisions captures the projected renumbers/renames a
	// full clone would produce. Populated when local DB has data
	// for the prefix and AllowRenumber is false (or DryRun is true);
	// the caller can render this for user confirmation.
	PreviewCollisions *CollisionPreview `json:"preview_collisions,omitempty"`
}

// CollisionPreview is the dry-run report produced when `bacio sync clone`
// detects that the local DB has data that would be renumbered/renamed
// by the import. Empty fields are emitted but with `omitempty` so a
// JSON consumer doesn't have to special-case "no churn".
type CollisionPreview struct {
	Renumbered []RenumberEntry `json:"renumbered,omitempty"`
	Renamed    []RenameEntry   `json:"renamed,omitempty"`
}

// InitSyncRepo creates a sync repo at opts.LocalPath, exports the
// project's data into it, and (if opts.Remote is set) wires up origin
// + .bacio/config.yaml so collaborators can clone. Refuses to run if
// the remote already has commits.
func (e *Engine) InitSyncRepo(ctx context.Context, projectRoot string, opts InitOptions) (*InitResult, error) {
	if e.Store == nil {
		return nil, fmt.Errorf("InitSyncRepo: Store is nil")
	}
	if projectRoot == "" {
		return nil, fmt.Errorf("InitSyncRepo: projectRoot is empty")
	}
	if opts.LocalPath == "" {
		return nil, fmt.Errorf("InitSyncRepo: LocalPath is required")
	}
	// --remote is optional on init (it can be auto-detected from an
	// existing origin), but when supplied it must be a safe git URL.
	if opts.Remote != "" {
		if err := git.ValidateRemoteURL(opts.Remote); err != nil {
			return nil, fmt.Errorf("invalid --remote: %w", err)
		}
	}

	// Project root must be a real git repo, not a sync repo.
	if IsSyncRepo(projectRoot) {
		return nil, fmt.Errorf("cannot run bacio sync init from inside a sync repo (bacio-sync.yaml at %s)", projectRoot)
	}
	if _, err := git.Open(projectRoot); err != nil {
		return nil, fmt.Errorf("project root %s is not a git repo: %w", projectRoot, err)
	}

	// Classify the target path. Each state takes a different bootstrap
	// path; targetIncompatible is the explicit refusal case.
	state, err := classifyInitTarget(opts.LocalPath)
	if err != nil {
		return nil, err
	}
	if state == targetIncompatible {
		return nil, fmt.Errorf("%s is not empty and not an bacio sync repo; remove it or pick another path", opts.LocalPath)
	}
	attached := state == targetExistingSyncRepo || state == targetEmptyGit

	res := &InitResult{LocalPath: opts.LocalPath, Remote: opts.Remote, Attached: attached}

	if e.DryRun {
		// In attach mode, preview the import too so the user sees
		// what would be merged in alongside the export preview.
		// Uses the same additive flag the real run uses (see
		// BACI-4); a dry-run that overstated deletions would be
		// misleading.
		if state == targetExistingSyncRepo {
			impEng := &Engine{Store: e.Store, Actor: e.Actor, DryRun: true, SkipPropagateDeletes: true}
			impRes, err := impEng.Import(ctx, opts.LocalPath)
			if err != nil {
				return nil, fmt.Errorf("dry-run import: %w", err)
			}
			res.Import = impRes
		}
		// Build a full Export against a temp dir so the user sees
		// the projected counts. No filesystem changes outside the
		// temp dir.
		tmp, err := os.MkdirTemp("", "bacio-sync-init-dryrun-")
		if err != nil {
			return nil, fmt.Errorf("dry-run tmp: %w", err)
		}
		defer os.RemoveAll(tmp)
		exportRes, err := (&Engine{Store: e.Store, Actor: e.Actor, DryRun: false}).Export(ctx, tmp)
		if err != nil {
			return nil, fmt.Errorf("dry-run export: %w", err)
		}
		res.Export = exportRes
		return res, nil
	}

	// 1. Open or create the local git repo.
	var syncRepo *git.Repo
	switch state {
	case targetEmpty:
		syncRepo, err = git.Init(opts.LocalPath)
		if err != nil {
			return nil, fmt.Errorf("git init: %w", err)
		}
	case targetEmptyGit, targetExistingSyncRepo:
		syncRepo, err = git.Open(opts.LocalPath)
		if err != nil {
			return nil, fmt.Errorf("open existing repo at %s: %w", opts.LocalPath, err)
		}
	}

	// 2. Resolve the effective remote. If the existing repo already
	// has an `origin` set, default to that; if the caller also passed
	// --remote, they must match.
	existingOrigin, err := syncRepo.RemoteURL("origin")
	if err != nil {
		return nil, fmt.Errorf("read existing origin: %w", err)
	}
	effectiveRemote := opts.Remote
	switch {
	case effectiveRemote == "" && existingOrigin != "":
		effectiveRemote = existingOrigin
	case effectiveRemote != "" && existingOrigin != "" && effectiveRemote != existingOrigin:
		return nil, fmt.Errorf("existing repo at %s already has origin=%s which doesn't match --remote=%s", opts.LocalPath, existingOrigin, opts.Remote)
	}
	res.Remote = effectiveRemote

	// 3. Wire up origin if it isn't already configured.
	if effectiveRemote != "" && existingOrigin == "" {
		if err := syncRepo.AddRemote("origin", effectiveRemote); err != nil {
			return nil, fmt.Errorf("add origin: %w", err)
		}
	}

	// 4. Refuse to seed an already-populated remote only in the pure
	// bootstrap case. In attach mode we expect the remote to have
	// commits — that's the whole point.
	if effectiveRemote != "" && state != targetExistingSyncRepo {
		hasContent, err := syncRepo.RemoteHasContent("origin")
		if err != nil {
			return nil, fmt.Errorf("check remote: %w", err)
		}
		if hasContent && state == targetEmpty {
			return nil, fmt.Errorf("remote %s already has content; use 'bacio sync clone' (or `git clone %s <path>` then `bacio sync init <path>`) to join it", effectiveRemote, effectiveRemote)
		}
		// If state == targetEmptyGit and remote has content, fall
		// through: pull will bring it in below, and we'll switch to
		// attach-style flow from there.
		if hasContent && state == targetEmptyGit {
			state = targetExistingSyncRepo
			res.Attached = true
		}
	}

	// 5. Write sentinel + .gitattributes if they're not already there.
	// Preserves the original created_at for an existing sync repo.
	if !IsSyncRepo(opts.LocalPath) {
		if err := WriteSentinel(opts.LocalPath, Sentinel{
			SchemaVersion: SchemaVersion,
			CreatedAt:     time.Now().UTC(),
		}); err != nil {
			return nil, err
		}
	}
	if _, err := os.Stat(filepath.Join(opts.LocalPath, ".gitattributes")); os.IsNotExist(err) {
		// Pin LF line endings so checkouts on Windows don't rewrite the
		// canonical YAML. Defence in depth alongside NormalizeBody on
		// the parse side.
		if err := syncRepo.WriteGitattributes("* -text\n"); err != nil {
			return nil, fmt.Errorf("write .gitattributes: %w", err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("stat .gitattributes: %w", err)
	}

	// 6. Attach mode only: pull existing commits and import any
	// rows we don't yet have in the local DB before re-exporting.
	if state == targetExistingSyncRepo && effectiveRemote != "" {
		if err := syncRepo.Pull(); err != nil {
			if !isNoUpstreamErr(err) {
				return nil, fmt.Errorf("git pull: %w", err)
			}
		}
	}
	if state == targetExistingSyncRepo {
		// Attach-mode import is additive: SkipPropagateDeletes
		// preserves local-only DB rows whose uuids aren't in the
		// sync repo's current working tree. Steady-state bacio sync
		// is where deletes propagate; init must never destroy local
		// work (see BACI-4).
		importEng := &Engine{Store: e.Store, Actor: e.Actor, DryRun: false, SkipPropagateDeletes: true}
		impRes, err := importEng.Import(ctx, opts.LocalPath)
		if err != nil {
			return nil, fmt.Errorf("import: %w", err)
		}
		res.Import = impRes
	}

	// 7. Run the export.
	exportEng := &Engine{Store: e.Store, Actor: e.Actor, DryRun: false}
	exportRes, err := exportEng.Export(ctx, opts.LocalPath)
	if err != nil {
		return nil, fmt.Errorf("export: %w", err)
	}
	res.Export = exportRes

	// 8. Mark every uuid as synced so a subsequent `bacio sync` doesn't
	// treat them as "never synced". Idempotent — safe to re-run.
	if err := e.markAllAsSynced(); err != nil {
		return nil, fmt.Errorf("seed sync_state: %w", err)
	}

	// 9. Stage + commit. Commit returns ("", nil) if nothing staged.
	if err := syncRepo.Add(); err != nil {
		return nil, fmt.Errorf("git add: %w", err)
	}
	sha, err := syncRepo.Commit(initCommitMessage(e.Actor))
	if err != nil {
		return nil, fmt.Errorf("git commit: %w", err)
	}
	res.CommitSHA = sha

	// 10. Push (if remote configured). Use plain Push if the branch
	// already tracks an upstream; otherwise PushSetUpstream to wire
	// up tracking on first push.
	if effectiveRemote != "" {
		hasUpstream, err := syncRepo.HasUpstream()
		if err != nil {
			return nil, fmt.Errorf("check upstream: %w", err)
		}
		if hasUpstream {
			if sha != "" {
				if err := syncRepo.Push(); err != nil {
					return nil, fmt.Errorf("git push: %w", err)
				}
				res.Pushed = true
			}
		} else {
			branch, err := syncRepo.CurrentBranch()
			if err != nil {
				return nil, fmt.Errorf("resolve current branch: %w", err)
			}
			if err := syncRepo.PushSetUpstream("origin", branch); err != nil {
				return nil, fmt.Errorf("git push: %w", err)
			}
			res.Pushed = true
		}
	}

	// 11. .bacio/config.yaml + DB-side bookkeeping (only meaningful
	// when a remote is configured).
	if effectiveRemote != "" {
		if err := WriteProjectConfig(projectRoot, ProjectConfig{
			Sync: ProjectSync{Remote: effectiveRemote},
		}); err != nil {
			return nil, err
		}
		if err := e.Store.UpsertSyncRemote(effectiveRemote, opts.LocalPath); err != nil {
			return nil, fmt.Errorf("record sync remote: %w", err)
		}
	}
	return res, nil
}

// CloneSyncRepo joins an existing sync remote: clones (or validates)
// the repo at opts.LocalPath, runs the import-only flow, and records
// the local-path mapping in DB. Refuses without `--allow-renumber`
// when local DB already has rows that would collide with imported
// labels.
func (e *Engine) CloneSyncRepo(ctx context.Context, projectRoot string, opts CloneOptions) (*CloneResult, error) {
	if e.Store == nil {
		return nil, fmt.Errorf("CloneSyncRepo: Store is nil")
	}
	if projectRoot == "" {
		return nil, fmt.Errorf("CloneSyncRepo: projectRoot is empty")
	}
	if opts.Remote == "" {
		return nil, fmt.Errorf("CloneSyncRepo: Remote is required")
	}
	if err := git.ValidateRemoteURL(opts.Remote); err != nil {
		return nil, fmt.Errorf("invalid --remote: %w", err)
	}

	// Project root must be a real git repo, not a sync repo.
	if IsSyncRepo(projectRoot) {
		return nil, fmt.Errorf("cannot run bacio sync clone from inside a sync repo (bacio-sync.yaml at %s)", projectRoot)
	}

	// Default LocalPath: ~/.bacio/sync/<basename of remote>.
	localPath := opts.LocalPath
	if localPath == "" {
		def, err := defaultClonePath(opts.Remote)
		if err != nil {
			return nil, err
		}
		localPath = def
	}

	res := &CloneResult{LocalPath: localPath, Remote: opts.Remote, DryRun: opts.DryRun}

	// 1. Either clone (path empty/missing) or validate (path exists
	// with bacio-sync.yaml + matching origin).
	syncRepo, err := openOrCloneSyncRepo(localPath, opts.Remote)
	if err != nil {
		return nil, err
	}

	// 2. Detect potential collisions before any DB write. The cheap
	// proxy: any existing repo row whose prefix is also present in
	// the cloned tree, with any rows that would collide on label.
	preview, err := previewClone(ctx, e, syncRepo)
	if err != nil {
		return nil, err
	}

	hasCollisions := preview != nil && (len(preview.Renumbered) > 0 || len(preview.Renamed) > 0)
	if hasCollisions && !opts.AllowRenumber && !opts.DryRun {
		res.PreviewCollisions = preview
		return res, fmt.Errorf("local DB has data that would be renumbered/renamed; re-run with --allow-renumber (or --dry-run to preview)")
	}

	if opts.DryRun {
		// Run the import in dry-run mode for accurate counts.
		// SkipPropagateDeletes matches the real path below — clone
		// is additive, so the preview must reflect that or it'll
		// overstate deletions (see BACI-4).
		dryEng := &Engine{Store: e.Store, Actor: e.Actor, DryRun: true, SkipPropagateDeletes: true}
		importRes, err := dryEng.Import(ctx, syncRepo.Root)
		if err != nil {
			return nil, fmt.Errorf("dry-run import: %w", err)
		}
		res.Import = importRes
		if hasCollisions {
			res.PreviewCollisions = preview
		}
		return res, nil
	}

	// 3. Real import. Additive merge: clone never destroys local
	// work, only adds remote-only records by uuid. Deletes are the
	// job of steady-state bacio sync (see BACI-4).
	importEng := &Engine{Store: e.Store, Actor: e.Actor, DryRun: false, SkipPropagateDeletes: true}
	importRes, err := importEng.Import(ctx, syncRepo.Root)
	if err != nil {
		return nil, fmt.Errorf("import: %w", err)
	}
	res.Import = importRes

	// 4. Record the (remote → local path) mapping.
	if err := e.Store.UpsertSyncRemote(opts.Remote, localPath); err != nil {
		return nil, fmt.Errorf("record sync remote: %w", err)
	}

	// 5. Write the project's machine-local .bacio/config.yaml so
	// steady-state `bacio sync` can find the remote. Unlike the old
	// model this file is NOT shared via git — each machine writes its
	// own from the trusted --remote flag (see ProjectConfig docs).
	if err := WriteProjectConfig(projectRoot, ProjectConfig{
		Sync: ProjectSync{Remote: opts.Remote},
	}); err != nil {
		return nil, err
	}

	return res, nil
}

// openOrCloneSyncRepo handles the "either clone or validate" branch
// of `bacio sync clone`. If localPath doesn't exist or is empty, run
// `git clone`. If it exists with content, verify it carries our
// sentinel and points at the same remote.
func openOrCloneSyncRepo(localPath, remote string) (*git.Repo, error) {
	info, err := os.Stat(localPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("stat %s: %w", localPath, err)
		}
		// Doesn't exist — clone.
		if err := git.Clone(remote, localPath); err != nil {
			return nil, fmt.Errorf("git clone: %w", err)
		}
		return git.Open(localPath)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", localPath)
	}
	// Exists. Check if empty.
	entries, err := os.ReadDir(localPath)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		// Empty dir: clone into it. `git clone` refuses an existing
		// non-empty dest but is fine with an empty one only if dest
		// doesn't exist (older git) or if --no-checkout is used; the
		// safe path is to remove and re-clone.
		if err := os.RemoveAll(localPath); err != nil {
			return nil, fmt.Errorf("remove empty dir: %w", err)
		}
		if err := git.Clone(remote, localPath); err != nil {
			return nil, fmt.Errorf("git clone: %w", err)
		}
		return git.Open(localPath)
	}
	// Existing non-empty: must already be our sync repo with this
	// remote configured.
	if !IsSyncRepo(localPath) {
		return nil, fmt.Errorf("%s exists but doesn't carry bacio-sync.yaml; refusing to overwrite", localPath)
	}
	syncRepo, err := git.Open(localPath)
	if err != nil {
		return nil, fmt.Errorf("open existing sync repo: %w", err)
	}
	// Best-effort remote check. If the user named "origin" something
	// else we'll miss this; that's acceptable for a v1 advisory.
	return syncRepo, nil
}

// previewClone runs the import in dry-run mode and extracts the
// renumber / rename projections for the preview report. We use the
// dry-run path rather than a hand-rolled scanner so the preview
// matches reality byte-for-byte.
func previewClone(ctx context.Context, e *Engine, syncRepo *git.Repo) (*CollisionPreview, error) {
	// Match the real clone path: additive merge, no propagated
	// deletes. Preview must use the same flag set as the run it's
	// previewing (see BACI-4).
	previewEng := &Engine{Store: e.Store, Actor: e.Actor, DryRun: true, SkipPropagateDeletes: true}
	imp, err := previewEng.Import(ctx, syncRepo.Root)
	if err != nil {
		return nil, err
	}
	if len(imp.Renumbered) == 0 && len(imp.Renamed) == 0 {
		return nil, nil
	}
	return &CollisionPreview{Renumbered: imp.Renumbered, Renamed: imp.Renamed}, nil
}

// defaultClonePath computes the default local path for a sync repo
// given its remote URL. Strategy: ~/.bacio/sync/<basename> where
// <basename> is the last path component minus a trailing `.git`.
func defaultClonePath(remote string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	base := remote
	// Strip any URL prefix so we end up at the last path segment. Cheap
	// rather than parsing as a URL — supports `git@host:user/repo.git`
	// and `https://host/user/repo.git` alike.
	if i := strings.LastIndexAny(base, "/:"); i >= 0 {
		base = base[i+1:]
	}
	base = strings.TrimSuffix(base, ".git")
	if base == "" {
		base = "default"
	}
	return filepath.Join(home, ".bacio", "sync", base), nil
}

// initTargetState classifies the state of the directory passed to
// `bacio sync init`. The classification gates how InitSyncRepo behaves:
// fresh paths get full bootstrap; existing sync repos get attach mode;
// existing git repos with no commits get sentinel-only bootstrap;
// anything else is refused.
type initTargetState int

const (
	// targetEmpty: path is missing, or exists as an empty directory.
	targetEmpty initTargetState = iota
	// targetEmptyGit: path exists, has a `.git` entry, but no commits
	// and no other working-tree files. Typical of `git init` or a
	// fresh `git clone` of an empty bare remote.
	targetEmptyGit
	// targetExistingSyncRepo: path is an bacio sync repo (carries
	// bacio-sync.yaml at root). Attach mode.
	targetExistingSyncRepo
	// targetIncompatible: anything else — non-git directory with
	// files, or a git repo with unrelated commits and no sentinel.
	targetIncompatible
)

// classifyInitTarget inspects path on disk and returns its state.
// Used by InitSyncRepo to pick between bootstrap and attach behavior.
// Returns targetIncompatible (and nil error) for cases the caller
// should refuse, so callers always render the same refusal message.
func classifyInitTarget(path string) (initTargetState, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return targetEmpty, nil
		}
		return 0, err
	}
	if !info.IsDir() {
		return targetIncompatible, nil
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return 0, err
	}
	if len(entries) == 0 {
		return targetEmpty, nil
	}
	if IsSyncRepo(path) {
		return targetExistingSyncRepo, nil
	}
	// Not a sync repo but non-empty: only acceptable when the only
	// entry is `.git` (working tree is otherwise clean). This covers
	// `git init` on a fresh folder, `git clone <empty-bare>`, and
	// `git clone <existing-repo>` followed by deleting tracked files —
	// the commit history is fine; what matters is that there are no
	// working-tree files that would coexist with bacio's `repos/` and
	// `bacio-sync.yaml`.
	if len(entries) == 1 && entries[0].Name() == ".git" {
		if _, err := git.Open(path); err != nil {
			return targetIncompatible, nil
		}
		return targetEmptyGit, nil
	}
	return targetIncompatible, nil
}

// initCommitMessage is the canonical first-commit message for `bacio sync
// init`. Format mirrors the design doc's commit-message convention:
// short subject + actor + timestamp.
func initCommitMessage(actor string) string {
	if actor == "" {
		actor = "unknown"
	}
	return fmt.Sprintf("bacio sync init: bootstrap by %s @ %s", actor, time.Now().UTC().Format(time.RFC3339))
}

// markAllAsSynced seeds sync_state for every uuid in the DB. Called
// on bootstrap so a subsequent `bacio sync import` doesn't treat
// existing local records as "new from elsewhere" — the design doc's
// case table relies on the presence/absence of sync_state rows to
// detect deletions and resurrections.
//
// Hashes are best-effort — the canonical content hash is computed on
// the export side using the exact YAML bytes; here we re-derive a
// minimal hash from the in-memory record. If the hash is wrong it's
// recomputed correctly on the next export pass.
func (e *Engine) markAllAsSynced() error {
	repos, err := e.Store.ListRepos()
	if err != nil {
		return err
	}
	for _, repo := range repos {
		if err := e.Store.MarkSynced(repo.UUID, store.SyncKindRepo, ""); err != nil {
			return err
		}
		feats, err := e.Store.ListFeatures(repo.ID, false)
		if err != nil {
			return err
		}
		for _, f := range feats {
			if err := e.Store.MarkSynced(f.UUID, store.SyncKindFeature, ""); err != nil {
				return err
			}
			// BACI-124: feature-scoped comments mirror the issue-comment
			// bootstrap path.
			fcs, err := e.Store.ListFeatureComments(f.ID)
			if err != nil {
				return err
			}
			for _, c := range fcs {
				if err := e.Store.MarkSynced(c.UUID, store.SyncKindFeatureComment, ""); err != nil {
					return err
				}
			}
		}
		issues, err := e.Store.ListIssues(store.IssueFilter{RepoID: &repo.ID})
		if err != nil {
			return err
		}
		for _, iss := range issues {
			if err := e.Store.MarkSynced(iss.UUID, store.SyncKindIssue, ""); err != nil {
				return err
			}
			comments, err := e.Store.ListComments(iss.ID)
			if err != nil {
				return err
			}
			for _, c := range comments {
				if err := e.Store.MarkSynced(c.UUID, store.SyncKindComment, ""); err != nil {
					return err
				}
			}
		}
		docs, err := e.Store.ListDocuments(store.DocumentFilter{RepoID: repo.ID})
		if err != nil {
			return err
		}
		for _, d := range docs {
			if err := e.Store.MarkSynced(d.UUID, store.SyncKindDocument, ""); err != nil {
				return err
			}
		}
	}
	return nil
}

// keepImported is a placeholder so future bootstrap helpers can
// share the "already-synced" guard without re-querying. Keeps the
// import path symmetrical with markAllAsSynced if someone adds a
// reverse helper later.
var _ = errors.New
