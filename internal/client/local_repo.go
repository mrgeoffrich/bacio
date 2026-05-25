package client

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mrgeoffrich/bacio/internal/git"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
	"github.com/mrgeoffrich/bacio/internal/sync"
)

func (c *localClient) ListRepos(ctx context.Context) ([]*model.Repo, error) {
	repos, err := c.store.ListRepos()
	if err != nil {
		return nil, err
	}
	if repos == nil {
		repos = []*model.Repo{}
	}
	return repos, nil
}

func (c *localClient) GetRepoByPrefix(ctx context.Context, prefix string) (*model.Repo, error) {
	return c.store.GetRepoByPrefix(strings.ToUpper(prefix))
}

func (c *localClient) GetRepoByPath(ctx context.Context, path string) (*model.Repo, error) {
	return c.store.GetRepoByPath(path)
}

func (c *localClient) DeleteRepo(ctx context.Context, prefix, confirm string, dryRun bool) (*model.Repo, *RepoDeletePreview, error) {
	prefix = strings.ToUpper(strings.TrimSpace(prefix))
	repo, err := c.store.GetRepoByPrefix(prefix)
	if err != nil {
		return nil, nil, err
	}
	counts, err := c.store.RepoCascadeCountsForID(repo.ID)
	if err != nil {
		return nil, nil, err
	}
	preview := &RepoDeletePreview{Repo: repo, Cascade: counts, WouldDelete: true}

	// Dry-run is the agent-CLI rehearsal path: success, no changes,
	// no audit row. The error path below is the *abort* path; we
	// return it only when the caller actually intended to delete.
	if dryRun {
		return nil, preview, nil
	}

	// Confirm gate: backend-side, not CLI-side, so direct HTTP /
	// in-process callers can't bypass it by skipping a flag.
	if !strings.EqualFold(strings.TrimSpace(confirm), prefix) {
		return nil, nil, &RepoConfirmError{
			Prefix:     prefix,
			GotConfirm: confirm,
			Preview:    preview,
		}
	}

	// History rows aren't covered by the FK cascade (the schema
	// deliberately keeps `history` FK-less). Wipe them first so the
	// audit log doesn't carry dead-prefix entries forward; the final
	// `repo.delete` row written below has repo_id NULL.
	if err := c.store.DeleteHistoryByRepo(repo.ID); err != nil {
		return nil, nil, fmt.Errorf("delete history: %w", err)
	}
	if err := c.store.DeleteRepo(repo.ID); err != nil {
		return nil, nil, err
	}
	c.recordOp(model.HistoryEntry{
		// repo_id is NULL — the row no longer exists; the prefix
		// snapshot is what callers join on after a delete.
		RepoPrefix: repo.Prefix,
		Op:         "repo.delete", Kind: "repo",
		TargetLabel: repo.Prefix,
		Details:     formatCascadeDetails(repo, counts),
	})
	return repo, nil, nil
}

// formatCascadeDetails turns a RepoCascadeCounts into a one-line
// summary suitable for the audit log's `details` column.
func formatCascadeDetails(repo *model.Repo, c store.RepoCascadeCounts) string {
	return fmt.Sprintf("%s (%d issues, %d comments, %d features, %d documents, %d history)",
		repo.Name, c.Issues, c.Comments, c.Features, c.Documents, c.History)
}

