package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/store"
)

// seedLinkFixture builds the on-disk fixture every repo-link CLI test
// needs: a phantom TEST row, a sync_remotes entry whose local clone
// carries repos/TEST/, and an existing git working tree to bind to.
// Returns the (project-path, sync-remote-url) tuple the test asserts
// against. Skips the test if git isn't available.
func seedLinkFixture(t *testing.T, s *store.Store) (string, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	dir := t.TempDir()
	if _, err := s.CreatePhantomRepo("test-uuid-aaaa", "TEST", "test-project", "git@example.com:dev/test.git"); err != nil {
		t.Fatalf("CreatePhantomRepo: %v", err)
	}
	syncLocalPath := filepath.Join(dir, "sync-clone")
	if err := os.MkdirAll(filepath.Join(syncLocalPath, "repos", "TEST"), 0o755); err != nil {
		t.Fatalf("mkdir sync clone: %v", err)
	}
	const remote = "git@example.com:dev/team-sync.git"
	if err := s.UpsertSyncRemote(remote, syncLocalPath); err != nil {
		t.Fatalf("UpsertSyncRemote: %v", err)
	}
	projectPath := filepath.Join(dir, "project")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	cmd := exec.Command("git", "init", "--quiet", projectPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	return projectPath, remote
}

// runRepoLink executes the `bacio repo link` cobra command against the
// process-global opts.dbPath (set by withTestStore) and captures
// stdout — emit() writes there directly. argv is the literal flag /
// positional list (no leading "link").
func runRepoLink(t *testing.T, argv ...string) ([]byte, error) {
	t.Helper()
	r, w, perr := os.Pipe()
	if perr != nil {
		t.Fatalf("pipe: %v", perr)
	}
	prev := os.Stdout
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = prev })

	cmd := repoLinkCmd()
	cmd.SetArgs(argv)
	err := cmd.Execute()
	_ = w.Close()

	var buf bytes.Buffer
	if _, rerr := buf.ReadFrom(r); rerr != nil {
		t.Fatalf("read stdout: %v", rerr)
	}
	return buf.Bytes(), err
}

func TestRepoLinkPositionalHappyPath(t *testing.T) {
	s := withTestStore(t)
	projectPath, _ := seedLinkFixture(t, s)

	if _, err := runRepoLink(t, "TEST", projectPath); err != nil {
		t.Fatalf("repo link TEST <path>: %v", err)
	}
	repo, err := s.GetRepoByPrefix("TEST")
	if err != nil {
		t.Fatalf("GetRepoByPrefix: %v", err)
	}
	if repo.Path != projectPath {
		t.Fatalf("repo.Path = %q; want %q", repo.Path, projectPath)
	}
}

func TestRepoLinkJSONHappyPath(t *testing.T) {
	s := withTestStore(t)
	projectPath, _ := seedLinkFixture(t, s)

	payload, err := json.Marshal(map[string]string{
		"prefix": "TEST",
		"path":   projectPath,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := runRepoLink(t, "--json", string(payload)); err != nil {
		t.Fatalf("repo link --json: %v", err)
	}
	repo, err := s.GetRepoByPrefix("TEST")
	if err != nil {
		t.Fatalf("GetRepoByPrefix: %v", err)
	}
	if repo.Path != projectPath {
		t.Fatalf("repo.Path = %q; want %q", repo.Path, projectPath)
	}
}

func TestRepoLinkDryRun(t *testing.T) {
	s := withTestStore(t)
	projectPath, remote := seedLinkFixture(t, s)

	// Set the global dry-run flag the verb checks (opts.dryRun) for
	// the duration of this test.
	prevDry := opts.dryRun
	opts.dryRun = true
	t.Cleanup(func() { opts.dryRun = prevDry })

	out, err := runRepoLink(t, "TEST", projectPath)
	if err != nil {
		t.Fatalf("repo link --dry-run: %v", err)
	}
	// The store row stays phantom.
	repo, err := s.GetRepoByPrefix("TEST")
	if err != nil {
		t.Fatalf("GetRepoByPrefix: %v", err)
	}
	if repo.Path != "" {
		t.Fatalf("dry-run mutated path = %q; want empty", repo.Path)
	}
	// Output carries the projected remote URL.
	if !bytes.Contains(out, []byte(remote)) {
		t.Fatalf("dry-run output missing sync remote %q: %s", remote, out)
	}
}
