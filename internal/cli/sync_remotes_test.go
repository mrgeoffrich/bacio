package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/identity"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// withTestStore wires up an on-disk test DB and points opts.dbPath at
// it for the duration of the test, returning the open store handle so
// callers can seed rows. The store is closed and opts.dbPath restored
// via t.Cleanup so newSyncRemotesCmd's openStore() picks up the same
// path. Mirrors install_agent_test.go's pattern.
func withTestStore(t *testing.T) *store.Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "db.sqlite")
	prevDB := opts.dbPath
	opts.dbPath = dbPath
	t.Cleanup(func() { opts.dbPath = prevDB })
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// runSyncRemotesJSON executes the `bacio sync remotes` command in JSON
// mode and decodes the payload. Captures stdout via os.Pipe — emit
// writes to os.Stdout directly, so we swap it for the duration of the
// command.
func runSyncRemotesJSON(t *testing.T) syncRemotesResult {
	t.Helper()
	prevOut := opts.output
	opts.output = outputJSON
	t.Cleanup(func() { opts.output = prevOut })

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	prevStdout := os.Stdout
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = prevStdout })

	cmd := newSyncRemotesCmd()
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		_ = w.Close()
		t.Fatalf("sync remotes: %v", err)
	}
	_ = w.Close()

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	var got syncRemotesResult
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("decode payload %q: %v", buf.String(), err)
	}
	return got
}

// TestSyncRemotesEmpty: an empty registry renders the explicit "(no
// sync remotes registered)" line in text mode and an empty (non-nil)
// `remotes` array in JSON.
func TestSyncRemotesEmpty(t *testing.T) {
	_ = withTestStore(t)

	// JSON path.
	got := runSyncRemotesJSON(t)
	if got.Remotes == nil {
		t.Fatal("Remotes = nil, want empty non-nil slice (json encoders should see `[]`, not `null`)")
	}
	if len(got.Remotes) != 0 {
		t.Fatalf("Remotes len = %d, want 0", len(got.Remotes))
	}

	// Text path.
	var buf bytes.Buffer
	printSyncRemotesResult(&buf, syncRemotesResult{Remotes: []syncRemoteEntry{}})
	if !strings.Contains(buf.String(), "(no sync remotes registered)") {
		t.Fatalf("text renderer empty case missing sentinel: %q", buf.String())
	}
}

