package api_test

// HTTP-side coverage for POST /repos/{prefix}/sync/setup (BACI-110).
// The engine itself is exhaustively tested in internal/sync; the
// scenarios here are the API-shape ones: input validation, dry-run,
// audit-log shape, the renumber-collision 409 envelope, and the
// mode-specific paths (init / clone / attach) that thread through the
// engine via the HTTP layer.
//
// The git-touching tests skip when git isn't on PATH (mirrors the
// pattern in internal/sync/bootstrap_test.go).

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/api"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
	bsync "github.com/mrgeoffrich/bacio/internal/sync"
)

// requireGit short-circuits a test that needs the `git` binary on
// PATH. Mirrors internal/sync/bootstrap_test.go's helper without
// pulling its private package.
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not on PATH: %v", err)
	}
}

// runGit runs `git <args...>` (optionally with -C cwd) and fails the
// test on a non-zero exit. Used to lay out bare remotes and project
// working trees for the integration-style tests below.
func runGit(t *testing.T, cwd string, args ...string) {
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

// setupGitIdentity makes commits possible on a freshly-init'd repo by
// pinning user.name / user.email so the test doesn't depend on the
// host's global git config.
func setupGitIdentity(t *testing.T, repoDir string) {
	t.Helper()
	runGit(t, repoDir, "config", "user.email", "tester@example.invalid")
	runGit(t, repoDir, "config", "user.name", "tester")
}

// initBareRemote creates a bare git repo to stand in for the team
// sync remote. Returns its absolute path.
func initBareRemote(t *testing.T, dir string) string {
	t.Helper()
	bare := filepath.Join(dir, "remote.git")
	runGit(t, "", "init", "--bare", "-b", "main", bare)
	return bare
}

// initProjectRepo lays out a tiny project working tree as a git repo
// (InitSyncRepo / CloneSyncRepo both require this).
func initProjectRepo(t *testing.T, dir, name string) string {
	t.Helper()
	project := filepath.Join(dir, name)
	runGit(t, "", "init", "-b", "main", project)
	setupGitIdentity(t, project)
	return project
}

// envGitAuthor pins author/committer identity via env vars so the
// engine's `git commit` runs don't depend on host config.
func envGitAuthor(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_AUTHOR_NAME", "tester")
	t.Setenv("GIT_AUTHOR_EMAIL", "tester@example.invalid")
	t.Setenv("GIT_COMMITTER_NAME", "tester")
	t.Setenv("GIT_COMMITTER_EMAIL", "tester@example.invalid")
}

// openEphemeralStore opens an in-memory bacio store, cleaned up at
// test teardown. Used by the seeding helpers below so they don't
// share the API's DB.
func openEphemeralStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "seed.sqlite"))
	if err != nil {
		t.Fatalf("open ephemeral store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// historyContains returns true when the store's audit log carries at
// least one row whose Op matches and whose Details includes the
// supplied substring (substring matching keeps the test resilient to
// future detail-line additions).
func historyContains(t *testing.T, s *store.Store, op, detailsSubstr string) bool {
	t.Helper()
	rows, err := s.ListHistory(store.HistoryFilter{})
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	for _, r := range rows {
		if r.Op != op {
			continue
		}
		if detailsSubstr == "" || strings.Contains(r.Details, detailsSubstr) {
			return true
		}
	}
	return false
}

// syncSetupRespFull mirrors api.SyncSetupOut just deeply enough for
// the tests. The engine result types are reused as-is so the wire
// shape and the engine's JSON encoding don't drift.
type syncSetupRespFull struct {
	Mode              string                  `json:"mode"`
	Init              *bsync.InitResult       `json:"init,omitempty"`
	Clone             *bsync.CloneResult      `json:"clone,omitempty"`
	PreviewCollisions *bsync.CollisionPreview `json:"preview_collisions,omitempty"`
}

// errorEnv mirrors api's error envelope shape for the tests that need
// to inspect code / details.
type errorEnv struct {
	Error   string         `json:"error"`
	Code    string         `json:"code"`
	Details map[string]any `json:"details,omitempty"`
}

// seedSyncRemote runs the engine's InitSyncRepo against `bare`,
// populating it with a sentinel + .gitattributes and pushing them.
// Returns the path of the seeded sync-repo clone. Uses an ephemeral
// store so the seeding doesn't pollute the API test's DB.
func seedSyncRemote(t *testing.T, tdir, bare string) string {
	t.Helper()
	projectSeed := initProjectRepo(t, tdir, "project-seed")
	syncSeed := filepath.Join(tdir, "sync-seed")
	s2 := openEphemeralStore(t)
	if _, err := s2.CreateRepo("SEED", "seed", projectSeed, ""); err != nil {
		t.Fatalf("seed repo: %v", err)
	}
	eng := &bsync.Engine{Store: s2, Actor: "seed"}
	if _, err := eng.InitSyncRepo(context.Background(), projectSeed, bsync.InitOptions{
		LocalPath: syncSeed,
		Remote:    bare,
	}); err != nil {
		t.Fatalf("seed init: %v", err)
	}
	return syncSeed
}

// seedSyncRemoteWithIssue is like seedSyncRemote but the seeded repo
// carries an issue under prefix "MINI" so a clone into a target that
// already has MINI-1 will collide. Returns the seeded clone path.
func seedSyncRemoteWithIssue(t *testing.T, tdir, bare string) string {
	t.Helper()
	projectSeed := initProjectRepo(t, tdir, "project-seed-coll")
	syncSeed := filepath.Join(tdir, "sync-seed-coll")
	s2 := openEphemeralStore(t)
	r2, err := s2.CreateRepo("MINI", "mini", projectSeed, "")
	if err != nil {
		t.Fatalf("seed repo: %v", err)
	}
	if _, err := s2.CreateIssue(r2.ID, nil, "remote first", "", model.StateTodo, nil); err != nil {
		t.Fatalf("seed issue: %v", err)
	}
	eng := &bsync.Engine{Store: s2, Actor: "seed"}
	if _, err := eng.InitSyncRepo(context.Background(), projectSeed, bsync.InitOptions{
		LocalPath: syncSeed,
		Remote:    bare,
	}); err != nil {
		t.Fatalf("seed init: %v", err)
	}
	return syncSeed
}

// seedRepoWithPath inserts a repo whose path is a real git working
// tree. Used by tests that drive the API endpoint — every code path
// in handleSyncSetup needs a non-empty repo.Path.
func seedRepoWithPath(t *testing.T, s *store.Store, prefix, path string) *model.Repo {
	t.Helper()
	repo, err := s.CreateRepo(prefix, strings.ToLower(prefix), path, "")
	if err != nil {
		t.Fatalf("CreateRepo %s: %v", prefix, err)
	}
	return repo
}

// ---------- input validation ----------

// TestSyncSetupUnknownPrefix: 404 when the URL prefix doesn't resolve.
func TestSyncSetupUnknownPrefix(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp, body := apiPost(t, ts.URL+"/repos/ZZZZ/sync/setup",
		map[string]any{"mode": "init", "local_path": "/tmp/x"})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body: %s)", resp.StatusCode, body)
	}
}

