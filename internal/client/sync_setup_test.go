package client

// Tests for the cross-transport SetupSync entry point (BACI-111).
//
// Local impl coverage: every mode (init / clone / attach), plus the
// collision-refusal path (ErrSetupCollision + populated
// PreviewCollisions). We use real git on disk because the engine
// shells out — these tests skip when git isn't on PATH.
//
// Remote impl coverage: an httptest.Server returns 200 / 409 / 400 /
// 404 bodies in turn, asserting the right SyncSetupResult and error
// surface.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
	bsync "github.com/mrgeoffrich/bacio/internal/sync"
)

// requireGit short-circuits a test that needs the `git` binary on
// PATH. Same pattern as internal/api/sync_setup_test.go.
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not on PATH: %v", err)
	}
}

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

func setupGitIdentity(t *testing.T, repoDir string) {
	t.Helper()
	runGit(t, repoDir, "config", "user.email", "tester@example.invalid")
	runGit(t, repoDir, "config", "user.name", "tester")
}

func initBareRemote(t *testing.T, dir string) string {
	t.Helper()
	bare := filepath.Join(dir, "remote.git")
	runGit(t, "", "init", "--bare", "-b", "main", bare)
	return bare
}

func initProjectRepo(t *testing.T, dir, name string) string {
	t.Helper()
	project := filepath.Join(dir, name)
	runGit(t, "", "init", "-b", "main", project)
	setupGitIdentity(t, project)
	return project
}

func envGitAuthor(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_AUTHOR_NAME", "tester")
	t.Setenv("GIT_AUTHOR_EMAIL", "tester@example.invalid")
	t.Setenv("GIT_COMMITTER_NAME", "tester")
	t.Setenv("GIT_COMMITTER_EMAIL", "tester@example.invalid")
}

// openTestLocalClient builds a *localClient against an ephemeral DB.
// Returns the client, its underlying store, and a cleanup function.
// Bypasses newLocalClient so the test can set the DB path explicitly
// and so that store.DefaultPath() inside SetupSync resolves the actor's
// real ~/.bacio/db.sqlite (which the test's AcquireSyncLock will use).
//
// SetupSync acquires the cross-process sync lock at store.DefaultPath(),
// not the test's DB. That's fine for the test — we just need that lock
// path to be acquireable; it doesn't need to match the test store.
func openTestLocalClient(t *testing.T) (*localClient, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.sqlite")
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	c := &localClient{store: s, actor: "tester"}
	return c, dbPath
}

func seedRepo(t *testing.T, s *store.Store, prefix, path string) *model.Repo {
	t.Helper()
	repo, err := s.CreateRepo(prefix, strings.ToLower(prefix), path, "")
	if err != nil {
		t.Fatalf("CreateRepo %s: %v", prefix, err)
	}
	return repo
}

// ---------- input validation ----------

// TestLocalSetupSync_NilRepo refuses a nil repo with a clear error.
func TestLocalSetupSync_NilRepo(t *testing.T) {
	c, _ := openTestLocalClient(t)
	_, err := c.SetupSync(context.Background(), nil, inputs.SyncSetupInput{Mode: "init", LocalPath: "/tmp"})
	if err == nil {
		t.Fatal("expected error on nil repo")
	}
}

// TestLocalSetupSync_PhantomRefused: a repo with no working tree is
// refused at the client layer (matching the API handler precedent), so
// the engine doesn't fail deep with a misleading "not a git repo" error.
func TestLocalSetupSync_PhantomRefused(t *testing.T) {
	c, _ := openTestLocalClient(t)
	phantom, err := c.store.CreatePhantomRepo("uuid-phantom", "PHAN", "phantom", "")
	if err != nil {
		t.Fatalf("CreatePhantomRepo: %v", err)
	}
	_, err = c.SetupSync(context.Background(), phantom, inputs.SyncSetupInput{Mode: "init", LocalPath: "/tmp"})
	if err == nil || !strings.Contains(err.Error(), "no local working tree") {
		t.Fatalf("expected phantom refusal, got %v", err)
	}
}

