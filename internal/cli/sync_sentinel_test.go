package cli

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/store"
	"github.com/mrgeoffrich/bacio/internal/sync"
)

// TestResolveRepo_BailsInSyncRepo confirms the Phase-4 sentinel
// detection: running a tracking command from inside a sync repo
// returns errSyncRepoMode rather than auto-registering the sync repo
// as a project. This is what protects users from silently turning
// their sync data into a tracked project.
func TestResolveRepo_BailsInSyncRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not on PATH: %v", err)
	}
	dir := t.TempDir()
	syncRepo := filepath.Join(dir, "sync")
	if err := exec.Command("git", "init", "-b", "main", syncRepo).Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}
	// Drop in the sentinel.
	if err := sync.WriteSentinel(syncRepo, sync.Sentinel{SchemaVersion: 1}); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	// Switch CWD into the sync repo for the duration of resolveRepo.
	saved, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(saved) })
	if err := os.Chdir(syncRepo); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	_, err = resolveRepo(s)
	if err == nil {
		t.Fatal("expected error in sync repo, got nil")
	}
	if !errors.Is(err, errSyncRepoMode) {
		t.Fatalf("error %v is not errSyncRepoMode", err)
	}
}
