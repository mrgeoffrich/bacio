package client_test

// BACI-111 — cross-transport SetupSync tests. The local impl's
// validation surface (mode whitelist, per-mode required fields,
// phantom refusal) is tested directly; the engine-backed happy paths
// are exercised by the sync package's own bootstrap_test.go and don't
// need to be re-run here. The remote impl is tested against a small
// httptest stand-in that returns 200 / 409 / 400 so the (result,
// sentinel) shape is pinned end-to-end.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// newLocalOnDisk opens a temp-DB-backed local client so SetupSync can
// resolve a real <dbPath>.sync.lock — the in-memory store path doesn't
// give the lock helper a stable file location.
func newLocalOnDisk(t *testing.T) (client.Client, *store.Store, *model.Repo) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "db.sqlite")
	c, err := client.Open(context.Background(), client.Options{DBPath: dbPath, Actor: "tester"})
	if err != nil {
		t.Fatalf("client.Open: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	repo, err := s.CreateRepo("TEST", "test-repo", filepath.Join(dir, "project"), "")
	if err != nil {
		t.Fatalf("CreateRepo: %v", err)
	}
	return c, s, repo
}

func TestLocalSetupSync_PhantomRefused(t *testing.T) {
	c, s, _ := newLocalOnDisk(t)
	phantom, err := s.CreateRepo("PHAN", "phantom", "", "git@example.com:user/phantom.git")
	if err != nil {
		t.Fatalf("CreateRepo phantom: %v", err)
	}
	_, err = c.SetupSync(context.Background(), phantom, inputs.SyncSetupInput{
		Mode:      "init",
		LocalPath: "/tmp/whatever",
	})
	if err == nil {
		t.Fatalf("expected refusal on phantom, got nil")
	}
	if got := err.Error(); !contains(got, "phantom") {
		t.Fatalf("expected phantom error, got %q", got)
	}
}

func TestLocalSetupSync_ModeValidation(t *testing.T) {
	c, _, repo := newLocalOnDisk(t)
	cases := []struct {
		name   string
		in     inputs.SyncSetupInput
		errsub string
	}{
		{"bad mode", inputs.SyncSetupInput{Mode: "bogus"}, `mode must be one of`},
		{"init no localpath", inputs.SyncSetupInput{Mode: "init"}, `mode="init" requires local_path`},
		{"clone no remote", inputs.SyncSetupInput{Mode: "clone"}, `mode="clone" requires remote`},
		{"attach no remote", inputs.SyncSetupInput{Mode: "attach"}, `mode="attach" requires remote`},
		{"attach with localpath", inputs.SyncSetupInput{
			Mode:      "attach",
			Remote:    "git@example.com:x/y.git",
			LocalPath: "/tmp/x",
		}, `must not set local_path`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := c.SetupSync(context.Background(), repo, tc.in)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.errsub)
			}
			if got := err.Error(); !contains(got, tc.errsub) {
				t.Fatalf("expected %q substring, got %q", tc.errsub, got)
			}
		})
	}
}

func TestLocalSetupSync_AttachUnknownRemote(t *testing.T) {
	c, _, repo := newLocalOnDisk(t)
	_, err := c.SetupSync(context.Background(), repo, inputs.SyncSetupInput{
		Mode:   "attach",
		Remote: "git@example.com:unknown/sync.git",
	})
	if err == nil {
		t.Fatalf("expected ErrNotFound style error, got nil")
	}
	if got := err.Error(); !contains(got, "no sync_remotes registry entry") {
		t.Fatalf("expected registry-not-found error, got %q", got)
	}
}

// remoteFixture spins up an httptest.Server that responds to
// POST /repos/{prefix}/sync/setup with a canned status + body —
// enough to exercise the remote client's 200 / 409 / 400 branches
// without booting the full api server (which would drag in git).
type remoteFixture struct {
	t      *testing.T
	srv    *httptest.Server
	status int
	body   any
	calls  int
}

