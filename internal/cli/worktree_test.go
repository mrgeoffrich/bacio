package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/wtenv"
)

// initRealGitRepo seeds a real git working tree at t.TempDir() and
// returns its absolute (symlink-resolved) path. Tests need a real
// `.git/` so git.Detect resolves correctly.
func initRealGitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not on PATH: %v", err)
	}
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

// TestWorktreeInit_RoundTrip drives runWorktreeInit + commitWorktreeInit
// + ensureWorktreeManifestGitignored end-to-end. Verifies the manifest
// is written, the registry row lands, and .gitignore picks up the
// filename exactly once on a repeat call.
func TestWorktreeInit_RoundTrip(t *testing.T) {
	root := initRealGitRepo(t)
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	res, err := runWorktreeInit(root, filepath.Base(root), inputs.WorktreeInitInput{Slug: "demo-wt"})
	if err != nil {
		t.Fatalf("runWorktreeInit: %v", err)
	}
	if res.Manifest.Identity.Slug != "demo-wt" {
		t.Fatalf("slug: got %q", res.Manifest.Identity.Slug)
	}
	if res.Manifest.Allocations.APIPort == wtenv.DefaultAPIPort || res.Manifest.Allocations.APIPort == 0 {
		t.Fatalf("port: got %d", res.Manifest.Allocations.APIPort)
	}
	if res.Manifest.Allocations.DBPath != filepath.Join(".bacio", "db.sqlite") {
		t.Fatalf("db_path: got %q", res.Manifest.Allocations.DBPath)
	}

	if err := commitWorktreeInit(res); err != nil {
		t.Fatalf("commitWorktreeInit: %v", err)
	}
	// Manifest exists.
	if _, err := os.Stat(res.ManifestPath); err != nil {
		t.Fatalf("manifest missing: %v", err)
	}
	// .gitignore got the line on first run.
	if !res.GitignoreAdded {
		t.Errorf("gitignore_added: want true on first run")
	}

	// Re-running with --force is allowed; idempotent.
	res2, err := runWorktreeInit(root, filepath.Base(root), inputs.WorktreeInitInput{Slug: "demo-wt", Force: true})
	if err != nil {
		t.Fatalf("re-run: %v", err)
	}
	if err := commitWorktreeInit(res2); err != nil {
		t.Fatalf("commit re-run: %v", err)
	}
	if res2.GitignoreAdded {
		t.Errorf("gitignore_added: want false on idempotent second run")
	}

	// Registry has the entry.
	reg, err := wtenv.ReadRegistry(tmpHome)
	if err != nil {
		t.Fatalf("read registry: %v", err)
	}
	entry, ok := reg.FindBySlug("demo-wt")
	if !ok {
		t.Fatalf("registry missing slug: %+v", reg.Worktrees)
	}
	if entry.Path != root {
		t.Errorf("registry path: got %q want %q", entry.Path, root)
	}
	if entry.APIPort != res.Manifest.Allocations.APIPort {
		t.Errorf("registry port: got %d want %d", entry.APIPort, res.Manifest.Allocations.APIPort)
	}

	// .gitignore line appears exactly once.
	gi, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	if strings.Count(string(gi), wtenv.DefaultManifestFilename) != 1 {
		t.Errorf(".gitignore: line count: %q", string(gi))
	}
}

// TestWorktreeInit_RefusesWithoutForce checks that re-running init
// against an existing manifest fails unless --force is set.
func TestWorktreeInit_RefusesWithoutForce(t *testing.T) {
	root := initRealGitRepo(t)
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	if _, err := runWorktreeInit(root, filepath.Base(root), inputs.WorktreeInitInput{Slug: "demo"}); err != nil {
		t.Fatalf("first run: %v", err)
	}
	res, _ := runWorktreeInit(root, filepath.Base(root), inputs.WorktreeInitInput{Slug: "demo"})
	if res != nil {
		// runWorktreeInit only errors when the manifest exists on disk; need to commit first.
		_ = commitWorktreeInit(res)
	}
	if _, err := runWorktreeInit(root, filepath.Base(root), inputs.WorktreeInitInput{Slug: "demo"}); err == nil {
		t.Fatalf("second run: want error without --force")
	}
}

// TestWorktreeInit_RejectsReservedPort ensures the legacy default
// port can't be allocated to a worktree manifest (otherwise it would
// clash with the legacy default bacio instance).
func TestWorktreeInit_RejectsReservedPort(t *testing.T) {
	root := initRealGitRepo(t)
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	_, err := runWorktreeInit(root, filepath.Base(root), inputs.WorktreeInitInput{Slug: "demo", Port: wtenv.DefaultAPIPort})
	if err == nil {
		t.Fatal("want error on reserved port")
	}
	if !strings.Contains(err.Error(), "reserved") {
		t.Errorf("err: %v", err)
	}
}

// TestSanitiseDefaultSlug exercises the helper that derives the
// default manifest slug from a directory basename.
func TestSanitiseDefaultSlug(t *testing.T) {
	cases := map[string]string{
		"bacio":             "bacio",
		"bacio-BACI-63":     "bacio-baci-63",
		"My Repo":           "my-repo",
		"feature/foo":       "feature-foo",
		"  trim  ":          "trim",
		"...":               "worktree",
		"":                  "worktree",
	}
	for in, want := range cases {
		got := sanitiseDefaultSlug(in)
		if got != want {
			t.Errorf("sanitiseDefaultSlug(%q): got %q, want %q", in, got, want)
		}
	}
}
