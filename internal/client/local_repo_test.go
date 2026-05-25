package client

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/store"
	bsync "github.com/mrgeoffrich/bacio/internal/sync"
)

// linkTestEnv bundles the fixtures every LinkPhantomRepo test rebuilds:
// an open *store.Store, the wrapping local client (actor stamped so
// audit rows are clean), and the directories the test seeded for the
// sync repo + the project working tree it should bind to.
type linkTestEnv struct {
	t      *testing.T
	store  *store.Store
	client *localClient
	// dir is the temp-dir root; sub-paths derive from it.
	dir string
	// projectPath is a git-initialised working tree we'll link the
	// phantom to (set up via initGitRepo). Empty for tests that don't
	// need it.
	projectPath string
	// syncLocalPath is the local clone of a sync repo carrying
	// repos/<prefix>/ on disk. Empty for tests that exercise the
	// "no owning sync repo" path.
	syncLocalPath string
}

// newLinkTestEnv builds the standard happy-path fixture: a phantom
// repo row with prefix TEST, a sync_remotes entry whose local clone
// carries repos/TEST/, and a fresh git-initialised target path. Sub-
// tests can opt out of the latter two by either deleting them or
// pointing at a different path.
func newLinkTestEnv(t *testing.T) *linkTestEnv {
	t.Helper()
	dir := t.TempDir()

	dbPath := filepath.Join(dir, "db.sqlite")
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	// Phantom repo row: path == "" by definition.
	if _, err := s.CreatePhantomRepo("test-uuid-aaaa", "TEST", "test-project", "git@example.com:dev/test-project.git"); err != nil {
		t.Fatalf("CreatePhantomRepo: %v", err)
	}

	// Sync repo on disk: <syncLocalPath>/repos/TEST/ exists.
	syncLocalPath := filepath.Join(dir, "sync-clone")
	if err := os.MkdirAll(filepath.Join(syncLocalPath, "repos", "TEST"), 0o755); err != nil {
		t.Fatalf("mkdir sync clone: %v", err)
	}
	if err := s.UpsertSyncRemote("git@example.com:dev/team-sync.git", syncLocalPath); err != nil {
		t.Fatalf("UpsertSyncRemote: %v", err)
	}

	// Target working tree: empty git repo.
	projectPath := filepath.Join(dir, "project")
	initGitRepo(t, projectPath)

	return &linkTestEnv{
		t:             t,
		store:         s,
		client:        &localClient{store: s, actor: "tester"},
		dir:           dir,
		projectPath:   projectPath,
		syncLocalPath: syncLocalPath,
	}
}

// initGitRepo creates dir + runs `git init` inside it. Tests that need
// a working tree the validator will accept call this. Skips the test
// if git isn't on PATH (unlikely on dev / CI but cheap to guard).
func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	cmd := exec.Command("git", "init", "--quiet", dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
}

func TestLinkPhantomRepo_HappyPath(t *testing.T) {
	env := newLinkTestEnv(t)

	res, err := env.client.LinkPhantomRepo(context.Background(), "TEST", env.projectPath, false)
	if err != nil {
		t.Fatalf("LinkPhantomRepo: %v", err)
	}
	if res.AlreadyLinked {
		t.Fatalf("AlreadyLinked = true on first link; want false")
	}
	if res.Repo == nil || res.Repo.Path != env.projectPath {
		t.Fatalf("Repo.Path = %q; want %q", res.Repo.Path, env.projectPath)
	}
	if res.SyncRemoteURL != "git@example.com:dev/team-sync.git" {
		t.Fatalf("SyncRemoteURL = %q; want git@example.com:dev/team-sync.git", res.SyncRemoteURL)
	}
	// Config file present + carries the owning sync URL.
	cfg, err := bsync.ReadProjectConfig(env.projectPath)
	if err != nil {
		t.Fatalf("ReadProjectConfig: %v", err)
	}
	if cfg.Sync.Remote != "git@example.com:dev/team-sync.git" {
		t.Fatalf(".bacio/config.yaml sync.remote = %q; want git@example.com:dev/team-sync.git", cfg.Sync.Remote)
	}
	// Audit row recorded with the explicit-link details.
	hist, err := env.store.ListHistory(store.HistoryFilter{})
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	var found bool
	for _, h := range hist {
		if h.Op == "repo.upgrade_phantom" {
			found = true
			if h.Details == "" || h.Details[:8] != "explicit" {
				t.Fatalf("audit row Details = %q; want it to start with 'explicit'", h.Details)
			}
		}
	}
	if !found {
		t.Fatalf("no repo.upgrade_phantom audit row recorded; got %d rows", len(hist))
	}
}

func TestLinkPhantomRepo_Idempotent(t *testing.T) {
	env := newLinkTestEnv(t)
	ctx := context.Background()

	if _, err := env.client.LinkPhantomRepo(ctx, "TEST", env.projectPath, false); err != nil {
		t.Fatalf("first link: %v", err)
	}
	res, err := env.client.LinkPhantomRepo(ctx, "TEST", env.projectPath, false)
	if err != nil {
		t.Fatalf("second link: %v", err)
	}
	if !res.AlreadyLinked {
		t.Fatalf("second link AlreadyLinked = false; want true")
	}
	// Exactly one audit row — the second call must NOT have written one.
	hist, err := env.store.ListHistory(store.HistoryFilter{})
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	count := 0
	for _, h := range hist {
		if h.Op == "repo.upgrade_phantom" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("audit row count = %d after idempotent re-link; want 1", count)
	}
}