func newRemoteFixture(t *testing.T, status int, body any) *remoteFixture {
	t.Helper()
	rf := &remoteFixture{t: t, status: status, body: body}
	rf.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rf.calls++
		if r.Method != http.MethodPost {
			http.Error(w, "expected POST", http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path != "/repos/TEST/sync/setup" {
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(rf.status)
		_ = json.NewEncoder(w).Encode(rf.body)
	}))
	t.Cleanup(rf.srv.Close)
	return rf
}

func newRemote(t *testing.T, base string) client.Client {
	t.Helper()
	c, err := client.Open(context.Background(), client.Options{Remote: base, Actor: "tester"})
	if err != nil {
		t.Fatalf("client.Open(remote): %v", err)
	}
	return c
}

func TestRemoteSetupSync_OK(t *testing.T) {
	body := map[string]any{
		"mode": "init",
		"init": map[string]any{
			"local_path": "/tmp/sync",
			"remote":     "",
			"pushed":     false,
		},
	}
	rf := newRemoteFixture(t, http.StatusOK, body)
	c := newRemote(t, rf.srv.URL)
	repo := &model.Repo{Prefix: "TEST"}
	res, err := c.SetupSync(context.Background(), repo, inputs.SyncSetupInput{
		Mode:      "init",
		LocalPath: "/tmp/sync",
	})
	if err != nil {
		t.Fatalf("SetupSync: %v", err)
	}
	if res.Mode != "init" {
		t.Fatalf("Mode=%q want init", res.Mode)
	}
	if res.Init == nil || res.Init.LocalPath != "/tmp/sync" {
		t.Fatalf("Init not populated: %+v", res.Init)
	}
	if res.PreviewCollisions != nil {
		t.Fatalf("PreviewCollisions populated on 200: %+v", res.PreviewCollisions)
	}
}

func TestRemoteSetupSync_409CollisionReturnsSentinel(t *testing.T) {
	body := map[string]any{
		"mode": "clone",
		"preview_collisions": map[string]any{
			"renumbered": []map[string]any{
				{"prefix": "MINI", "uuid": "u1", "old_number": 1, "new_number": 3},
			},
			"renamed": []map[string]any{
				{"kind": "feature", "prefix": "MINI", "uuid": "u2", "old": "auth", "new": "auth-2"},
			},
		},
	}
	rf := newRemoteFixture(t, http.StatusConflict, body)
	c := newRemote(t, rf.srv.URL)
	repo := &model.Repo{Prefix: "TEST"}
	res, err := c.SetupSync(context.Background(), repo, inputs.SyncSetupInput{
		Mode:   "clone",
		Remote: "git@example.com:x/y.git",
	})
	if !errors.Is(err, client.ErrSetupCollision) {
		t.Fatalf("expected ErrSetupCollision, got %v", err)
	}
	if res == nil {
		t.Fatalf("expected populated result on collision, got nil")
	}
	if res.PreviewCollisions == nil {
		t.Fatalf("PreviewCollisions nil")
	}
	if len(res.PreviewCollisions.Renumbered) != 1 || res.PreviewCollisions.Renumbered[0].NewNumber != 3 {
		t.Fatalf("Renumbered wrong: %+v", res.PreviewCollisions.Renumbered)
	}
	if len(res.PreviewCollisions.Renamed) != 1 || res.PreviewCollisions.Renamed[0].New != "auth-2" {
		t.Fatalf("Renamed wrong: %+v", res.PreviewCollisions.Renamed)
	}
}

func TestRemoteSetupSync_400Validation(t *testing.T) {
	body := map[string]any{
		"error": "mode must be one of ...",
		"code":  "invalid_input",
	}
	rf := newRemoteFixture(t, http.StatusBadRequest, body)
	c := newRemote(t, rf.srv.URL)
	repo := &model.Repo{Prefix: "TEST"}
	_, err := c.SetupSync(context.Background(), repo, inputs.SyncSetupInput{
		Mode: "bogus",
	})
	if err == nil {
		t.Fatalf("expected 400 error, got nil")
	}
	if !contains(err.Error(), "mode must be one of") {
		t.Fatalf("expected envelope message, got %q", err.Error())
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