// TestSyncSetupPhantomRepo: a repo with no local working tree
// (path=="") is refused with 400, not allowed to fall into the
// engine where the error would be more confusing.
func TestSyncSetupPhantomRepo(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	phantom, err := s.CreatePhantomRepo(uuidFor("phantom-setup"), "PHAN", "phantom-name", "")
	if err != nil {
		t.Fatalf("CreatePhantomRepo: %v", err)
	}
	resp, body := apiPost(t, ts.URL+"/repos/"+phantom.Prefix+"/sync/setup",
		map[string]any{"mode": "init", "local_path": "/tmp/x"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body: %s)", resp.StatusCode, body)
	}
	var env errorEnv
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Code != "invalid_input" {
		t.Fatalf("code = %q, want invalid_input", env.Code)
	}
}

// TestSyncSetupUnknownMode: an unrecognised mode is rejected with a
// field-anchored 400.
func TestSyncSetupUnknownMode(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepoWithPath(t, s, "MINI", t.TempDir())
	resp, body := apiPost(t, ts.URL+"/repos/"+repo.Prefix+"/sync/setup",
		map[string]any{"mode": "yolo"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body: %s)", resp.StatusCode, body)
	}
	var env errorEnv
	_ = json.Unmarshal(body, &env)
	if d, _ := env.Details["field"].(string); d != "mode" {
		t.Fatalf("details.field = %q, want mode (env: %+v)", d, env)
	}
}

