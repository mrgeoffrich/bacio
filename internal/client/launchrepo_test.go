package client

// Coverage for the BACI-368 launch-repo seam. Uses real git on disk
// (git.Detect shells out), so these skip when git isn't on PATH.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/store"
	bsync "github.com/mrgeoffrich/bacio/internal/sync"
)

// TestEnsureRepoForDir_EnrolsFreshRepo: a git repo bacio has never
// seen is registered on the spot, with the repo.create audit row.
func TestEnsureRepoForDir_EnrolsFreshRepo(t *testing.T) {
	requireGit(t)
	c, _ := openTestLocalClient(t)
	project := initProjectRepo(t, t.TempDir(), "widgets")

	repo, err := EnsureRepoForDir(context.Background(), c, project)
	if err != nil {
		t.Fatalf("EnsureRepoForDir: %v", err)
	}
	if repo == nil {
		t.Fatal("repo = nil, want an enrolled repo")
	}
	if repo.Prefix == "" {
		t.Error("Prefix is empty")
	}
	rows, err := c.store.ListHistory(store.HistoryFilter{})
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	var creates int
	for _, r := range rows {
		if r.Op == "repo.create" {
			creates++
		}
	}
	if creates != 1 {
		t.Errorf("repo.create rows = %d, want 1", creates)
	}
}

// TestEnsureRepoForDir_Idempotent: a second call on the same directory
// returns the same repo and writes no further audit row.
func TestEnsureRepoForDir_Idempotent(t *testing.T) {
	requireGit(t)
	c, _ := openTestLocalClient(t)
	project := initProjectRepo(t, t.TempDir(), "widgets")

	first, err := EnsureRepoForDir(context.Background(), c, project)
	if err != nil {
		t.Fatalf("first EnsureRepoForDir: %v", err)
	}
	second, err := EnsureRepoForDir(context.Background(), c, project)
	if err != nil {
		t.Fatalf("second EnsureRepoForDir: %v", err)
	}
	if second == nil || second.Prefix != first.Prefix {
		t.Fatalf("second prefix = %v, want %q", second, first.Prefix)
	}
	rows, err := c.store.ListHistory(store.HistoryFilter{})
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	var creates int
	for _, r := range rows {
		if r.Op == "repo.create" {
			creates++
		}
	}
	if creates != 1 {
		t.Errorf("repo.create rows = %d, want 1", creates)
	}
}

// TestEnsureRepoForDir_SyncRepo: bacio's own sync checkout is not a
// project — no repo, no error.
func TestEnsureRepoForDir_SyncRepo(t *testing.T) {
	requireGit(t)
	c, _ := openTestLocalClient(t)
	project := initProjectRepo(t, t.TempDir(), "team-sync")
	sentinel := filepath.Join(project, bsync.SentinelFilename)
	if err := os.WriteFile(sentinel, []byte("version: 1\n"), 0o644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	repo, err := EnsureRepoForDir(context.Background(), c, project)
	if err != nil {
		t.Fatalf("EnsureRepoForDir: %v", err)
	}
	if repo != nil {
		t.Errorf("repo = %+v, want nil for a sync repo", repo)
	}
}

// TestEnsureRepoForDir_NotAGitRepo: launched from a bare directory —
// no repo, no error, nothing registered.
func TestEnsureRepoForDir_NotAGitRepo(t *testing.T) {
	c, _ := openTestLocalClient(t)

	repo, err := EnsureRepoForDir(context.Background(), c, t.TempDir())
	if err != nil {
		t.Fatalf("EnsureRepoForDir: %v", err)
	}
	if repo != nil {
		t.Errorf("repo = %+v, want nil outside a git repo", repo)
	}
}

// TestEnsureRepoForDir_EmptyDir: an unresolvable cwd is the same
// ordinary no-op.
func TestEnsureRepoForDir_EmptyDir(t *testing.T) {
	c, _ := openTestLocalClient(t)

	repo, err := EnsureRepoForDir(context.Background(), c, "")
	if err != nil {
		t.Fatalf("EnsureRepoForDir: %v", err)
	}
	if repo != nil {
		t.Errorf("repo = %+v, want nil for an empty dir", repo)
	}
}
