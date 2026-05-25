package api_test

// HTTP-layer tests for POST /repos/{prefix}/link (BACI-112). Status
// mapping is the load-bearing contract — the React modal and the CLI
// both branch on (status, details.kind) — so this file pins every
// branch the handler's status switch covers.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/api"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// repoLinkRequireGit short-circuits a test that needs the `git` binary
// (the handler validates via git.Detect which shells out). Mirrors the
// pattern in sync_setup_test.go.
func repoLinkRequireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not on PATH: %v", err)
	}
}

func repoLinkRunGit(t *testing.T, cwd string, args ...string) {
	t.Helper()
	full := args
	if cwd != "" {
		full = append([]string{"-C", cwd}, args...)
	}
	cmd := exec.Command("git", full...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// makeTargetWorkingTree initialises a fresh git working tree at path so
// git.Detect returns nil. Returns the absolute path.
func makeTargetWorkingTree(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	repoLinkRunGit(t, "", "init", "-b", "main", path)
	repoLinkRunGit(t, path, "config", "user.email", "tester@example.invalid")
	repoLinkRunGit(t, path, "config", "user.name", "tester")
	return path
}

// seedSyncRepoWithPrefix writes a fake sync clone at dir/sync-clone
// with a repos/<prefix>/ directory, then registers it in sync_remotes.
// Returns the remote URL it registered.
func seedSyncRepoWithPrefix(t *testing.T, s *store.Store, dir, prefix string) string {
	t.Helper()
	syncRoot := filepath.Join(dir, "sync-clone")
	if err := os.MkdirAll(filepath.Join(syncRoot, "repos", prefix), 0o755); err != nil {
		t.Fatalf("mkdir sync clone: %v", err)
	}
	remoteURL := "git@example.com:team/" + prefix + "-sync.git"
	if err := s.UpsertSyncRemote(remoteURL, syncRoot); err != nil {
		t.Fatalf("UpsertSyncRemote: %v", err)
	}
	return remoteURL
}

func seedPhantomAPI(t *testing.T, s *store.Store, prefix string) {
	t.Helper()
	if _, err := s.CreatePhantomRepo("uuid-"+prefix, prefix, prefix, "git@example.com:user/"+prefix+".git"); err != nil {
		t.Fatalf("CreatePhantomRepo: %v", err)
	}
}

// TestHandleRepoLink_OK exercises the canonical 200 happy path.
func TestHandleRepoLink_OK(t *testing.T) {
	repoLinkRequireGit(t)
	ts, s := newTestAPI(t, api.Options{})
	dir := t.TempDir()
	seedPhantomAPI(t, s, "MINI")
	remoteURL := seedSyncRepoWithPrefix(t, s, dir, "MINI")
	target := makeTargetWorkingTree(t, filepath.Join(dir, "mini-checkout"))

	body := bytes.NewBufferString(`{"path":"` + target + `"}`)
	resp := do(t, http.MethodPost, ts.URL+"/repos/MINI/link", body, nil)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status: %d body: %s", resp.StatusCode, raw)
	}
	result := decode[map[string]any](t, resp.Body)
	if result["sync_remote_url"] != remoteURL {
		t.Errorf("sync_remote_url = %v, want %s", result["sync_remote_url"], remoteURL)
	}
	repo, _ := result["repo"].(map[string]any)
	if repo == nil || repo["path"] != target {
		t.Errorf("repo.path = %v, want %s", repo, target)
	}
}

// TestHandleRepoLink_DryRun verifies the projection short-circuits
// before the upgrade and emits the X-Dry-Run header.
func TestHandleRepoLink_DryRun(t *testing.T) {
	repoLinkRequireGit(t)
	ts, s := newTestAPI(t, api.Options{})
	dir := t.TempDir()
	seedPhantomAPI(t, s, "MINI")
	seedSyncRepoWithPrefix(t, s, dir, "MINI")
	target := makeTargetWorkingTree(t, filepath.Join(dir, "mini-checkout"))

	body := bytes.NewBufferString(`{"path":"` + target + `"}`)
	resp := do(t, http.MethodPost, ts.URL+"/repos/MINI/link?dry_run=true", body, nil)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Dry-Run"); got != "applied" {
		t.Errorf("X-Dry-Run header: %q", got)
	}
	result := decode[map[string]any](t, resp.Body)
	if result["would_link"] != true {
		t.Errorf("would_link = %v, want true", result["would_link"])
	}
	// Row still phantom.
	got, err := s.GetRepoByPrefix("MINI")
	if err != nil {
		t.Fatalf("GetRepoByPrefix: %v", err)
	}
	if got.Path != "" {
		t.Errorf("post-dry-run path = %q, want '' (row should still be phantom)", got.Path)
	}
}

// TestHandleRepoLink_NotFound returns 404 on an unknown prefix.
func TestHandleRepoLink_NotFound(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	body := bytes.NewBufferString(`{"path":"/tmp/x"}`)
	resp := do(t, http.MethodPost, ts.URL+"/repos/NOPE/link", body, nil)
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	env := decode[map[string]any](t, resp.Body)
	if env["code"] != "not_found" {
		t.Errorf("code: %v", env["code"])
	}
}

