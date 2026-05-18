package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestDetect_WorktreeResolvesToMainRoot is the regression for the bug
// where running `bacio` inside a linked worktree auto-registered a fresh
// repo because Detect returned the worktree's own toplevel instead of
// the main worktree's.
func TestDetect_WorktreeResolvesToMainRoot(t *testing.T) {
	requireGit(t)
	withGitIdentity(t)

	tmp := t.TempDir()
	main := filepath.Join(tmp, "repo")
	wt := filepath.Join(tmp, "wt-feature")

	mustGit(t, tmp, "init", "-q", "repo")
	mustGit(t, main, "commit", "--allow-empty", "-q", "-m", "init")
	mustGit(t, main, "worktree", "add", "-q", wt, "-b", "feature")

	mainInfo, err := Detect(main)
	if err != nil {
		t.Fatalf("Detect(main): %v", err)
	}
	wtInfo, err := Detect(wt)
	if err != nil {
		t.Fatalf("Detect(worktree): %v", err)
	}

	mainRoot, _ := filepath.EvalSymlinks(mainInfo.Root)
	wtRoot, _ := filepath.EvalSymlinks(wtInfo.Root)
	if mainRoot != wtRoot {
		t.Fatalf("worktree resolved to %q, expected main %q", wtRoot, mainRoot)
	}

	// Also exercise a subdirectory inside the worktree — Detect should
	// still walk up to the main root, not the worktree root or the
	// subdirectory.
	sub := filepath.Join(wt, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	subInfo, err := Detect(sub)
	if err != nil {
		t.Fatalf("Detect(sub): %v", err)
	}
	subRoot, _ := filepath.EvalSymlinks(subInfo.Root)
	if subRoot != mainRoot {
		t.Fatalf("sub resolved to %q, expected main %q", subRoot, mainRoot)
	}
}

// TestWorktreeRoot_LinkedWorktree is the BACI-71 regression: the
// linked-worktree probe must return the linked worktree's own
// toplevel, NOT the main worktree's. (Detect deliberately returns
// the main worktree for repo-identity reasons; WorktreeRoot is the
// inverse helper for the manifest layer.)
func TestWorktreeRoot_LinkedWorktree(t *testing.T) {
	requireGit(t)
	withGitIdentity(t)

	tmp := t.TempDir()
	main := filepath.Join(tmp, "repo")
	wt := filepath.Join(tmp, "wt-feature")

	mustGit(t, tmp, "init", "-q", "repo")
	mustGit(t, main, "commit", "--allow-empty", "-q", "-m", "init")
	mustGit(t, main, "worktree", "add", "-q", wt, "-b", "feature")

	mainRoot, err := WorktreeRoot(main)
	if err != nil {
		t.Fatalf("WorktreeRoot(main): %v", err)
	}
	wtRoot, err := WorktreeRoot(wt)
	if err != nil {
		t.Fatalf("WorktreeRoot(worktree): %v", err)
	}

	mainEval, _ := filepath.EvalSymlinks(mainRoot)
	wtEval, _ := filepath.EvalSymlinks(wtRoot)
	mainWant, _ := filepath.EvalSymlinks(main)
	wtWant, _ := filepath.EvalSymlinks(wt)

	if mainEval != mainWant {
		t.Errorf("WorktreeRoot(main): got %q, want %q", mainEval, mainWant)
	}
	if wtEval != wtWant {
		t.Errorf("WorktreeRoot(worktree): got %q, want %q (NOT the main root %q)", wtEval, wtWant, mainWant)
	}
	if wtEval == mainEval {
		t.Fatalf("WorktreeRoot(worktree) == WorktreeRoot(main); linked worktree resolved to main — this is exactly the BACI-71 bug")
	}

	// A subdirectory inside the linked worktree must still walk up
	// to the linked worktree's own root (not the main one, not the
	// subdir itself).
	sub := filepath.Join(wt, "sub", "deeper")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	subRoot, err := WorktreeRoot(sub)
	if err != nil {
		t.Fatalf("WorktreeRoot(sub): %v", err)
	}
	subEval, _ := filepath.EvalSymlinks(subRoot)
	if subEval != wtWant {
		t.Errorf("WorktreeRoot(sub): got %q, want %q (the linked worktree's root)", subEval, wtWant)
	}
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