func TestLinkPhantomRepo_NotPhantom(t *testing.T) {
	env := newLinkTestEnv(t)
	// Seed a non-phantom repo at a different prefix; aim the link at it.
	if _, err := env.store.CreateRepo("REAL", "real-repo", filepath.Join(env.dir, "real"), ""); err != nil {
		t.Fatalf("CreateRepo: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(env.dir, "real"), 0o755); err != nil {
		t.Fatalf("mkdir real: %v", err)
	}
	_, err := env.client.LinkPhantomRepo(context.Background(), "REAL", env.projectPath, false)
	var linkErr *RepoLinkError
	if !errors.As(err, &linkErr) {
		t.Fatalf("err = %v; want *RepoLinkError", err)
	}
	if linkErr.Kind != RepoLinkErrNotPhantom {
		t.Fatalf("Kind = %v; want %v", linkErr.Kind, RepoLinkErrNotPhantom)
	}
}

func TestLinkPhantomRepo_PathRelative(t *testing.T) {
	env := newLinkTestEnv(t)
	_, err := env.client.LinkPhantomRepo(context.Background(), "TEST", "./relative", false)
	var linkErr *RepoLinkError
	if !errors.As(err, &linkErr) || linkErr.Kind != RepoLinkErrPathNotAbsolute {
		t.Fatalf("err = %v; want RepoLinkErrPathNotAbsolute", err)
	}
}

func TestLinkPhantomRepo_PathMissing(t *testing.T) {
	env := newLinkTestEnv(t)
	missing := filepath.Join(env.dir, "no-such-dir")
	_, err := env.client.LinkPhantomRepo(context.Background(), "TEST", missing, false)
	var linkErr *RepoLinkError
	if !errors.As(err, &linkErr) || linkErr.Kind != RepoLinkErrPathNotExists {
		t.Fatalf("err = %v; want RepoLinkErrPathNotExists", err)
	}
}

func TestLinkPhantomRepo_PathNotGit(t *testing.T) {
	env := newLinkTestEnv(t)
	notGit := filepath.Join(env.dir, "not-git")
	if err := os.MkdirAll(notGit, 0o755); err != nil {
		t.Fatalf("mkdir notGit: %v", err)
	}
	_, err := env.client.LinkPhantomRepo(context.Background(), "TEST", notGit, false)
	var linkErr *RepoLinkError
	if !errors.As(err, &linkErr) || linkErr.Kind != RepoLinkErrPathNotGit {
		t.Fatalf("err = %v; want RepoLinkErrPathNotGit", err)
	}
}

func TestLinkPhantomRepo_PathAlreadyBound(t *testing.T) {
	env := newLinkTestEnv(t)
	// Pre-existing repo bound to projectPath; the phantom can't take it.
	if _, err := env.store.CreateRepo("OTHR", "other-repo", env.projectPath, ""); err != nil {
		t.Fatalf("CreateRepo: %v", err)
	}
	_, err := env.client.LinkPhantomRepo(context.Background(), "TEST", env.projectPath, false)
	var linkErr *RepoLinkError
	if !errors.As(err, &linkErr) {
		t.Fatalf("err = %v; want *RepoLinkError", err)
	}
	if linkErr.Kind != RepoLinkErrPathAlreadyBound {
		t.Fatalf("Kind = %v; want %v", linkErr.Kind, RepoLinkErrPathAlreadyBound)
	}
	if linkErr.ExistingPrefix != "OTHR" {
		t.Fatalf("ExistingPrefix = %q; want OTHR", linkErr.ExistingPrefix)
	}
}

func TestLinkPhantomRepo_NoOwningSyncRepo(t *testing.T) {
	env := newLinkTestEnv(t)
	// Remove the repos/TEST/ folder from the sync clone so the
	// owning-sync-repo discovery fails.
	if err := os.RemoveAll(filepath.Join(env.syncLocalPath, "repos", "TEST")); err != nil {
		t.Fatalf("remove repos/TEST: %v", err)
	}
	_, err := env.client.LinkPhantomRepo(context.Background(), "TEST", env.projectPath, false)
	var linkErr *RepoLinkError
	if !errors.As(err, &linkErr) || linkErr.Kind != RepoLinkErrNoOwningSyncRepo {
		t.Fatalf("err = %v; want RepoLinkErrNoOwningSyncRepo", err)
	}
}

func TestLinkPhantomRepo_DryRun(t *testing.T) {
	env := newLinkTestEnv(t)
	res, err := env.client.LinkPhantomRepo(context.Background(), "TEST", env.projectPath, true)
	if err != nil {
		t.Fatalf("dry-run LinkPhantomRepo: %v", err)
	}
	if res.Repo == nil || res.Repo.Path != env.projectPath {
		t.Fatalf("dry-run projected Repo.Path = %q; want %q", res.Repo.Path, env.projectPath)
	}
	if res.SyncRemoteURL == "" {
		t.Fatalf("dry-run SyncRemoteURL is empty; want resolved URL")
	}
	// Store still has the phantom unchanged.
	repo, err := env.store.GetRepoByPrefix("TEST")
	if err != nil {
		t.Fatalf("GetRepoByPrefix: %v", err)
	}
	if repo.Path != "" {
		t.Fatalf("dry-run mutated repo.Path = %q; want empty", repo.Path)
	}
	// No config file written.
	if _, err := os.Stat(filepath.Join(env.projectPath, ".bacio", "config.yaml")); !os.IsNotExist(err) {
		t.Fatalf("dry-run wrote .bacio/config.yaml: stat err = %v", err)
	}
	// No audit row recorded.
	hist, err := env.store.ListHistory(store.HistoryFilter{})
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	for _, h := range hist {
		if h.Op == "repo.upgrade_phantom" {
			t.Fatalf("dry-run wrote a repo.upgrade_phantom audit row: %+v", h)
		}
	}
}
