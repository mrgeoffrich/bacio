package client

// Tests for LinkPhantomRepo (BACI-112) — the cross-surface backend
// behind `bacio repo link`, `POST /repos/{prefix}/link`, the desktop /
// web PhantomLinkModal, and the TUI `l`-key overlay.
//
// Branch coverage: every typed-error Kind plus the idempotent re-link
// path and the dry-run projection. These tests use real git on disk
// (via the existing requireGit helper) because git.Detect shells out.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/store"
	bsync "github.com/mrgeoffrich/bacio/internal/sync"
)

// seedPhantom inserts a phantom repo row with the given prefix and
// remote URL — the on-import shape `bacio sync clone` writes.
func seedPhantom(t *testing.T, s *store.Store, prefix, remoteURL string) {
	t.Helper()
	if _, err := s.CreatePhantomRepo("uuid-"+prefix, prefix, prefix, remoteURL); err != nil {
		t.Fatalf("CreatePhantomRepo %s: %v", prefix, err)
	}
}

// seedSyncRemoteWithPrefix sets up a sync_remotes row whose local
// clone carries a repos/<prefix>/ directory — the shape
// discoverOwningSyncRemote walks. Returns the remote URL it wrote.
func seedSyncRemoteWithPrefix(t *testing.T, s *store.Store, dir, remoteURL, prefix string) string {
	t.Helper()
	syncRoot := filepath.Join(dir, "sync-clone")
	if err := os.MkdirAll(filepath.Join(syncRoot, "repos", prefix), 0o755); err != nil {
		t.Fatalf("mkdir sync clone repos: %v", err)
	}
	if err := s.UpsertSyncRemote(remoteURL, syncRoot); err != nil {
		t.Fatalf("UpsertSyncRemote: %v", err)
	}
	return syncRoot
}

// makeGitWorkingTree initialises a fresh git working tree at `path` so
// git.Detect returns nil. Skips the test when git isn't on PATH.
func makeGitWorkingTree(t *testing.T, path string) {
	t.Helper()
	requireGit(t)
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	runGit(t, "", "init", "-b", "main", path)
	setupGitIdentity(t, path)
}

// asLinkErr collapses errors.As + Kind assertion into one helper so the
// per-branch tests read cleanly.
func asLinkErr(t *testing.T, err error, wantKind string) *RepoLinkError {
	t.Helper()
	if err == nil {
		t.Fatalf("expected RepoLinkError(%s), got nil", wantKind)
	}
	var le *RepoLinkError
	if !errors.As(err, &le) {
		t.Fatalf("expected RepoLinkError, got %T: %v", err, err)
	}
	if le.Kind != wantKind {
		t.Fatalf("RepoLinkError.Kind = %q, want %q (msg=%s)", le.Kind, wantKind, le.Error())
	}
	return le
}

// TestLinkPhantomRepo_HappyPath exercises the canonical link flow:
// a phantom row + an on-disk sync clone carrying repos/<prefix>/ + a
// valid target working tree → the row is upgraded, the project's
// .bacio/config.yaml is written, and the audit row records the
// explicit-link details.
func TestLinkPhantomRepo_HappyPath(t *testing.T) {
	requireGit(t)
	c, _ := openTestLocalClient(t)
	dir := t.TempDir()
	seedPhantom(t, c.store, "MINI", "git@example.com:user/mini.git")
	seedSyncRemoteWithPrefix(t, c.store, dir, "git@example.com:user/team-sync.git", "MINI")

	target := filepath.Join(dir, "mini-checkout")
	makeGitWorkingTree(t, target)

	result, err := c.LinkPhantomRepo(context.Background(), "MINI", target, false)
	if err != nil {
		t.Fatalf("LinkPhantomRepo: %v", err)
	}
	if result.AlreadyLinked {
		t.Errorf("AlreadyLinked = true, want false on first link")
	}
	if result.Repo == nil || result.Repo.Path != target {
		t.Errorf("Repo.Path = %v, want %s", result.Repo, target)
	}
	if result.SyncRemoteURL != "git@example.com:user/team-sync.git" {
		t.Errorf("SyncRemoteURL = %q, want git@example.com:user/team-sync.git", result.SyncRemoteURL)
	}

	// The .bacio/config.yaml lives at <target>/.bacio/config.yaml and
	// points at the sync remote URL.
	cfg, err := bsync.ReadProjectConfig(target)
	if err != nil {
		t.Fatalf("ReadProjectConfig: %v", err)
	}
	if cfg.Sync.Remote != "git@example.com:user/team-sync.git" {
		t.Errorf("config Sync.Remote = %q, want git@example.com:user/team-sync.git", cfg.Sync.Remote)
	}

	// Audit row recorded under repo.upgrade_phantom.
	hist, err := c.store.ListHistory(store.HistoryFilter{})
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	found := false
	for _, h := range hist {
		if h.Op == "repo.upgrade_phantom" && h.RepoPrefix == "MINI" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected one repo.upgrade_phantom audit row, got %d total", len(hist))
	}
}