// TestLocalSetupSync_UnknownMode rejects a typoed mode with a 400-style
// message including the offered alternatives.
func TestLocalSetupSync_UnknownMode(t *testing.T) {
	c, _ := openTestLocalClient(t)
	repo := seedRepo(t, c.store, "MINI", t.TempDir())
	_, err := c.SetupSync(context.Background(), repo, inputs.SyncSetupInput{Mode: "yolo"})
	if err == nil || !strings.Contains(err.Error(), `mode must be one of`) {
		t.Fatalf("expected mode validation error, got %v", err)
	}
}

// TestLocalSetupSync_InitRequiresLocalPath: mode=init without
// local_path is rejected before the engine runs.
func TestLocalSetupSync_InitRequiresLocalPath(t *testing.T) {
	c, _ := openTestLocalClient(t)
	repo := seedRepo(t, c.store, "MINI", t.TempDir())
	_, err := c.SetupSync(context.Background(), repo, inputs.SyncSetupInput{Mode: "init"})
	if err == nil || !strings.Contains(err.Error(), "local_path") {
		t.Fatalf("expected local_path error, got %v", err)
	}
}

// TestLocalSetupSync_CloneRequiresRemote: mode=clone without remote is
// rejected before the engine runs.
func TestLocalSetupSync_CloneRequiresRemote(t *testing.T) {
	c, _ := openTestLocalClient(t)
	repo := seedRepo(t, c.store, "MINI", t.TempDir())
	_, err := c.SetupSync(context.Background(), repo, inputs.SyncSetupInput{Mode: "clone"})
	if err == nil || !strings.Contains(err.Error(), "remote") {
		t.Fatalf("expected remote error, got %v", err)
	}
}

// TestLocalSetupSync_AttachRejectsLocalPath: attach forbids local_path
// — the registry's local_path is used. Matches the API handler.
func TestLocalSetupSync_AttachRejectsLocalPath(t *testing.T) {
	c, _ := openTestLocalClient(t)
	repo := seedRepo(t, c.store, "MINI", t.TempDir())
	_, err := c.SetupSync(context.Background(), repo, inputs.SyncSetupInput{
		Mode: "attach", Remote: "git@e.com:x.git", LocalPath: "/tmp/wrong",
	})
	if err == nil || !strings.Contains(err.Error(), "must not set local_path") {
		t.Fatalf("expected attach+local_path refusal, got %v", err)
	}
}

// TestLocalSetupSync_AttachUnknownRemote: attach against a remote that
// isn't in sync_remotes returns an explicit "no registry entry" error
// (the caller is told to switch to clone). Mirrors the API handler's
// 404 path.
func TestLocalSetupSync_AttachUnknownRemote(t *testing.T) {
	c, _ := openTestLocalClient(t)
	repo := seedRepo(t, c.store, "MINI", t.TempDir())
	_, err := c.SetupSync(context.Background(), repo, inputs.SyncSetupInput{
		Mode: "attach", Remote: "git@e.com:never.git",
	})
	if err == nil || !strings.Contains(err.Error(), "no sync_remotes registry entry") {
		t.Fatalf("expected unknown-remote error, got %v", err)
	}
}

// ---------- happy path: init ----------

// TestLocalSetupSync_InitHappy: full init flow. Confirms the engine
// runs, the result is wrapped in a SyncSetupResult with Mode="init"
// and non-nil Init, and an audit row is recorded.
func TestLocalSetupSync_InitHappy(t *testing.T) {
	requireGit(t)
	envGitAuthor(t)
	c, _ := openTestLocalClient(t)

	tdir := t.TempDir()
	bare := initBareRemote(t, tdir)
	project := initProjectRepo(t, tdir, "project")
	repo := seedRepo(t, c.store, "MINI", project)

	syncLocal := filepath.Join(tdir, "sync-A")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := c.SetupSync(ctx, repo, inputs.SyncSetupInput{
		Mode:      "init",
		LocalPath: syncLocal,
		Remote:    bare,
	})
	if err != nil {
		t.Fatalf("SetupSync: %v", err)
	}
	if res.Mode != "init" || res.Init == nil {
		t.Fatalf("Mode=%q Init=%+v, want init / non-nil", res.Mode, res.Init)
	}
	if res.Init.LocalPath != syncLocal {
		t.Errorf("LocalPath=%q want %q", res.Init.LocalPath, syncLocal)
	}
	if !bsync.IsSyncRepo(syncLocal) {
		t.Error("sync repo should carry bacio-sync.yaml")
	}
	// Audit row: sync.init for the project.
	rows, err := c.store.ListHistory(store.HistoryFilter{})
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	found := false
	for _, r := range rows {
		if r.Op == "sync.init" && r.RepoPrefix == "MINI" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected sync.init audit row")
	}
}

