package git

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Repo wraps a local git working tree at Root. Every method shells out
// to `git` via os/exec, matching detect.go's existing pattern. We don't
// pull in go-git for two reasons: (1) sync repos are small, the
// cost of the subprocess is negligible, and (2) `git` is already on
// every developer's PATH and behaves the same as the user's manual
// invocations — handy for debugging.
//
// Errors from each method wrap stderr from git via *Error so callers
// can `errors.As` the wrapped form to surface the original command
// and stderr without re-shelling.
type Repo struct {
	Root string
}

// Error wraps a failed git invocation with the command line and the
// captured stderr. Returned (wrapped) by every method on *Repo when
// the underlying `git` exits non-zero.
type Error struct {
	Command string
	Stderr  string
	Err     error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if strings.TrimSpace(e.Stderr) == "" {
		return fmt.Sprintf("git %s: %v", e.Command, e.Err)
	}
	return fmt.Sprintf("git %s: %v: %s", e.Command, e.Err, strings.TrimSpace(e.Stderr))
}

func (e *Error) Unwrap() error { return e.Err }

// Open returns a *Repo at path if it contains a .git directory or
// file (a worktree pointer). Errors with ErrNotARepo otherwise so
// callers can distinguish "not a repo" from "filesystem error".
func Open(path string) (*Repo, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(filepath.Join(abs, ".git"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotARepo
		}
		return nil, err
	}
	// .git is either a directory (regular clone) or a file (a worktree
	// or submodule pointer). Both are fine.
	_ = info
	return &Repo{Root: abs}, nil
}

// Init runs `git init -b main` at path and returns a *Repo bound to
// it. The directory is created if it doesn't yet exist. We pin the
// initial branch to `main` so sync repos look the same regardless of
// the user's `init.defaultBranch` (Linux/older git defaults to
// `master`, which mismatches the bare remote we push to and produces
// an empty checkout for the next cloner).
func Init(path string) (*Repo, error) {
	if err := os.MkdirAll(path, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", path, err)
	}
	if _, err := runGit(path, "init", "-b", "main"); err != nil {
		return nil, err
	}
	return Open(path)
}

// Clone runs `git clone <remote> <dest>`. The destination is created
// by git itself; it must not already exist.
func Clone(remote, dest string) error {
	parent := filepath.Dir(dest)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("mkdir parent %s: %w", parent, err)
	}
	_, err := runGit(parent, "clone", remote, dest)
	return err
}

// AddRemote runs `git remote add <name> <url>`.
func (r *Repo) AddRemote(name, url string) error {
	_, err := runGit(r.Root, "remote", "add", name, url)
	return err
}

// Pull runs a fast-forward-only `git pull --ff-only`. We use ff-only
// rather than allowing merge so a sync race that produces a true
// conflict surfaces immediately rather than silently merging in the
// background.
func (r *Repo) Pull() error {
	_, err := runGit(r.Root, "pull", "--ff-only")
	return err
}

// Add runs `git add` on the supplied paths. With no paths, equivalent
// to `git add -A`.
func (r *Repo) Add(paths ...string) error {
	args := []string{"add"}
	if len(paths) == 0 {
		args = append(args, "-A")
	} else {
		args = append(args, paths...)
	}
	_, err := runGit(r.Root, args...)
	return err
}

// Mv runs `git mv <from> <to>`. The caller is responsible for any
// parent-directory creation on the new side; git mv will fail if the
// parent doesn't exist.
func (r *Repo) Mv(from, to string) error {
	// `git mv` requires the destination's parent to exist, but the
	// staging-aware caller may have already created the destination
	// folder. mkdir -p is harmless either way.
	dst := filepath.Join(r.Root, to)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("mkdir parent for git mv: %w", err)
	}
	_, err := runGit(r.Root, "mv", from, to)
	return err
}