// TestSyncSetupUnknownField: strict decode rejects unknown JSON keys.
// Same defence-in-depth as every other --json mutation.
func TestSyncSetupUnknownField(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepoWithPath(t, s, "MINI", t.TempDir())
	resp, _ := apiPost(t, ts.URL+"/repos/"+repo.Prefix+"/sync/setup",
		map[string]any{"mode": "clone", "remote": "git@e.com:x.git", "bogus": true})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

// TestSyncSetupInitRequiresLocalPath: mode="init" without local_path
// surfaces a field-anchored 400.
func TestSyncSetupInitRequiresLocalPath(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepoWithPath(t, s, "MINI", t.TempDir())
	resp, body := apiPost(t, ts.URL+"/repos/"+repo.Prefix+"/sync/setup",
		map[string]any{"mode": "init", "remote": "git@e.com:x.git"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body: %s)", resp.StatusCode, body)
	}
	var env errorEnv
	_ = json.Unmarshal(body, &env)
	if env.Details["field"] != "local_path" {
		t.Fatalf("details.field = %v, want local_path", env.Details["field"])
	}
}

// TestSyncSetupCloneRequiresRemote: mode="clone" without remote
// surfaces a field-anchored 400.
func TestSyncSetupCloneRequiresRemote(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepoWithPath(t, s, "MINI", t.TempDir())
	resp, body := apiPost(t, ts.URL+"/repos/"+repo.Prefix+"/sync/setup",
		map[string]any{"mode": "clone"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body: %s)", resp.StatusCode, body)
	}
	var env errorEnv
	_ = json.Unmarshal(body, &env)
	if env.Details["field"] != "remote" {
		t.Fatalf("details.field = %v, want remote", env.Details["field"])
	}
}

// TestSyncSetupAttachRequiresRemote: mode="attach" without remote is
// 400 — the registry lookup needs the key.
func TestSyncSetupAttachRequiresRemote(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepoWithPath(t, s, "MINI", t.TempDir())
	resp, body := apiPost(t, ts.URL+"/repos/"+repo.Prefix+"/sync/setup",
		map[string]any{"mode": "attach"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body: %s)", resp.StatusCode, body)
	}
	var env errorEnv
	_ = json.Unmarshal(body, &env)
	if env.Details["field"] != "remote" {
		t.Fatalf("details.field = %v, want remote", env.Details["field"])
	}
}

// TestSyncSetupAttachRejectsLocalPath: mode="attach" forbids
// local_path — the registry's local_path is used. A caller-supplied
// one risks pointing at a stale clone, so we hard-error rather than
// silently override.
func TestSyncSetupAttachRejectsLocalPath(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepoWithPath(t, s, "MINI", t.TempDir())
	resp, body := apiPost(t, ts.URL+"/repos/"+repo.Prefix+"/sync/setup",
		map[string]any{
			"mode":       "attach",
			"remote":     "git@e.com:x.git",
			"local_path": "/tmp/wrong",
		})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body: %s)", resp.StatusCode, body)
	}
	var env errorEnv
	_ = json.Unmarshal(body, &env)
	if env.Details["field"] != "local_path" {
		t.Fatalf("details.field = %v, want local_path", env.Details["field"])
	}
}

