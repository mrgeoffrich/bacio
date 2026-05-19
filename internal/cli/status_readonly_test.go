package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/git"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// TestStatus_ReadOnlyInUnregisteredGitTree confirms BACI-6: bacio status
// inside a git working tree with no matching repos row must NOT insert
// a row. The report carries InRepo=true, Registered=false, and Path.
func TestStatus_ReadOnlyInUnregisteredGitTree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not on PATH: %v", err)
	}
	dir := initGitRepo(t)
	withCwd(t, dir)
	s := openMemStore(t)

	info, err := git.Detect(dir)
	if err != nil {
		t.Fatalf("git.Detect: %v", err)
	}

	report, err := buildStatusReport(s, testEnv("/db.sqlite"), testLog(), nil, dir)
	if err != nil {
		t.Fatalf("buildStatusReport: %v", err)
	}
	if !report.InRepo {
		t.Fatalf("InRepo=false, want true")
	}
	if report.Registered {
		t.Fatalf("Registered=true, want false")
	}
	if report.Repo != nil {
		t.Fatalf("Repo set unexpectedly: %+v", report.Repo)
	}
	if report.Path != info.Root {
		t.Fatalf("Path=%q, want %q", report.Path, info.Root)
	}

	// Regression guard: no row inserted.
	repos, err := s.ListRepos()
	if err != nil {
		t.Fatalf("ListRepos: %v", err)
	}
	if len(repos) != 0 {
		t.Fatalf("status auto-registered %d repo(s); expected zero", len(repos))
	}
}

// TestStatus_RegisteredRepoReportsRegistered seeds a repos row for the
// current working tree and verifies the registered branch populates
// Repo + stats and reports Registered=true.
func TestStatus_RegisteredRepoReportsRegistered(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not on PATH: %v", err)
	}
	dir := initGitRepo(t)
	withCwd(t, dir)
	s := openMemStore(t)

	info, err := git.Detect(dir)
	if err != nil {
		t.Fatalf("git.Detect: %v", err)
	}
	prefix, err := s.AllocatePrefix(info.Name)
	if err != nil {
		t.Fatalf("AllocatePrefix: %v", err)
	}
	created, err := s.CreateRepo(prefix, info.Name, info.Root, info.RemoteURL)
	if err != nil {
		t.Fatalf("CreateRepo: %v", err)
	}

	report, err := buildStatusReport(s, testEnv("/db.sqlite"), testLog(), nil, dir)
	if err != nil {
		t.Fatalf("buildStatusReport: %v", err)
	}
	if !report.InRepo || !report.Registered {
		t.Fatalf("InRepo=%v Registered=%v, want both true", report.InRepo, report.Registered)
	}
	if report.Repo == nil || report.Repo.ID != created.ID {
		t.Fatalf("Repo mismatch: got %+v want id=%d", report.Repo, created.ID)
	}
	if report.Stats.NextIssueKey == "" {
		t.Fatalf("NextIssueKey empty; fillRepoStats not invoked")
	}
}

// TestStatus_OutsideGitRepoUsesGlobalStats confirms the no-git-tree
// branch is unchanged: InRepo=false, global stats populated.
func TestStatus_OutsideGitRepoUsesGlobalStats(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not on PATH: %v", err)
	}
	dir := t.TempDir() // no git init
	withCwd(t, dir)
	s := openMemStore(t)

	report, err := buildStatusReport(s, testEnv("/db.sqlite"), testLog(), nil, dir)
	if err != nil {
		t.Fatalf("buildStatusReport: %v", err)
	}
	if report.InRepo {
		t.Fatalf("InRepo=true, want false")
	}
	if report.Registered {
		t.Fatalf("Registered=true, want false")
	}
	if report.Stats.TrackedRepos != 0 {
		t.Fatalf("TrackedRepos=%d, want 0", report.Stats.TrackedRepos)
	}
}

func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := exec.Command("git", "init", "-b", "main", dir).Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("eval symlinks: %v", err)
	}
	return resolved
}

func withCwd(t *testing.T, dir string) {
	t.Helper()
	saved, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(saved) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
}

func openMemStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}