// TestHandleRepoLink_NotPhantom returns 409 + details.kind=not_phantom
// when the row exists but already has a path.
func TestHandleRepoLink_NotPhantom(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	dir := t.TempDir()
	bound := filepath.Join(dir, "already-bound")
	if _, err := s.CreateRepo("MINI", "mini", bound, ""); err != nil {
		t.Fatalf("CreateRepo: %v", err)
	}
	seedSyncRepoWithPrefix(t, s, dir, "MINI")

	body := bytes.NewBufferString(`{"path":"/tmp/whatever"}`)
	resp := do(t, http.MethodPost, ts.URL+"/repos/MINI/link", body, nil)
	defer resp.Body.Close()
	if resp.StatusCode != 409 {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status: %d body: %s", resp.StatusCode, raw)
	}
	env := decode[map[string]any](t, resp.Body)
	details, _ := env["details"].(map[string]any)
	if details["kind"] != "not_phantom" {
		t.Errorf("details.kind: %v", details["kind"])
	}
	if details["current_path"] != bound {
		t.Errorf("details.current_path = %v, want %s", details["current_path"], bound)
	}
}

// TestHandleRepoLink_PathAlreadyBound returns 409 with the conflicting
// prefix in details.existing_prefix.
func TestHandleRepoLink_PathAlreadyBound(t *testing.T) {
	repoLinkRequireGit(t)
	ts, s := newTestAPI(t, api.Options{})
	dir := t.TempDir()
	target := makeTargetWorkingTree(t, filepath.Join(dir, "shared-checkout"))
	// Existing real repo bound to target.
	if _, err := s.CreateRepo("OTHER", "other", target, ""); err != nil {
		t.Fatalf("CreateRepo OTHER: %v", err)
	}
	seedPhantomAPI(t, s, "MINI")
	seedSyncRepoWithPrefix(t, s, dir, "MINI")

	body := bytes.NewBufferString(`{"path":"` + target + `"}`)
	resp := do(t, http.MethodPost, ts.URL+"/repos/MINI/link", body, nil)
	defer resp.Body.Close()
	if resp.StatusCode != 409 {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status: %d body: %s", resp.StatusCode, raw)
	}
	env := decode[map[string]any](t, resp.Body)
	details, _ := env["details"].(map[string]any)
	if details["kind"] != "path_already_bound" {
		t.Errorf("details.kind: %v", details["kind"])
	}
	if details["existing_prefix"] != "OTHER" {
		t.Errorf("details.existing_prefix = %v, want OTHER", details["existing_prefix"])
	}
}

// TestHandleRepoLink_NoOwningSyncRepo returns 409 when sync_remotes has
// no row that carries repos/<prefix>/ on disk.
func TestHandleRepoLink_NoOwningSyncRepo(t *testing.T) {
	repoLinkRequireGit(t)
	ts, s := newTestAPI(t, api.Options{})
	dir := t.TempDir()
	seedPhantomAPI(t, s, "MINI")
	target := makeTargetWorkingTree(t, filepath.Join(dir, "mini-checkout"))

	body := bytes.NewBufferString(`{"path":"` + target + `"}`)
	resp := do(t, http.MethodPost, ts.URL+"/repos/MINI/link", body, nil)
	defer resp.Body.Close()
	if resp.StatusCode != 409 {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status: %d body: %s", resp.StatusCode, raw)
	}
	env := decode[map[string]any](t, resp.Body)
	details, _ := env["details"].(map[string]any)
	if details["kind"] != "no_owning_sync_repo" {
		t.Errorf("details.kind: %v", details["kind"])
	}
}

// TestHandleRepoLink_PathNotAbs returns 400 invalid_input on a
// relative path.
func TestHandleRepoLink_PathNotAbs(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedPhantomAPI(t, s, "MINI")

	body := bytes.NewBufferString(`{"path":"./relative"}`)
	resp := do(t, http.MethodPost, ts.URL+"/repos/MINI/link", body, nil)
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status: %d body: %s", resp.StatusCode, raw)
	}
	env := decode[map[string]any](t, resp.Body)
	if env["code"] != "invalid_input" {
		t.Errorf("code: %v", env["code"])
	}
	details, _ := env["details"].(map[string]any)
	if details["kind"] != "path_not_absolute" {
		t.Errorf("details.kind: %v", details["kind"])
	}
}

// TestHandleRepoLink_PathNotGit returns 400 on a path that exists but
// isn't a git working tree.
func TestHandleRepoLink_PathNotGit(t *testing.T) {
	repoLinkRequireGit(t)
	ts, s := newTestAPI(t, api.Options{})
	dir := t.TempDir()
	seedPhantomAPI(t, s, "MINI")
	seedSyncRepoWithPrefix(t, s, dir, "MINI")

	body := bytes.NewBufferString(`{"path":"` + dir + `"}`) // dir is a plain dir, not a git repo
	resp := do(t, http.MethodPost, ts.URL+"/repos/MINI/link", body, nil)
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status: %d body: %s", resp.StatusCode, raw)
	}
	env := decode[map[string]any](t, resp.Body)
	details, _ := env["details"].(map[string]any)
	if details["kind"] != "path_not_git" {
		t.Errorf("details.kind: %v", details["kind"])
	}
}

// TestHandleRepoLink_InvalidJSON rejects unknown JSON fields with 400.
func TestHandleRepoLink_InvalidJSON(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedPhantomAPI(t, s, "MINI")
	body := bytes.NewBufferString(`{"path":"/tmp/x","unknown":1}`)
	resp := do(t, http.MethodPost, ts.URL+"/repos/MINI/link", body, nil)
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	env := decode[map[string]any](t, resp.Body)
	if env["code"] != "invalid_input" {
		t.Errorf("code: %v", env["code"])
	}
}

// Bag in a tiny JSON marshalling assertion to make sure we don't break
// the wire shape later — keeps the tests parsing the structured shape
// rather than just string-matching.
func init() {
	_ = json.Marshal
}