// TestSyncSetupAttachUnknownRemote: mode="attach" against a remote
// the registry doesn't carry surfaces 404 — the contract is "join an
// existing entry", so a missing one is the wrong tool for the job
// (the caller should switch to mode="clone").
func TestSyncSetupAttachUnknownRemote(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepoWithPath(t, s, "MINI", t.TempDir())
	resp, body := apiPost(t, ts.URL+"/repos/"+repo.Prefix+"/sync/setup",
		map[string]any{
			"mode":   "attach",
			"remote": "git@e.com:never-registered.git",
		})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body: %s)", resp.StatusCode, body)
	}
}

// ---------- happy path: init ----------

// TestSyncSetupInitHappyPath: full init flow against a fresh bare
// remote. Verifies the engine result is echoed, .bacio/config.yaml is
// written into the project, sync_remotes has the row, and the audit
// log records sync.init.
func TestSyncSetupInitHappyPath(t *testing.T) {
	requireGit(t)
	envGitAuthor(t)
	ts, s := newTestAPI(t, api.Options{})

	tdir := t.TempDir()
	bare := initBareRemote(t, tdir)
	project := initProjectRepo(t, tdir, "project")
	repo := seedRepoWithPath(t, s, "MINI", project)

	syncLocal := filepath.Join(tdir, "sync-A")
	resp, body := apiPost(t, ts.URL+"/repos/"+repo.Prefix+"/sync/setup",
		map[string]any{
			"mode":       "init",
			"local_path": syncLocal,
			"remote":     bare,
		})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", resp.StatusCode, body)
	}
	var out syncSetupRespFull
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Mode != "init" || out.Init == nil {
		t.Fatalf("response Mode=%q Init=%+v, want mode=init with non-nil Init", out.Mode, out.Init)
	}
	if out.Init.LocalPath != syncLocal {
		t.Fatalf("init local_path = %q, want %q", out.Init.LocalPath, syncLocal)
	}
	if !bsync.IsSyncRepo(syncLocal) {
		t.Fatal("sync repo should carry bacio-sync.yaml")
	}
	cfg, err := bsync.ReadProjectConfig(project)
	if err != nil {
		t.Fatalf("read project config: %v", err)
	}
	if cfg.Sync.Remote != bare {
		t.Fatalf("project config remote = %q, want %q", cfg.Sync.Remote, bare)
	}
	rec, err := s.GetSyncRemote(bare)
	if err != nil {
		t.Fatalf("get sync remote: %v", err)
	}
	if rec.LocalPath != syncLocal {
		t.Fatalf("sync_remotes local_path = %q, want %q", rec.LocalPath, syncLocal)
	}
	if !historyContains(t, s, "sync.init", "") {
		t.Fatal("expected a sync.init audit row")
	}
}

// TestSyncSetupInitDryRun: dry-run is honoured — the engine projects
// counts but writes nothing, the response carries X-Dry-Run: applied,
// and no audit row is recorded.
func TestSyncSetupInitDryRun(t *testing.T) {
	requireGit(t)
	envGitAuthor(t)
	ts, s := newTestAPI(t, api.Options{})

	tdir := t.TempDir()
	project := initProjectRepo(t, tdir, "project")
	repo := seedRepoWithPath(t, s, "MINI", project)

	syncLocal := filepath.Join(tdir, "sync-A")
	resp, body := apiPost(t, ts.URL+"/repos/"+repo.Prefix+"/sync/setup?dry_run=true",
		map[string]any{
			"mode":       "init",
			"local_path": syncLocal,
		})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", resp.StatusCode, body)
	}
	if got := resp.Header.Get("X-Dry-Run"); got != "applied" {
		t.Fatalf("X-Dry-Run = %q, want applied", got)
	}
	if _, err := os.Stat(filepath.Join(syncLocal, "bacio-sync.yaml")); !os.IsNotExist(err) {
		t.Fatalf("dry-run wrote a sync sentinel: %v", err)
	}
	if historyContains(t, s, "sync.init", "") {
		t.Fatal("dry-run should not record sync.init")
	}
}

// ---------- happy path: clone ----------