// Commit runs `git commit -m <message>` and returns the resulting
// SHA. If the index has no staged changes, returns ("", nil) so the
// caller can distinguish "nothing to commit" from a hard error.
func (r *Repo) Commit(message string) (string, error) {
	clean, err := r.indexClean()
	if err != nil {
		return "", err
	}
	if clean {
		return "", nil
	}
	if _, err := runGit(r.Root, "commit", "-m", message); err != nil {
		return "", err
	}
	out, err := runGit(r.Root, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// indexClean reports whether `git diff --cached --quiet` succeeds
// (exit 0 → clean). Any other exit is treated as "dirty"; we don't
// distinguish "real error" from "dirty" because a true error in this
// path means git itself is broken, and the subsequent Commit call
// will surface the underlying problem.
func (r *Repo) indexClean() (bool, error) {
	cmd := exec.Command("git", "diff", "--cached", "--quiet")
	cmd.Dir = r.Root
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			// exit code 1 = dirty; >1 = error. Be permissive: any
			// exit means "go ahead and try to commit; if it really
			// is broken the commit will fail loudly".
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// Push runs `git push`.
func (r *Repo) Push() error {
	_, err := runGit(r.Root, "push")
	return err
}

// PushSetUpstream runs `git push --set-upstream <remote> <branch>`.
// Used on first push from `bacio sync init` so subsequent `git push`
// invocations have a tracking branch.
func (r *Repo) PushSetUpstream(remote, branch string) error {
	_, err := runGit(r.Root, "push", "--set-upstream", remote, branch)
	return err
}

// HasUncommittedChanges reports whether the working tree or index
// has any uncommitted changes. `git status --porcelain` produces no
// output on a clean tree.
func (r *Repo) HasUncommittedChanges() (bool, error) {
	out, err := runGit(r.Root, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

// HasUnpulledChanges reports whether the upstream branch has commits
// not yet present locally. Best-effort — if the repo has no upstream
// configured (e.g., immediately after `git init` and before the first
// push), returns false with no error.
func (r *Repo) HasUnpulledChanges() (bool, error) {
	if _, err := runGit(r.Root, "rev-parse", "--abbrev-ref", "@{upstream}"); err != nil {
		// No upstream → nothing to be behind on.
		return false, nil
	}
	if _, err := runGit(r.Root, "fetch"); err != nil {
		return false, err
	}
	out, err := runGit(r.Root, "rev-list", "HEAD..@{upstream}", "--count")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "0", nil
}

// IsEmpty reports whether the repository has no commits at all.
// Detected by `git rev-parse HEAD` failing — the canonical "no
// commits yet" signal in git.
func (r *Repo) IsEmpty() (bool, error) {
	if _, err := runGit(r.Root, "rev-parse", "--verify", "HEAD"); err != nil {
		// Distinguish "no commits" from a real error by also
		// checking that the .git directory is intact.
		if _, statErr := os.Stat(filepath.Join(r.Root, ".git")); statErr != nil {
			return false, statErr
		}
		return true, nil
	}
	return false, nil
}

// RemoteHasContent reports whether the named remote currently has
// any branch heads. Used by `bacio sync init` to refuse to bootstrap
// against a populated remote.
func (r *Repo) RemoteHasContent(remote string) (bool, error) {
	out, err := runGit(r.Root, "ls-remote", "--heads", remote)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

// CurrentBranch returns the name of the currently checked-out branch
// (e.g. "main", "master", or whatever the user's `init.defaultBranch`
// resolved to). Used so we don't hardcode "main" when push --set-upstream
// runs after `git init`.
func (r *Repo) CurrentBranch() (string, error) {
	out, err := runGit(r.Root, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// WriteGitattributes writes the supplied content to .gitattributes at
// the repo root. Used by `bacio sync init` to pin LF line endings (`* -text`)
// so git's autocrlf doesn't rewrite the canonical YAML on Windows
// checkouts.
func (r *Repo) WriteGitattributes(content string) error {
	abs := filepath.Join(r.Root, ".gitattributes")
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return os.WriteFile(abs, []byte(content), 0o644)
}

// runGit shells out to git with the given args, returning stdout (as
// a string) on success or an *Error wrapping stderr on failure.
func runGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", &Error{
			Command: strings.Join(args, " "),
			Stderr:  stderr.String(),
			Err:     err,
		}
	}
	return string(out), nil
}