// TestLinkPhantomRepo_Idempotent confirms a second link with the same
// path is a no-op: AlreadyLinked=true, no second audit row, the config
// is left as-is.
func TestLinkPhantomRepo_Idempotent(t *testing.T) {
	requireGit(t)
	c, _ := openTestLocalClient(t)
	dir := t.TempDir()
	seedPhantom(t, c.store, "MINI", "git@example.com:user/mini.git")
	seedSyncRemoteWithPrefix(t, c.store, dir, "git@example.com:user/team-sync.git", "MINI")
	target := filepath.Join(dir, "mini-checkout")
	makeGitWorkingTree(t, target)

	if _, err := c.LinkPhantomRepo(context.Background(), "MINI", target, false); err != nil {
		t.Fatalf("first link: %v", err)
	}
	histBefore, _ := c.store.ListHistory(store.HistoryFilter{})

	result, err := c.LinkPhantomRepo(context.Background(), "MINI", target, false)
	if err != nil {
		t.Fatalf("second link: %v", err)
	}
	if !result.AlreadyLinked {
		t.Errorf("AlreadyLinked = false on re-link, want true")
	}
	histAfter, _ := c.store.ListHistory(store.HistoryFilter{})
	if len(histAfter) != len(histBefore) {
		t.Errorf("history grew on re-link: before=%d after=%d", len(histBefore), len(histAfter))
	}
}

// TestLinkPhantomRepo_NotPhantom: the row exists but already has a
// path. Linking errors with kind=not_phantom carrying current_path.
func TestLinkPhantomRepo_NotPhantom(t *testing.T) {
	c, _ := openTestLocalClient(t)
	dir := t.TempDir()
	seedRepo(t, c.store, "MINI", filepath.Join(dir, "already-bound"))
	// Set up the sync remote so the discovery step isn't what fails.
	seedSyncRemoteWithPrefix(t, c.store, dir, "git@example.com:user/team-sync.git", "MINI")
	target := filepath.Join(dir, "another-checkout")

	_, err := c.LinkPhantomRepo(context.Background(), "MINI", target, false)
	le := asLinkErr(t, err, "not_phantom")
	if le.CurrentPath != filepath.Join(dir, "already-bound") {
		t.Errorf("CurrentPath = %q, want %q", le.CurrentPath, filepath.Join(dir, "already-bound"))
	}
}

// TestLinkPhantomRepo_PathRelative rejects a relative path.
func TestLinkPhantomRepo_PathRelative(t *testing.T) {
	c, _ := openTestLocalClient(t)
	seedPhantom(t, c.store, "MINI", "git@example.com:user/mini.git")
	_, err := c.LinkPhantomRepo(context.Background(), "MINI", "./relative", false)
	asLinkErr(t, err, "path_not_absolute")
}

// TestLinkPhantomRepo_PathMissing rejects a path that doesn't exist on
// disk.
func TestLinkPhantomRepo_PathMissing(t *testing.T) {
	c, _ := openTestLocalClient(t)
	seedPhantom(t, c.store, "MINI", "git@example.com:user/mini.git")
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	_, err := c.LinkPhantomRepo(context.Background(), "MINI", missing, false)
	asLinkErr(t, err, "path_not_exists")
}

// TestLinkPhantomRepo_PathNotGit rejects a directory that exists but
// isn't a git working tree.
func TestLinkPhantomRepo_PathNotGit(t *testing.T) {
	requireGit(t)
	c, _ := openTestLocalClient(t)
	seedPhantom(t, c.store, "MINI", "git@example.com:user/mini.git")
	notGit := t.TempDir()
	_, err := c.LinkPhantomRepo(context.Background(), "MINI", notGit, false)
	asLinkErr(t, err, "path_not_git")
}