// TestSyncSetupCloneHappyPath: drives the clone flow against a bare
// remote seeded by an init from another (ephemeral) store. The
// project repo joins via the HTTP endpoint and the engine's clone
// result is echoed back.
func TestSyncSetupCloneHappyPath(t *testing.T) {
	requireGit(t)
	envGitAuthor(t)
	ts, s := newTestAPI(t, api.Options{})

	tdir := t.TempDir()
	bare := initBareRemote(t, tdir)
	if seedSyncRemote(t, tdir, bare) == "" {
		t.Fatal("seedSyncRemote returned empty path")
	}

	project := initProjectRepo(t, tdir, "project-B")
	repo := seedRepoWithPath(t, s, "PROJ", project)

	syncLocal := filepath.Join(tdir, "sync-B")
	resp, body := apiPost(t, ts.URL+"/repos/"+repo.Prefix+"/sync/setup",
		map[string]any{
			"mode":       "clone",
			"remote":     bare,
			"local_path": syncLocal,
		})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", resp.StatusCode, body)
	}
	var out syncSetupRespFull
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Mode != "clone" || out.Clone == nil {
		t.Fatalf("response Mode=%q Clone=%+v, want mode=clone with non-nil Clone", out.Mode, out.Clone)
	}
	if out.Clone.Remote != bare {
		t.Fatalf("clone remote = %q, want %q", out.Clone.Remote, bare)
	}
	cfg, err := bsync.ReadProjectConfig(project)
	if err != nil {
		t.Fatalf("read project config: %v", err)
	}
	if cfg.Sync.Remote != bare {
		t.Fatalf("project config remote = %q, want %q", cfg.Sync.Remote, bare)
	}
	if _, err := s.GetSyncRemote(bare); err != nil {
		t.Fatalf("sync_remotes row missing: %v", err)
	}
	if !historyContains(t, s, "sync.clone", "") {
		t.Fatal("expected a sync.clone audit row")
	}
}

// ---------- happy path: attach ----------

// TestSyncSetupAttachHappyPath: a sync_remotes row already exists
// (seeded by some earlier init/clone), and a fresh project repo joins
// it via mode="attach". The clone result is echoed and the audit row
// carries attach=true in its details.
func TestSyncSetupAttachHappyPath(t *testing.T) {
	requireGit(t)
	envGitAuthor(t)
	ts, s := newTestAPI(t, api.Options{})

	tdir := t.TempDir()
	bare := initBareRemote(t, tdir)
	seededClone := seedSyncRemote(t, tdir, bare)

	// Register the seeded clone in our test API's DB so attach can find it.
	if err := s.UpsertSyncRemote(bare, seededClone); err != nil {
		t.Fatalf("UpsertSyncRemote: %v", err)
	}

	project := initProjectRepo(t, tdir, "project-C")
	repo := seedRepoWithPath(t, s, "PROJ", project)

	resp, body := apiPost(t, ts.URL+"/repos/"+repo.Prefix+"/sync/setup",
		map[string]any{
			"mode":   "attach",
			"remote": bare,
		})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", resp.StatusCode, body)
	}
	var out syncSetupRespFull
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Mode != "attach" || out.Clone == nil {
		t.Fatalf("response Mode=%q Clone=%+v, want mode=attach with non-nil Clone", out.Mode, out.Clone)
	}
	if out.Clone.LocalPath != seededClone {
		t.Fatalf("attach local_path = %q, want %q (registry's local_path)",
			out.Clone.LocalPath, seededClone)
	}
	cfg, err := bsync.ReadProjectConfig(project)
	if err != nil {
		t.Fatalf("read project config: %v", err)
	}
	if cfg.Sync.Remote != bare {
		t.Fatalf("project config remote = %q, want %q", cfg.Sync.Remote, bare)
	}
	if !historyContains(t, s, "sync.clone", "attach=true") {
		t.Fatal("expected a sync.clone audit row with attach=true marker")
	}
}

// ---------- renumber-collision 409 ----------