// ---------- happy path: clone ----------

// TestLocalSetupSync_CloneHappy: seed a remote with bacio data, then
// clone into a fresh project. Result is mode="clone" with a populated
// Clone struct.
func TestLocalSetupSync_CloneHappy(t *testing.T) {
	requireGit(t)
	envGitAuthor(t)
	c, _ := openTestLocalClient(t)

	tdir := t.TempDir()
	bare := initBareRemote(t, tdir)

	// Seed the bare remote with sync content from an ephemeral store.
	seedProject := initProjectRepo(t, tdir, "seed-project")
	seedStore, err := store.Open(filepath.Join(tdir, "seed.sqlite"))
	if err != nil {
		t.Fatalf("open seed store: %v", err)
	}
	defer seedStore.Close()
	if _, err := seedStore.CreateRepo("SEED", "seed", seedProject, ""); err != nil {
		t.Fatalf("seed repo: %v", err)
	}
	seedSync := filepath.Join(tdir, "seed-sync")
	seedEng := &bsync.Engine{Store: seedStore, Actor: "seed"}
	if _, err := seedEng.InitSyncRepo(context.Background(), seedProject, bsync.InitOptions{
		LocalPath: seedSync,
		Remote:    bare,
	}); err != nil {
		t.Fatalf("seed init: %v", err)
	}

	// Now run setup against a fresh project that clones the seeded remote.
	project := initProjectRepo(t, tdir, "project")
	repo := seedRepo(t, c.store, "MINI", project)
	cloneLocal := filepath.Join(tdir, "clone-target")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := c.SetupSync(ctx, repo, inputs.SyncSetupInput{
		Mode:      "clone",
		Remote:    bare,
		LocalPath: cloneLocal,
	})
	if err != nil {
		t.Fatalf("SetupSync: %v", err)
	}
	if res.Mode != "clone" || res.Clone == nil {
		t.Fatalf("Mode=%q Clone=%+v, want clone / non-nil", res.Mode, res.Clone)
	}
	if res.Clone.LocalPath != cloneLocal {
		t.Errorf("LocalPath=%q want %q", res.Clone.LocalPath, cloneLocal)
	}
	if !bsync.IsSyncRepo(cloneLocal) {
		t.Error("clone target should carry bacio-sync.yaml")
	}
}

// ---------- happy path: attach ----------

// TestLocalSetupSync_AttachHappy: pre-populate sync_remotes with a
// registry row, then attach a project to it. The engine reuses the
// registry's local_path via openOrCloneSyncRepo's short-circuit; no
// new clone runs.
func TestLocalSetupSync_AttachHappy(t *testing.T) {
	requireGit(t)
	envGitAuthor(t)
	c, _ := openTestLocalClient(t)

	tdir := t.TempDir()
	bare := initBareRemote(t, tdir)

	// Pre-seed the registry by running clone on a sibling project first.
	otherProject := initProjectRepo(t, tdir, "other")
	otherRepo := seedRepo(t, c.store, "OTHR", otherProject)
	registryLocal := filepath.Join(tdir, "registry-clone")
	// Bootstrap the bare remote with sync content first (via init from
	// a third project in an ephemeral store).
	seedProject := initProjectRepo(t, tdir, "seed-project")
	seedStore, err := store.Open(filepath.Join(tdir, "seed.sqlite"))
	if err != nil {
		t.Fatalf("open seed store: %v", err)
	}
	defer seedStore.Close()
	if _, err := seedStore.CreateRepo("SEED", "seed", seedProject, ""); err != nil {
		t.Fatalf("seed repo: %v", err)
	}
	seedEng := &bsync.Engine{Store: seedStore, Actor: "seed"}
	if _, err := seedEng.InitSyncRepo(context.Background(), seedProject, bsync.InitOptions{
		LocalPath: filepath.Join(tdir, "seed-sync"),
		Remote:    bare,
	}); err != nil {
		t.Fatalf("seed init: %v", err)
	}
	// Now use clone to populate the registry row + on-disk clone for OTHR.
	if _, err := c.SetupSync(context.Background(), otherRepo, inputs.SyncSetupInput{
		Mode:      "clone",
		Remote:    bare,
		LocalPath: registryLocal,
	}); err != nil {
		t.Fatalf("seed clone: %v", err)
	}

	// Now attach a new project to the same registry row.
	project := initProjectRepo(t, tdir, "project-attach")
	repo := seedRepo(t, c.store, "MINI", project)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := c.SetupSync(ctx, repo, inputs.SyncSetupInput{
		Mode:   "attach",
		Remote: bare,
	})
	if err != nil {
		t.Fatalf("SetupSync(attach): %v", err)
	}
	if res.Mode != "attach" || res.Clone == nil {
		t.Fatalf("Mode=%q Clone=%+v, want attach / non-nil", res.Mode, res.Clone)
	}
	if res.Clone.LocalPath != registryLocal {
		t.Errorf("LocalPath=%q want %q (registry's path)", res.Clone.LocalPath, registryLocal)
	}
}

