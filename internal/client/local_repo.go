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
	bsync "github.com/mrgeoffrich/bacio/internal/sync"
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

// LinkPhantomRepo (BACI-112) is the cross-surface implementation behind
// the `bacio repo link` CLI verb, `POST /repos/{prefix}/link`, the
// desktop / web `PhantomLinkModal`, and the TUI's `l`-key overlay on a
// phantom row. One method, four surfaces — see the matching plan note.
//
// Pipeline:
//
//  1. Look up the phantom by prefix; canonicalise prefix to upper.
//  2. Idempotency short-circuit: if `repos.path` already equals `path`,
//     return AlreadyLinked=true and skip every side effect (no upgrade,
//     no config write, no audit row). The first explicit link writes
//     audit; a re-link is a no-op for both the DB and the log.
//  3. Reject if the row is not a phantom (path != "").
//  4. Validate the supplied path: non-empty, absolute, exists + is a
//     dir, passes git.Detect (it's a git working tree).
//  5. Reject if another non-phantom repo is already bound to the path.
//  6. Discover the owning sync repo by walking sync_remotes and
//     stat-ing each `local_path/repos/<prefix>/`. Fail loud if no sync
//     repo on this machine carries the phantom.
//  7. dryRun short-circuits here with WouldLink=true.
//  8. UpgradePhantomRepo(uuid, path) — the store-side commit.
//  9. WriteProjectConfig(path, ...) — best-effort. If the store wrote
//     but the config fails, surface the error with a hint that the
//     user can re-run `bacio sync init/clone/attach` from inside the
//     now-linked checkout to heal it. We deliberately do NOT roll back
//     the DB write — the inverse (config-first, DB second) leaves a
//     stale config on a different failure path.
// 10. Record the audit row reusing the existing `repo.upgrade_phantom`
//     op so cwd-implicit and CLI-explicit upgrades share one history
//     facet (distinguished by the Details prefix).
func (c *localClient) LinkPhantomRepo(ctx context.Context, prefix, path string, dryRun bool) (*RepoLinkResult, error) {
	prefix = strings.ToUpper(strings.TrimSpace(prefix))
	if prefix == "" {
		return nil, fmt.Errorf("prefix is required")
	}
	repo, err := c.store.GetRepoByPrefix(prefix)
	if err != nil {
		return nil, err
	}

	// (2) Idempotent re-link: the row already points at this path.
	// Return success with AlreadyLinked=true and no side effects.
	if repo.HasWorkingTree() && repo.Path == path {
		return &RepoLinkResult{Repo: repo, AlreadyLinked: true}, nil
	}

	// (3a) A workspace is pathless like a phantom but is NOT one: it has
	// no git project waiting somewhere to be bound to. Linking one would
	// break the store's (kind='workspace' ⇒ path='') invariant, so refuse
	// with its own Kind rather than the misleading "not a phantom".
	if repo.IsWorkspace() {
		return nil, &RepoLinkError{
			Kind: "workspace", Prefix: prefix, Path: path,
			Message: "repo " + prefix + " is a workspace, not a git repo — a workspace has no working tree to link. " +
				"Register the git repository itself with `bacio repo add` (or by running bacio inside it)",
		}
	}

	// (3b) Not a phantom — bail before touching the user-supplied path.
	if !repo.IsPhantom() {
		return nil, &RepoLinkError{Kind: "not_phantom", Prefix: prefix, CurrentPath: repo.Path}
	}

	// (4) Path validation. Empty / relative / non-existent / non-git
	// each surface as a distinct Kind so the HTTP handler can map.
	if strings.TrimSpace(path) == "" {
		return nil, &RepoLinkError{Kind: "path_not_absolute", Prefix: prefix, Path: path,
			Message: "path is required"}
	}
	if !filepath.IsAbs(path) {
		return nil, &RepoLinkError{Kind: "path_not_absolute", Prefix: prefix, Path: path}
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, &RepoLinkError{Kind: "path_not_exists", Prefix: prefix, Path: path}
		}
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}
	if !info.IsDir() {
		return nil, &RepoLinkError{Kind: "path_not_git", Prefix: prefix, Path: path,
			Message: "path is not a directory: " + path}
	}
	if _, gerr := git.Detect(path); gerr != nil {
		// git.Detect returns ErrNotARepo whether the path is outside
		// any git repo or git is unusable; either way it's not the
		// working tree we can bind to.
		return nil, &RepoLinkError{Kind: "path_not_git", Prefix: prefix, Path: path}
	}

	// (5) Path-conflict check — another non-phantom repo already owns
	// this path. We surface its prefix so the caller can render a
	// useful error.
	if existing, gerr := c.store.GetRepoByPath(path); gerr == nil && existing != nil {
		return nil, &RepoLinkError{
			Kind: "path_already_bound", Prefix: prefix, Path: path,
			ExistingPrefix: existing.Prefix,
		}
	} else if gerr != nil && !errors.Is(gerr, store.ErrNotFound) {
		return nil, fmt.Errorf("lookup repo by path: %w", gerr)
	}

	// (6) Discover the owning sync repo. The phantom's own
	// remote_url is the *project's* git origin (from the imported
	// repo.yaml); the sync repo's URL is the one we need to write to
	// .bacio/config.yaml. Walk sync_remotes and find the first row
	// whose local clone carries repos/<prefix>/ on disk.
	syncRemoteURL, err := discoverOwningSyncRemote(c.store, prefix)
	if err != nil {
		return nil, err
	}

	// (7) Dry-run short-circuit. The projected row is the existing
	// phantom with `path` filled in; we don't mutate the model.Repo
	// pointer so the caller doesn't see a phantom row with a populated
	// path leaking into other lookups.
	if dryRun {
		projected := *repo
		projected.Path = path
		return &RepoLinkResult{
			Repo:          &projected,
			SyncRemoteURL: syncRemoteURL,
			WouldLink:     true,
		}, nil
	}

	// (8) Commit the upgrade.
	if err := c.store.UpgradePhantomRepo(repo.UUID, path); err != nil {
		return nil, fmt.Errorf("upgrade phantom %s: %w", prefix, err)
	}

	// (9) Write the project config. Best-effort: if this fails the
	// upgrade has already committed, so we surface a partial-success
	// error rather than rolling back. The user can re-run sync
	// setup from inside the now-linked checkout to heal it.
	if err := bsync.WriteProjectConfig(path, bsync.ProjectConfig{
		Sync: bsync.ProjectSync{Remote: syncRemoteURL},
	}); err != nil {
		return nil, fmt.Errorf("link committed but writing .bacio/config.yaml failed (re-run `bacio sync attach` from %s to heal): %w", path, err)
	}

	// (10) Audit row. Reuse `repo.upgrade_phantom` so cwd-implicit and
	// CLI-explicit upgrades share one history-filter facet; distinguish
	// via Details so the audit reader can tell them apart.
	upgraded, err := c.store.GetRepoByID(repo.ID)
	if err != nil {
		return nil, err
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &upgraded.ID, RepoPrefix: upgraded.Prefix,
		Op: "repo.upgrade_phantom", Kind: "repo",
		TargetID: &upgraded.ID, TargetLabel: upgraded.Prefix,
		Details:  fmt.Sprintf("explicit link: path=%s sync_remote=%s", path, syncRemoteURL),
	})
	return &RepoLinkResult{Repo: upgraded, SyncRemoteURL: syncRemoteURL}, nil
}

// discoverOwningSyncRemote walks the sync_remotes registry and returns
// the URL of the first sync repo whose local clone carries
// `repos/<prefix>/` on disk. Returns a `no_owning_sync_repo`
// RepoLinkError when no sync repo on this machine has a folder for the
// phantom prefix — the caller has to clone the sync repo first.
//
// We deliberately do NOT reuse `sync.BuildRegistry` here — that's an
// N-walk to gather *every* membership, and we only need the first hit
// for one prefix. The small direct loop is leaner and keeps the
// lookup inside this file.
func discoverOwningSyncRemote(s *store.Store, prefix string) (string, error) {
	remotes, err := s.ListSyncRemotes()
	if err != nil {
		return "", fmt.Errorf("list sync remotes: %w", err)
	}
	for _, rec := range remotes {
		if rec == nil || rec.LocalPath == "" {
			continue
		}
		candidate := filepath.Join(rec.LocalPath, "repos", prefix)
		info, err := os.Stat(candidate)
		if err != nil || !info.IsDir() {
			continue
		}
		return rec.RemoteURL, nil
	}
	return "", &RepoLinkError{Kind: "no_owning_sync_repo", Prefix: prefix}
}