// TestSyncSetupCloneRenumberCollision: when the local DB carries
// data that would be renumbered by the imported sync repo, the
// endpoint returns 409 + a preview body — no DB / fs writes — and a
// re-POST with allow_renumber=true succeeds.
//
// Constructing the collision needs the local DB and the seeded sync
// repo to share the same repo-UUID; otherwise the engine refuses with
// a 500 "prefix already used by repo X" error rather than the
// renumber-collision pathway. We do this in two passes: first a
// successful clone (so the API DB inherits the seeded repo's uuid),
// then delete the imported MINI-1 and re-create it locally with a
// fresh uuid that collides on number with the seed.
func TestSyncSetupCloneRenumberCollision(t *testing.T) {
	requireGit(t)
	envGitAuthor(t)
	ts, s := newTestAPI(t, api.Options{})

	tdir := t.TempDir()
	bare := initBareRemote(t, tdir)
	seedSyncRemoteWithIssue(t, tdir, bare)

	// 1. Project repo + API DB row, with a path of its own so the
	// engine can write .bacio/config.yaml.
	project := initProjectRepo(t, tdir, "project-coll")
	apiRepo := seedRepoWithPath(t, s, "PROJ", project)

	// 2. First clone: pulls the seed's MINI repo (uuid X) + MINI-1
	// (uuid Y) into the API DB as a phantom (Path:"") under prefix MINI.
	syncLocal := filepath.Join(tdir, "sync-coll")
	resp0, body0 := apiPost(t, ts.URL+"/repos/"+apiRepo.Prefix+"/sync/setup",
		map[string]any{
			"mode":       "clone",
			"remote":     bare,
			"local_path": syncLocal,
		})
	if resp0.StatusCode != http.StatusOK {
		t.Fatalf("seed clone status = %d, want 200 (body: %s)", resp0.StatusCode, body0)
	}

	// 3. Delete the imported MINI-1 and clear its sync_state so the
	// next import doesn't propagate the delete back. Then re-create
	// MINI-1 locally with a fresh uuid — that's the row the next
	// import will renumber.
	miniRepo, err := s.GetRepoByPrefix("MINI")
	if err != nil {
		t.Fatalf("GetRepoByPrefix MINI: %v", err)
	}
	if _, err := s.DB.Exec(`DELETE FROM issues WHERE repo_id = ? AND number = 1`, miniRepo.ID); err != nil {
		t.Fatalf("delete imported MINI-1: %v", err)
	}
	if _, err := s.DB.Exec(`DELETE FROM sync_state WHERE kind = 'issue'`); err != nil {
		t.Fatalf("clear issue sync_state: %v", err)
	}
	freshIssue, err := s.CreateIssue(miniRepo.ID, nil, "local replacement", "", model.StateTodo, nil)
	if err != nil {
		t.Fatalf("create fresh MINI-1: %v", err)
	}
	if _, err := s.DB.Exec(`UPDATE issues SET number = 1 WHERE id = ?`, freshIssue.ID); err != nil {
		t.Fatalf("force fresh issue number: %v", err)
	}

	// 4. Re-clone via the API. This time the engine sees the local
	// MINI-1 (uuid != seed's) and the incoming MINI-1 — collision
	// preview, 409.
	resp, body := apiPost(t, ts.URL+"/repos/"+apiRepo.Prefix+"/sync/setup",
		map[string]any{
			"mode":       "clone",
			"remote":     bare,
			"local_path": syncLocal,
		})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body: %s)", resp.StatusCode, body)
	}
	var out syncSetupRespFull
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.PreviewCollisions == nil {
		t.Fatal("expected preview_collisions on 409 response")
	}
	if len(out.PreviewCollisions.Renumbered) == 0 && len(out.PreviewCollisions.Renamed) == 0 {
		t.Fatalf("preview_collisions empty: %+v", out.PreviewCollisions)
	}

	// 5. Re-POST with allow_renumber: the engine renumbers, the call
	// succeeds.
	resp2, body2 := apiPost(t, ts.URL+"/repos/"+apiRepo.Prefix+"/sync/setup",
		map[string]any{
			"mode":           "clone",
			"remote":         bare,
			"local_path":     syncLocal,
			"allow_renumber": true,
		})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("retry status = %d, want 200 (body: %s)", resp2.StatusCode, body2)
	}
}