// ---------- remote impl ----------

// TestRemoteSetupSync_OK: a 200 response decodes the init result.
func TestRemoteSetupSync_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("Method=%s", r.Method)
		}
		if r.URL.Path != "/repos/MINI/sync/setup" {
			t.Errorf("Path=%s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(SyncSetupResult{
			Mode: "init",
			Init: &bsync.InitResult{LocalPath: "/srv/sync", Remote: "git@e.com:x.git", Pushed: true},
		})
	}))
	defer srv.Close()
	c, err := newRemoteFromURL(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.SetupSync(context.Background(), &model.Repo{Prefix: "MINI"}, inputs.SyncSetupInput{Mode: "init", LocalPath: "/tmp/x"})
	if err != nil {
		t.Fatalf("SetupSync: %v", err)
	}
	if res.Mode != "init" || res.Init == nil || res.Init.LocalPath != "/srv/sync" {
		t.Errorf("got %+v / Init=%+v", res, res.Init)
	}
}

// TestRemoteSetupSync_409Collision: a 409 response decodes the
// preview_collisions body and returns ErrSetupCollision.
func TestRemoteSetupSync_409Collision(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(SyncSetupResult{
			Mode: "clone",
			PreviewCollisions: &bsync.CollisionPreview{
				Renumbered: []bsync.RenumberEntry{
					{Prefix: "MINI", UUID: "u-1", OldNumber: 1, NewNumber: 3},
				},
			},
		})
	}))
	defer srv.Close()
	c, err := newRemoteFromURL(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.SetupSync(context.Background(), &model.Repo{Prefix: "MINI"}, inputs.SyncSetupInput{Mode: "clone", Remote: "x"})
	if !errors.Is(err, ErrSetupCollision) {
		t.Fatalf("err = %v, want ErrSetupCollision", err)
	}
	if res == nil || res.PreviewCollisions == nil {
		t.Fatalf("expected populated PreviewCollisions, got %+v", res)
	}
	if got := res.PreviewCollisions.Renumbered[0].NewNumber; got != 3 {
		t.Errorf("renumber NewNumber=%d want 3", got)
	}
}

// TestRemoteSetupSync_4xxOtherEnvelope: a 400 with the standard
// {error, code, details} envelope surfaces as an HTTPError with the
// message preserved.
func TestRemoteSetupSync_4xxOtherEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": `mode must be one of "init", "clone", "attach"`,
			"code":  "invalid_input",
		})
	}))
	defer srv.Close()
	c, err := newRemoteFromURL(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.SetupSync(context.Background(), &model.Repo{Prefix: "MINI"}, inputs.SyncSetupInput{Mode: "yolo"})
	if err == nil {
		t.Fatal("expected error on 400")
	}
	var he *HTTPError
	if !errors.As(err, &he) {
		t.Fatalf("err is not HTTPError: %T %v", err, err)
	}
	if he.Status != http.StatusBadRequest {
		t.Errorf("Status=%d", he.Status)
	}
	if !strings.Contains(he.Message, "init") {
		t.Errorf("Message=%q", he.Message)
	}
}

// newRemoteFromURL builds a minimal *remoteClient pointed at srv.URL —
// keeps the tests above from each repeating the URL parse / client
// construction dance.
func newRemoteFromURL(raw string) (*remoteClient, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	return &remoteClient{
		httpClient: &http.Client{Timeout: 5 * time.Second},
		base:       u,
		actor:      "tester",
	}, nil
}
