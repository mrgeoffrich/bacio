package git

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
)

var ErrNotARepo = errors.New("not inside a git repository")

type Info struct {
	Root      string // absolute path
	Name      string // basename of root
	RemoteURL string // empty if no origin configured
}

// Detect finds the git repo containing dir (or any of its parents).
// For a linked worktree it returns the *main* worktree's root, so that
// every worktree of a repo resolves to the same Info — without this,
// resolveRepo auto-registers a fresh repo per worktree.
func Detect(dir string) (*Info, error) {
	root, err := mainWorktreeRoot(dir)
	if err != nil {
		return nil, ErrNotARepo
	}
	info := &Info{Root: root, Name: filepath.Base(root)}
	if remote, err := run(dir, "remote", "get-url", "origin"); err == nil {
		info.RemoteURL = remote
	}
	return info, nil
}

// WorktreeRoot returns the absolute path to the linked worktree
// containing dir — i.e. the toplevel of *that* working tree, not the
// main worktree's root. Use this for things that need per-worktree
// isolation on the filesystem (bacio's environment-config.yaml is the
// canonical example).
//
// This is deliberately NOT what Detect returns. Detect walks back to
// the main worktree's root so resolveRepo / install-agent / sync /
// status etc. share one repo identity across every linked worktree of
// a project. WorktreeRoot is the inverse — the file lives at *this*
// worktree's root and only this worktree's bacio should read it.
//
// Built on `git rev-parse --show-toplevel`, which DOES return the
// linked worktree's own root (in contrast to --git-common-dir).
func WorktreeRoot(dir string) (string, error) {
	root, err := run(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", ErrNotARepo
	}
	return filepath.Abs(root)
}

// LinkedWorktreeRoot reports the linked-worktree root containing dir,
// distinguishing it from the primary worktree of the same repo. It
// returns one of three results:
//
//   - root != "", linked == true   — dir is inside a *linked* worktree,
//     root is that worktree's toplevel.
//   - root != "", linked == false  — dir is inside the *primary*
//     worktree (the main checkout), root is its toplevel.
//   - root == "", err != nil       — dir is not inside any git repo (or
//     git is unusable).
//
// The classification is via `git rev-parse --git-dir` vs
// `--git-common-dir`: equal paths ⇒ primary, distinct paths ⇒ linked.
// This is what the BACI-129 PreToolUse guard needs: a fast, manifest-
// free way to tell "main checkout that must be denied" from "linked
// worktree that is allowed". Errors collapse to the no-repo case so
// the hook's fail-open invariant has a single error path to handle.
func LinkedWorktreeRoot(dir string) (string, bool, error) {
	gitDir, err := run(dir, "rev-parse", "--git-dir")
	if err != nil {
		return "", false, ErrNotARepo
	}
	commonDir, err := run(dir, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", false, ErrNotARepo
	}
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(dir, gitDir)
	}
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(dir, commonDir)
	}
	gitDir = filepath.Clean(gitDir)
	commonDir = filepath.Clean(commonDir)

	// Primary worktree iff --git-dir == --git-common-dir.
	linked := gitDir != commonDir

	// --show-toplevel returns the *current* worktree's toplevel —
	// the linked worktree's own root in a linked-worktree cwd, the
	// main worktree's root in the primary case.
	top, err := run(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", false, ErrNotARepo
	}
	root, err := filepath.Abs(top)
	if err != nil {
		return "", false, err
	}
	return root, linked, nil
}

// mainWorktreeRoot returns the absolute path to the main worktree of
// the repo containing dir. It uses --git-common-dir (the shared .git
// directory across all worktrees) and takes its parent: for the main
// worktree this is the toplevel; for a linked worktree it skips past
// the linked worktree's own root to land on the main one. Falls back
// to --show-toplevel for layouts where the common dir isn't named
// `.git` (e.g. bare repos), which is the best we can do.
func mainWorktreeRoot(dir string) (string, error) {
	common, err := run(dir, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(dir, common)
	}
	common = filepath.Clean(common)
	if filepath.Base(common) == ".git" {
		return filepath.Dir(common), nil
	}
	root, err := run(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	return filepath.Abs(root)
}

func run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
