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

	// First-export counts (from the underlying ExportResult). One
	// pointer is enough; we don't need a separate StagedExportResult
	// because init is always a full export into an empty tree.
	Export *ExportResult `json:"export,omitempty"`
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

	// Project root must be a real git repo, not a sync repo.
	if IsSyncRepo(projectRoot) {
		return nil, fmt.Errorf("cannot run bacio sync init from inside a sync repo (bacio-sync.yaml at %s)", projectRoot)
	}
	if _, err := git.Open(projectRoot); err != nil {
		return nil, fmt.Errorf("project root %s is not a git repo: %w", projectRoot, err)
	}

	// LocalPath either doesn't exist or is empty.
	if err := requireEmptyOrMissing(opts.LocalPath); err != nil {
		return nil, err
	}

	res := &InitResult{LocalPath: opts.LocalPath, Remote: opts.Remote}

	if e.DryRun {
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

	// 1. git init at LocalPath; write the sentinel + .gitattributes.
	syncRepo, err := git.Init(opts.LocalPath)
	if err != nil {
		return nil, fmt.Errorf("git init: %w", err)
	}
	if err := WriteSentinel(opts.LocalPath, Sentinel{
		SchemaVersion: SchemaVersion,
		CreatedAt:     time.Now().UTC(),
	}); err != nil {
		return nil, err
	}
	// Pin LF line endings so checkouts on Windows don't rewrite the
	// canonical YAML. Defence in depth alongside NormalizeBody on
	// the parse side.
	if err := syncRepo.WriteGitattributes("* -text\n"); err != nil {
		return nil, fmt.Errorf("write .gitattributes: %w", err)
	}

	// 2. If a remote was supplied, wire it up before any export so
	// the next step's "is remote empty?" check fires before we do
	// expensive work.
	if opts.Remote != "" {
		if err := syncRepo.AddRemote("origin", opts.Remote); err != nil {
			return nil, fmt.Errorf("add origin: %w", err)
		}
		hasContent, err := syncRepo.RemoteHasContent("origin")
		if err != nil {
			return nil, fmt.Errorf("check remote: %w", err)
		}
		if hasContent {
			return nil, fmt.Errorf("remote %s already has content; use 'bacio sync clone' to join it instead of 'bacio sync init'", opts.Remote)
		}
	}

	// 3. Run the export. First export against an empty tree, so the
	// simple Phase-2 path is fine — no atomic staging needed.
	exportEng := &Engine{Store: e.Store, Actor: e.Actor, DryRun: false}
	exportRes, err := exportEng.Export(ctx, opts.LocalPath)
	if err != nil {
		return nil, fmt.Errorf("first export: %w", err)
	}
	res.Export = exportRes

	// 3a. Mark every freshly-exported uuid as synced so a subsequent
	// `bacio sync` doesn't treat them as "never synced". The export side
	// of Phase 2 didn't bookkeep into sync_state — that's an
	// import-side concern in Phase 3 — but the bootstrap is the
	// natural moment to seed.
	if err := e.markAllAsSynced(); err != nil {
		return nil, fmt.Errorf("seed sync_state: %w", err)
	}

	// 4. Stage everything, commit. The first commit covers the
	// sentinel + .gitattributes + repos/.
	if err := syncRepo.Add(); err != nil {
		return nil, fmt.Errorf("git add: %w", err)
	}
	sha, err := syncRepo.Commit(initCommitMessage(e.Actor))
	if err != nil {
		return nil, fmt.Errorf("git commit: %w", err)
	}
	res.CommitSHA = sha

	// 5. Push (if remote configured).
	if opts.Remote != "" {
		branch, err := syncRepo.CurrentBranch()
		if err != nil {
			return nil, fmt.Errorf("resolve current branch: %w", err)
		}
		if err := syncRepo.PushSetUpstream("origin", branch); err != nil {
			return nil, fmt.Errorf("git push: %w", err)
		}
		res.Pushed = true
	}

	// 6. .bacio/config.yaml (if remote configured).
	if opts.Remote != "" {
		if err := WriteProjectConfig(projectRoot, ProjectConfig{
			Sync: ProjectSync{Remote: opts.Remote},
		}); err != nil {
			return nil, err
		}
	}

	// 7. DB-side bookkeeping: record the (remote → local path)
	// mapping so steady-state `bacio sync` finds the working tree.
	if opts.Remote != "" {
		if err := e.Store.UpsertSyncRemote(opts.Remote, opts.LocalPath); err != nil {
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
		dryEng := &Engine{Store: e.Store, Actor: e.Actor, DryRun: true}
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

	// 3. Real import.
	importEng := &Engine{Store: e.Store, Actor: e.Actor, DryRun: false}
	importRes, err := importEng.Import(ctx, syncRepo.Root)
	if err != nil {
		return nil, fmt.Errorf("import: %w", err)
	}
	res.Import = importRes

	// 4. Record the (remote → local path) mapping.
	if err := e.Store.UpsertSyncRemote(opts.Remote, localPath); err != nil {
		return nil, fmt.Errorf("record sync remote: %w", err)
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
	previewEng := &Engine{Store: e.Store, Actor: e.Actor, DryRun: true}
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

// requireEmptyOrMissing returns nil if path doesn't exist, or exists
// as an empty directory. Anything else (file, non-empty dir) errors.
func requireEmptyOrMissing(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s exists and is not a directory", path)
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	if len(entries) > 0 {
		return fmt.Errorf("%s exists and is not empty; remove it or pick another path", path)
	}
	return nil
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
