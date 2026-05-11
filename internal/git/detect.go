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