// TestSyncRemotesMembershipMix seeds one registry row with a real
// on-disk clone carrying three prefix folders — a linked DB row, a
// phantom DB row, and an unknown prefix — and asserts the per-row
// projects come back with the three statuses correctly classified plus
// the derived label and clone_present:true.
func TestSyncRemotesMembershipMix(t *testing.T) {
	s := withTestStore(t)

	// Build a sync-repo-style local clone with three prefix folders.
	clone := t.TempDir()
	for _, p := range []string{"MINI", "PHTM", "UNKN"} {
		if err := os.MkdirAll(filepath.Join(clone, "repos", p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", p, err)
		}
	}

	const remoteURL = "git@github.com:alice/sync.git"
	if err := s.UpsertSyncRemote(remoteURL, clone); err != nil {
		t.Fatalf("UpsertSyncRemote: %v", err)
	}

	// Linked: a normal repo row (path != "").
	if _, err := s.CreateRepo("MINI", "Mini Project", "/tmp/mini", ""); err != nil {
		t.Fatalf("CreateRepo MINI: %v", err)
	}
	// Phantom: path == "".
	if _, err := s.CreatePhantomRepo(identity.New(), "PHTM", "Phantom Project", ""); err != nil {
		t.Fatalf("CreatePhantomRepo PHTM: %v", err)
	}
	// UNKN has no DB row → expected status `absent`.

	got := runSyncRemotesJSON(t)
	if len(got.Remotes) != 1 {
		t.Fatalf("Remotes len = %d, want 1", len(got.Remotes))
	}
	entry := got.Remotes[0]
	if entry.RemoteURL != remoteURL {
		t.Errorf("RemoteURL = %q, want %q", entry.RemoteURL, remoteURL)
	}
	if entry.Label != "sync" {
		t.Errorf("Label = %q, want %q", entry.Label, "sync")
	}
	if !entry.ClonePresent {
		t.Error("ClonePresent = false, want true")
	}
	if len(entry.Projects) != 3 {
		t.Fatalf("Projects len = %d, want 3 (%+v)", len(entry.Projects), entry.Projects)
	}
	// DiscoverMembership sorts by prefix ASC: MINI, PHTM, UNKN.
	wantStatus := map[string]string{"MINI": "linked", "PHTM": "phantom", "UNKN": "absent"}
	for _, p := range entry.Projects {
		if got, want := p.Status, wantStatus[p.Prefix]; got != want {
			t.Errorf("project %s status = %q, want %q", p.Prefix, got, want)
		}
		// repo_uuid populated for the two non-absent classifications.
		if p.Status == "absent" && p.RepoUUID != "" {
			t.Errorf("project %s absent but has repo_uuid=%q", p.Prefix, p.RepoUUID)
		}
		if p.Status != "absent" && p.RepoUUID == "" {
			t.Errorf("project %s status=%q but repo_uuid empty", p.Prefix, p.Status)
		}
	}
}

// TestSyncRemotesCloneMissing seeds a row whose local_path doesn't
// exist on disk. The result should carry clone_present:false, an empty
// projects slice, and the text renderer should call the clone out as
// "(missing)" while skipping the projects block.
func TestSyncRemotesCloneMissing(t *testing.T) {
	s := withTestStore(t)

	const remoteURL = "git@github.com:bob/gone.git"
	bogusPath := filepath.Join(t.TempDir(), "does-not-exist")
	if err := s.UpsertSyncRemote(remoteURL, bogusPath); err != nil {
		t.Fatalf("UpsertSyncRemote: %v", err)
	}

	got := runSyncRemotesJSON(t)
	if len(got.Remotes) != 1 {
		t.Fatalf("Remotes len = %d, want 1", len(got.Remotes))
	}
	entry := got.Remotes[0]
	if entry.ClonePresent {
		t.Error("ClonePresent = true, want false")
	}
	if len(entry.Projects) != 0 {
		t.Errorf("Projects len = %d, want 0 when clone is missing", len(entry.Projects))
	}

	// Text renderer signals (missing) and skips projects.
	var buf bytes.Buffer
	printSyncRemotesResult(&buf, got)
	out := buf.String()
	if !strings.Contains(out, "(missing)") {
		t.Errorf("text output missing (missing) marker: %q", out)
	}
	if strings.Contains(out, "projects:") {
		t.Errorf("text output unexpectedly includes projects block for missing clone: %q", out)
	}
}

// TestSyncRemotesLastSyncError: a row with a non-nil last_sync_error
// must surface in JSON (omitempty leaves last_sync_at off when nil) and
// the text renderer must print `error:` instead of `last sync:`.
func TestSyncRemotesLastSyncError(t *testing.T) {
	s := withTestStore(t)

	const remoteURL = "git@github.com:carol/sync.git"
	const errMsg = "git push: rejected"
	// Local path doesn't need to exist for this case — we're focused on
	// the last_sync_error column.
	bogusPath := filepath.Join(t.TempDir(), "absent")
	if err := s.UpsertSyncRemote(remoteURL, bogusPath); err != nil {
		t.Fatalf("UpsertSyncRemote: %v", err)
	}
	if err := s.MarkSyncFailed(remoteURL, errMsg); err != nil {
		t.Fatalf("MarkSyncFailed: %v", err)
	}

	got := runSyncRemotesJSON(t)
	if len(got.Remotes) != 1 {
		t.Fatalf("Remotes len = %d, want 1", len(got.Remotes))
	}
	entry := got.Remotes[0]
	if entry.LastSyncError == nil || *entry.LastSyncError != errMsg {
		t.Errorf("LastSyncError = %v, want %q", entry.LastSyncError, errMsg)
	}
	if entry.LastSyncAt != nil {
		t.Error("LastSyncAt should be nil — MarkSyncFailed doesn't stamp it")
	}

	var buf bytes.Buffer
	printSyncRemotesResult(&buf, got)
	out := buf.String()
	if !strings.Contains(out, "error:        "+errMsg) {
		t.Errorf("text output missing `error:` line: %q", out)
	}
	if strings.Contains(out, "last sync:") {
		t.Errorf("text output unexpectedly includes `last sync:` line alongside error: %q", out)
	}
}
