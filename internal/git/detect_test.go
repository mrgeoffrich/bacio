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

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