// LinkPhantomRepo (BACI-112) binds a phantom repo row to a local git
// working tree. The validation pipeline catches each precondition
// failure separately so the caller can surface them as distinct
// HTTP / UI states; the actual store write reuses the existing
// Store.UpgradePhantomRepo so phantoms upgraded via this explicit
// path are indistinguishable from phantoms upgraded via the implicit
// cwd-driven path EnsureRepo runs on every command (audit-row
// `Details` string aside).
func (c *localClient) LinkPhantomRepo(ctx context.Context, prefix, path string, dryRun bool) (*RepoLinkResult, error) {
	prefix = strings.ToUpper(strings.TrimSpace(prefix))
	if prefix == "" {
		return nil, &RepoLinkError{
			Kind:    RepoLinkErrPathNotAbsolute, // prefix-missing is a 400 in spirit; reuse the bucket
			Message: "prefix is required",
		}
	}
	repo, err := c.store.GetRepoByPrefix(prefix)
	if err != nil {
		// store.ErrNotFound bubbles unchanged — the HTTP handler maps
		// it to 404 via statusForError; the CLI surfaces it as the
		// usual "not found" message.
		return nil, err
	}

	// Idempotency gate (Q4 from the brief): if the row already points
	// at exactly this path, skip everything — no store write, no
	// config rewrite, no audit row. Callers learn this happened via
	// AlreadyLinked=true so the UI can render a "nothing to do" hint
	// without faking success.
	if repo.Path != "" && repo.Path == path {
		return &RepoLinkResult{Repo: repo, AlreadyLinked: true}, nil
	}

	// Phantom gate: a non-empty path on a non-matching path means
	// this isn't a phantom and we'd be silently overwriting an
	// already-bound row. Refuse loudly.
	if repo.Path != "" {
		return nil, &RepoLinkError{
			Kind:    RepoLinkErrNotPhantom,
			Prefix:  prefix,
			Path:    repo.Path,
			Message: fmt.Sprintf("repo %s is already linked to %s; refusing to overwrite", prefix, repo.Path),
		}
	}

	// Path shape checks. Each is a separate failure bucket so the API
	// can return a precise 400 detail and the UI can surface the right
	// inline hint.
	if strings.TrimSpace(path) == "" {
		return nil, &RepoLinkError{
			Kind:    RepoLinkErrPathNotAbsolute,
			Prefix:  prefix,
			Message: "path is required",
		}
	}
	if !filepath.IsAbs(path) {
		return nil, &RepoLinkError{
			Kind:    RepoLinkErrPathNotAbsolute,
			Prefix:  prefix,
			Path:    path,
			Message: fmt.Sprintf("path must be absolute (got %q)", path),
		}
	}
	info, statErr := os.Stat(path)
	if statErr != nil || !info.IsDir() {
		return nil, &RepoLinkError{
			Kind:    RepoLinkErrPathNotExists,
			Prefix:  prefix,
			Path:    path,
			Message: fmt.Sprintf("path does not exist or is not a directory: %s", path),
		}
	}
	if _, derr := git.Detect(path); derr != nil {
		return nil, &RepoLinkError{
			Kind:    RepoLinkErrPathNotGit,
			Prefix:  prefix,
			Path:    path,
			Message: fmt.Sprintf("path is not a git working tree: %s", path),
		}
	}

	// Bind-collision gate. The schema's partial UNIQUE on repos(path)
	// would catch this at INSERT time, but we want a typed error with
	// the existing prefix instead of a raw constraint-violation string.
	if existing, perr := c.store.GetRepoByPath(path); perr == nil {
		return nil, &RepoLinkError{
			Kind:           RepoLinkErrPathAlreadyBound,
			Prefix:         prefix,
			Path:           path,
			ExistingPrefix: existing.Prefix,
			Message:        fmt.Sprintf("path %s is already linked to repo %s", path, existing.Prefix),
		}
	} else if !errors.Is(perr, store.ErrNotFound) {
		return nil, perr
	}

	// Discover the owning sync repo: walk sync_remotes and stat
	// <local_path>/repos/<prefix>/. First hit wins — sync_remotes
	// keyed by remote_url means the same prefix can't legitimately
	// live under two different sync repos on one machine.
	syncRemoteURL, err := c.discoverOwningSyncRemote(prefix)
	if err != nil {
		return nil, err
	}

	if dryRun {
		// Project the would-be-upgraded row without writing. The
		// caller renders this as the dry-run preview; the HTTP handler
		// wraps it in writeDryRun.
		projected := *repo
		projected.Path = path
		return &RepoLinkResult{Repo: &projected, SyncRemoteURL: syncRemoteURL}, nil
	}

	if err := c.store.UpgradePhantomRepo(repo.UUID, path); err != nil {
		return nil, fmt.Errorf("upgrade phantom %s: %w", prefix, err)
	}
	// Write .bacio/config.yaml AFTER the DB commit. If this fails the
	// DB is already upgraded — surface the partial-success error with
	// a hint that re-running sync setup from inside the now-linked
	// checkout will heal it. We don't roll back the DB write because
	// the alternative (config-first, DB second) leaves a stale config
	// pointing at no DB row on a different failure path; the brief
	// captured this trade-off explicitly.
	if werr := sync.WriteProjectConfig(path, sync.ProjectConfig{
		Sync: sync.ProjectSync{Remote: syncRemoteURL},
	}); werr != nil {
		// Audit the DB-side success even though the config write
		// failed, so `bacio history` records the upgrade we actually
		// committed.
		c.recordOp(model.HistoryEntry{
			RepoID: &repo.ID, RepoPrefix: repo.Prefix,
			Op: "repo.upgrade_phantom", Kind: "repo",
			TargetID: &repo.ID, TargetLabel: repo.Prefix,
			Details: fmt.Sprintf("explicit link: path=%s sync_remote=%s (config write failed: %v)", path, syncRemoteURL, werr),
		})
		return nil, fmt.Errorf("upgraded %s but failed to write %s/.bacio/config.yaml — re-run `bacio sync` from inside the working tree to heal: %w", prefix, path, werr)
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &repo.ID, RepoPrefix: repo.Prefix,
		Op: "repo.upgrade_phantom", Kind: "repo",
		TargetID: &repo.ID, TargetLabel: repo.Prefix,
		Details: fmt.Sprintf("explicit link: path=%s sync_remote=%s", path, syncRemoteURL),
	})
	upgraded, err := c.store.GetRepoByID(repo.ID)
	if err != nil {
		return nil, err
	}
	return &RepoLinkResult{Repo: upgraded, SyncRemoteURL: syncRemoteURL}, nil
}

// discoverOwningSyncRemote walks sync_remotes and returns the
// remote_url of the first row whose local clone carries a
// `repos/<prefix>/` folder. Returns a typed RepoLinkErrNoOwningSyncRepo
// error when nothing matches — the user must clone (or attach) the
// owning sync repo before they can link the phantom.
func (c *localClient) discoverOwningSyncRemote(prefix string) (string, error) {
	remotes, err := c.store.ListSyncRemotes()
	if err != nil {
		return "", fmt.Errorf("list sync remotes: %w", err)
	}
	for _, rec := range remotes {
		if rec == nil || rec.LocalPath == "" {
			continue
		}
		dir := filepath.Join(rec.LocalPath, "repos", strings.ToUpper(prefix))
		st, serr := os.Stat(dir)
		if serr == nil && st.IsDir() {
			return rec.RemoteURL, nil
		}
	}
	return "", &RepoLinkError{
		Kind:    RepoLinkErrNoOwningSyncRepo,
		Prefix:  prefix,
		Message: fmt.Sprintf("no sync repo on this machine carries repos/%s/ — clone (or attach) the owning sync repo first", prefix),
	}
}
