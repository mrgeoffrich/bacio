package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// requireGit short-circuits a test that needs the `git` binary.
// CI without it (rare) just skips.
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not on PATH: %v", err)
	}
}

// withGitIdentity sets author/committer via env so commits succeed
// without inheriting the host's global git config.
func withGitIdentity(t *testing.T) {
	t.Setenv("GIT_AUTHOR_NAME", "tester")
	t.Setenv("GIT_AUTHOR_EMAIL", "tester@example.invalid")
	t.Setenv("GIT_COMMITTER_NAME", "tester")
	t.Setenv("GIT_COMMITTER_EMAIL", "tester@example.invalid")
}

// TestInit_OpenRoundTrip exercises the most basic shape: Init creates
// a working tree, Open finds the .git, returned Repo has the right
// Root.
func TestInit_OpenRoundTrip(t *testing.T) {
	requireGit(t)
	dir := filepath.Join(t.TempDir(), "repo")
	r, err := Init(dir)
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if r.Root != dir {
		t.Fatalf("root = %q, want %q", r.Root, dir)
	}
	r2, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if r2.Root != r.Root {
		t.Fatalf("re-open root = %q, want %q", r2.Root, r.Root)
	}
}

// TestCommit_NoChangesReturnsEmpty: indexClean detects a clean tree
// and Commit returns ("", nil) so callers can branch on "nothing to
// commit" without parsing stderr.
func TestCommit_NoChangesReturnsEmpty(t *testing.T) {
	requireGit(t)
	withGitIdentity(t)
	dir := filepath.Join(t.TempDir(), "repo")
	r, err := Init(dir)
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	sha, err := r.Commit("nothing")
	if err != nil {
		t.Fatalf("commit on empty: %v", err)
	}
	if sha != "" {
		t.Fatalf("expected empty sha, got %q", sha)
	}
}

// TestCommit_WithStaged makes a real commit, returning the sha.
func TestCommit_WithStaged(t *testing.T) {
	requireGit(t)
	withGitIdentity(t)
	dir := filepath.Join(t.TempDir(), "repo")
	r, err := Init(dir)
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := r.Add("hello.txt"); err != nil {
		t.Fatalf("add: %v", err)
	}
	sha, err := r.Commit("first")
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if sha == "" {
		t.Fatal("expected sha, got empty")
	}
}

// TestHasUncommittedChanges flips between clean and dirty as
// content changes.
func TestHasUncommittedChanges(t *testing.T) {
	requireGit(t)
	withGitIdentity(t)
	dir := filepath.Join(t.TempDir(), "repo")
	r, err := Init(dir)
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	dirty, err := r.HasUncommittedChanges()
	if err != nil {
		t.Fatalf("status on empty: %v", err)
	}
	if dirty {
		t.Fatal("empty repo reported dirty")
	}
	if err := os.WriteFile(filepath.Join(dir, "x.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	dirty, err = r.HasUncommittedChanges()
	if err != nil {
		t.Fatalf("status after write: %v", err)
	}
	if !dirty {
		t.Fatal("untracked file should mark working tree dirty")
	}
}

// TestRemoteHasContent_EmptyBare confirms the empty-remote check
// returns false against a fresh bare repo. This is what `bacio sync
// init` uses to gate the "remote already has content" refusal.
func TestRemoteHasContent_EmptyBare(t *testing.T) {
	requireGit(t)
	tdir := t.TempDir()
	bare := filepath.Join(tdir, "remote.git")
	if err := exec.Command("git", "init", "--bare", "-b", "main", bare).Run(); err != nil {
		t.Fatalf("init bare: %v", err)
	}
	working := filepath.Join(tdir, "work")
	r, err := Init(working)
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := r.AddRemote("origin", bare); err != nil {
		t.Fatalf("add remote: %v", err)
	}
	has, err := r.RemoteHasContent("origin")
	if err != nil {
		t.Fatalf("ls-remote: %v", err)
	}
	if has {
		t.Fatal("empty bare reported has content")
	}
}
