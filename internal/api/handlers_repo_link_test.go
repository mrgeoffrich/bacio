package api_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/api"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// linkAPITestEnv bundles the temp-on-disk + sync-remote + git
// working-tree fixtures needed to drive POST /repos/{prefix}/link
// against the live router. Each test re-builds the fixture from
// scratch — newTestAPI gives us a fresh DB so the audit-log
// assertions stay simple.
type linkAPITestEnv struct {
	dir         string
	projectPath string
	syncRemote  string
}

func newLinkAPITestEnv(t *testing.T, s *store.Store) *linkAPITestEnv {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	dir := t.TempDir()
	// Phantom row at TEST.
	if _, err := s.CreatePhantomRepo("test-uuid-aaaa", "TEST", "test-project", "git@example.com:dev/test.git"); err != nil {
		t.Fatalf("CreatePhantomRepo: %v", err)
	}
	// Sync clone on disk + sync_remotes registry row.
	syncLocalPath := filepath.Join(dir, "sync-clone")
	if err := os.MkdirAll(filepath.Join(syncLocalPath, "repos", "TEST"), 0o755); err != nil {
		t.Fatalf("mkdir sync clone: %v", err)
	}
	const remote = "git@example.com:dev/team-sync.git"
	if err := s.UpsertSyncRemote(remote, syncLocalPath); err != nil {
		t.Fatalf("UpsertSyncRemote: %v", err)
	}
	// Real working tree for the link target.
	projectPath := filepath.Join(dir, "project")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	cmd := exec.Command("git", "init", "--quiet", projectPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	return &linkAPITestEnv{dir: dir, projectPath: projectPath, syncRemote: remote}
}

func TestHandleRepoLink_OK(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	env := newLinkAPITestEnv(t, s)

	resp, body := apiPost(t, ts.URL+"/repos/TEST/link",
		map[string]any{"path": env.projectPath})
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body=%s", resp.StatusCode, body)
	}
	var result struct {
		Repo struct {
			Prefix string `json:"prefix"`
			Path   string `json:"path"`
		} `json:"repo"`
		SyncRemoteURL string `json:"sync_remote_url"`
		AlreadyLinked bool   `json:"already_linked"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("decode: %v body=%s", err, body)
	}
	if result.Repo.Path != env.projectPath {
		t.Fatalf("repo.path = %q; want %q", result.Repo.Path, env.projectPath)
	}
	if result.SyncRemoteURL != env.syncRemote {
		t.Fatalf("sync_remote_url = %q; want %q", result.SyncRemoteURL, env.syncRemote)
	}
	if result.AlreadyLinked {
		t.Fatalf("already_linked = true on first link; want false")
	}
	assertHistoryOps(t, s, []string{"repo.upgrade_phantom"})
}

func TestHandleRepoLink_DryRun(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	env := newLinkAPITestEnv(t, s)
	resp, body := apiPost(t, ts.URL+"/repos/TEST/link?dry_run=true",
		map[string]any{"path": env.projectPath})
	if resp.StatusCode != 200 || resp.Header.Get("X-Dry-Run") != "applied" {
		t.Fatalf("status: %d, header=%q body=%s", resp.StatusCode, resp.Header.Get("X-Dry-Run"), body)
	}
	if !strings.Contains(string(body), `"sync_remote_url"`) {
		t.Fatalf("dry-run body missing sync_remote_url: %s", body)
	}
	// Store unchanged.
	repo, err := s.GetRepoByPrefix("TEST")
	if err != nil {
		t.Fatalf("GetRepoByPrefix: %v", err)
	}
	if repo.Path != "" {
		t.Fatalf("dry-run mutated path = %q; want empty", repo.Path)
	}
	assertHistoryOps(t, s, nil)
}

func TestHandleRepoLink_NotFound(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp, _ := apiPost(t, ts.URL+"/repos/NONE/link", map[string]any{"path": "/tmp/x"})
	if resp.StatusCode != 404 {
		t.Fatalf("status: %d; want 404", resp.StatusCode)
	}
}

func TestHandleRepoLink_NotPhantom(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	env := newLinkAPITestEnv(t, s)
	// Seed a real (non-phantom) repo at OTHR.
	if _, err := s.CreateRepo("OTHR", "other", filepath.Join(env.dir, "other"), ""); err != nil {
		t.Fatalf("CreateRepo: %v", err)
	}
	resp, body := apiPost(t, ts.URL+"/repos/OTHR/link",
		map[string]any{"path": env.projectPath})
	if resp.StatusCode != 409 {
		t.Fatalf("status: %d body=%s; want 409", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"kind":"not_phantom"`) {
		t.Fatalf("body missing kind=not_phantom: %s", body)
	}
}

func TestHandleRepoLink_PathAlreadyBound(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	env := newLinkAPITestEnv(t, s)
	// Bind another repo to the projectPath first; the phantom can't take it.
	if _, err := s.CreateRepo("OTHR", "other", env.projectPath, ""); err != nil {
		t.Fatalf("CreateRepo: %v", err)
	}
	resp, body := apiPost(t, ts.URL+"/repos/TEST/link",
		map[string]any{"path": env.projectPath})
	if resp.StatusCode != 409 {
		t.Fatalf("status: %d body=%s; want 409", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"kind":"path_already_bound"`) {
		t.Fatalf("body missing kind=path_already_bound: %s", body)
	}
	if !strings.Contains(string(body), `"existing_prefix":"OTHR"`) {
		t.Fatalf("body missing existing_prefix=OTHR: %s", body)
	}
}

func TestHandleRepoLink_NoOwningSyncRepo(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	env := newLinkAPITestEnv(t, s)
	// Remove the repos/TEST/ folder so discovery fails.
	if err := os.RemoveAll(filepath.Join(env.dir, "sync-clone", "repos", "TEST")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	resp, body := apiPost(t, ts.URL+"/repos/TEST/link",
		map[string]any{"path": env.projectPath})
	if resp.StatusCode != 409 {
		t.Fatalf("status: %d body=%s; want 409", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"kind":"no_owning_sync_repo"`) {
		t.Fatalf("body missing kind=no_owning_sync_repo: %s", body)
	}
}

func TestHandleRepoLink_PathNotAbs(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	_ = newLinkAPITestEnv(t, s)
	resp, body := apiPost(t, ts.URL+"/repos/TEST/link",
		map[string]any{"path": "./relative"})
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d body=%s; want 400", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"kind":"path_not_absolute"`) {
		t.Fatalf("body missing kind=path_not_absolute: %s", body)
	}
}

func TestHandleRepoLink_PathNotGit(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	env := newLinkAPITestEnv(t, s)
	notGit := filepath.Join(env.dir, "not-git")
	if err := os.MkdirAll(notGit, 0o755); err != nil {
		t.Fatalf("mkdir notGit: %v", err)
	}
	resp, body := apiPost(t, ts.URL+"/repos/TEST/link",
		map[string]any{"path": notGit})
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d body=%s; want 400", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"kind":"path_not_git"`) {
		t.Fatalf("body missing kind=path_not_git: %s", body)
	}
}

func TestHandleRepoLink_PrefixMismatch(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	env := newLinkAPITestEnv(t, s)
	resp, body := apiPost(t, ts.URL+"/repos/TEST/link",
		map[string]any{"prefix": "OTHR", "path": env.projectPath})
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d body=%s; want 400", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "does not match URL prefix") {
		t.Fatalf("body missing mismatch message: %s", body)
	}
}