// TestLinkPhantomRepo_PathAlreadyBound rejects a path that's already
// the working tree of another non-phantom repo, carrying the other
// prefix in ExistingPrefix.
func TestLinkPhantomRepo_PathAlreadyBound(t *testing.T) {
	requireGit(t)
	c, _ := openTestLocalClient(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "shared-checkout")
	makeGitWorkingTree(t, target)
	// Existing real repo bound to the target path.
	seedRepo(t, c.store, "OTHER", target)
	// Phantom row trying to claim the same path.
	seedPhantom(t, c.store, "MINI", "git@example.com:user/mini.git")
	seedSyncRemoteWithPrefix(t, c.store, dir, "git@example.com:user/team-sync.git", "MINI")

	_, err := c.LinkPhantomRepo(context.Background(), "MINI", target, false)
	le := asLinkErr(t, err, "path_already_bound")
	if le.ExistingPrefix != "OTHER" {
		t.Errorf("ExistingPrefix = %q, want OTHER", le.ExistingPrefix)
	}
}

// TestLinkPhantomRepo_NoOwningSyncRepo rejects a link when no
// sync_remotes row's local clone carries repos/<prefix>/.
func TestLinkPhantomRepo_NoOwningSyncRepo(t *testing.T) {
	requireGit(t)
	c, _ := openTestLocalClient(t)
	dir := t.TempDir()
	seedPhantom(t, c.store, "MINI", "git@example.com:user/mini.git")
	target := filepath.Join(dir, "mini-checkout")
	makeGitWorkingTree(t, target)

	_, err := c.LinkPhantomRepo(context.Background(), "MINI", target, false)
	asLinkErr(t, err, "no_owning_sync_repo")
}

// TestLinkPhantomRepo_DryRun returns the projected RepoLinkResult
// without touching the store or the filesystem.
func TestLinkPhantomRepo_DryRun(t *testing.T) {
	requireGit(t)
	c, _ := openTestLocalClient(t)
	dir := t.TempDir()
	seedPhantom(t, c.store, "MINI", "git@example.com:user/mini.git")
	seedSyncRemoteWithPrefix(t, c.store, dir, "git@example.com:user/team-sync.git", "MINI")
	target := filepath.Join(dir, "mini-checkout")
	makeGitWorkingTree(t, target)

	result, err := c.LinkPhantomRepo(context.Background(), "MINI", target, true)
	if err != nil {
		t.Fatalf("LinkPhantomRepo dry-run: %v", err)
	}
	if !result.WouldLink {
		t.Errorf("WouldLink = false, want true on dry-run")
	}
	if result.Repo == nil || result.Repo.Path != target {
		t.Errorf("Repo.Path projection = %v, want %s", result.Repo, target)
	}
	if result.SyncRemoteURL != "git@example.com:user/team-sync.git" {
		t.Errorf("SyncRemoteURL = %q, want git@example.com:user/team-sync.git", result.SyncRemoteURL)
	}

	// Store untouched: the row is still a phantom.
	got, err := c.store.GetRepoByPrefix("MINI")
	if err != nil {
		t.Fatalf("GetRepoByPrefix: %v", err)
	}
	if got.Path != "" {
		t.Errorf("post-dryrun Path = %q, want '' (no mutation)", got.Path)
	}
	// .bacio/config.yaml not written on dry-run.
	if _, err := os.Stat(filepath.Join(target, ".bacio", "config.yaml")); !os.IsNotExist(err) {
		t.Errorf("dry-run wrote .bacio/config.yaml (err=%v); should be a no-op", err)
	}
	// Audit log carries no repo.upgrade_phantom row.
	hist, _ := c.store.ListHistory(store.HistoryFilter{})
	for _, h := range hist {
		if h.Op == "repo.upgrade_phantom" {
			t.Errorf("dry-run wrote audit row %+v", h)
		}
	}
}

// TestLinkPhantomRepo_NotFound surfaces store.ErrNotFound for an unknown
// prefix — the HTTP handler relies on this to return 404.
func TestLinkPhantomRepo_NotFound(t *testing.T) {
	c, _ := openTestLocalClient(t)
	_, err := c.LinkPhantomRepo(context.Background(), "NOPE", "/tmp", false)
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("err = %v, want store.ErrNotFound", err)
	}
}
