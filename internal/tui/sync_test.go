//go:build !js

package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
	bsync "github.com/mrgeoffrich/bacio/internal/sync"
)

// syncTestRepo spins up an in-memory store with one repo, the fixture
// every sync-tab test builds on. Mirrors settingsTestRepo.
func syncTestRepo(t *testing.T) (*store.Store, *model.Repo) {
	t.Helper()
	s, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	repo, err := s.CreateRepo("MINI", "mini", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	return s, repo
}

// TestSyncViewEmptyRegistry: a fresh store renders the title bar, the
// toggle row, both section headers and an "unsynced projects" entry
// for the lone repo (which has no .bacio/config.yaml).
func TestSyncViewEmptyRegistry(t *testing.T) {
	s, repo := syncTestRepo(t)
	v := newSyncView(s, repo, "tester")

	if v.err != nil {
		t.Fatalf("unexpected reload error: %v", v.err)
	}
	if v.registry == nil {
		t.Fatal("expected a non-nil registry after reload")
	}
	if len(v.registry.SyncRepos) != 0 {
		t.Fatalf("SyncRepos len = %d, want 0", len(v.registry.SyncRepos))
	}
	if len(v.registry.UnsyncedProjects) != 1 {
		t.Fatalf("UnsyncedProjects len = %d, want 1", len(v.registry.UnsyncedProjects))
	}
	if v.registry.UnsyncedProjects[0].Prefix != repo.Prefix {
		t.Errorf("UnsyncedProjects[0].Prefix = %q, want %q",
			v.registry.UnsyncedProjects[0].Prefix, repo.Prefix)
	}

	out := v.View(140, 50)
	for _, want := range []string{"Sync", "Background sync", "Sync repositories", "Unsynced projects", repo.Prefix} {
		if !strings.Contains(out, want) {
			t.Errorf("View() missing %q\nfull output:\n%s", want, out)
		}
	}
}

// TestSyncViewWithRemoteRendersMembers: seed a sync_remotes row with a
// matching on-disk clone carrying the local repo's prefix; the view
// renders the URL-derived label and the linked member row.
func TestSyncViewWithRemoteRendersMembers(t *testing.T) {
	s, repo := syncTestRepo(t)

	// Lay out a sync clone with a MINI/ prefix directory so DiscoverMembership finds it.
	clone := t.TempDir()
	if err := mkdirAll(t, clone, "repos", repo.Prefix); err != nil {
		t.Fatalf("mkdir clone layout: %v", err)
	}
	remote := "git@example.com:team/billing-sync.git"
	if err := s.UpsertSyncRemote(remote, clone); err != nil {
		t.Fatalf("UpsertSyncRemote: %v", err)
	}

	v := newSyncView(s, repo, "tester")
	if len(v.registry.SyncRepos) != 1 {
		t.Fatalf("SyncRepos len = %d, want 1", len(v.registry.SyncRepos))
	}
	entry := v.registry.SyncRepos[0]
	if entry.Label != "billing-sync" {
		t.Errorf("Label = %q, want %q", entry.Label, "billing-sync")
	}
	if len(entry.Members) != 1 || entry.Members[0].Status != bsync.StatusLinked {
		t.Fatalf("Members = %+v, want one linked member", entry.Members)
	}

	out := v.View(140, 50)
	for _, want := range []string{"billing-sync", repo.Prefix, "linked"} {
		if !strings.Contains(out, want) {
			t.Errorf("View() missing %q\nfull output:\n%s", want, out)
		}
	}
}

// TestSyncViewToggleBackgroundSync: pressing `t` flips the
// sync.background_enabled flag, persists it, and writes the same
// sync_pref.update audit row the HTTP handler produces.
func TestSyncViewToggleBackgroundSync(t *testing.T) {
	s, repo := syncTestRepo(t)
	v := newSyncView(s, repo, "tester")

	// Default is true (opt-out). Toggle to false.
	if !v.backgroundEnabled {
		t.Fatal("precondition: background_enabled should default to true")
	}
	v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	if v.backgroundEnabled {
		t.Fatal("t should have flipped background_enabled to false")
	}
	got, err := s.GetSyncBackgroundEnabled()
	if err != nil {
		t.Fatalf("GetSyncBackgroundEnabled: %v", err)
	}
	if got {
		t.Fatal("store should reflect background_enabled=false after t")
	}
	rows, err := s.ListHistory(store.HistoryFilter{Op: "sync_pref.update", Limit: 5})
	if err != nil {
		t.Fatalf("list history: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected one sync_pref.update row, got %d", len(rows))
	}
	if rows[0].Kind != "app_setting" {
		t.Errorf("Kind = %q, want app_setting", rows[0].Kind)
	}
	if rows[0].TargetLabel != "sync.background_enabled" {
		t.Errorf("TargetLabel = %q, want sync.background_enabled", rows[0].TargetLabel)
	}
	if rows[0].Details != "background_enabled=false" {
		t.Errorf("Details = %q, want background_enabled=false", rows[0].Details)
	}

	// Toggle back to confirm the flip is symmetric.
	v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	if !v.backgroundEnabled {
		t.Fatal("second t should have flipped back to true")
	}
}

// TestSyncViewRefreshTickReArms: a syncTabRefreshTickMsg makes the
// view call reload() and return a non-nil Cmd that re-arms the next
// tick — the pattern documented in tui-cookbook.md §"Tickers".
// (We avoid executing the returned Cmd: tea.Tick blocks for the full
// interval, which would slow the test suite by 30 s per run.)
func TestSyncViewRefreshTickReArms(t *testing.T) {
	s, repo := syncTestRepo(t)
	v := newSyncView(s, repo, "tester")

	cmd := v.Update(syncTabRefreshTickMsg{})
	if cmd == nil {
		t.Fatal("expected the tick handler to return a Cmd re-arming the next tick")
	}
	// Init's first tick is the same shape, so the surface we're guarding
	// is just "is there a re-arm". Checking the Cmd is non-nil is enough
	// — the tick's wire shape is exercised by the actual cadence in
	// production, and asserting type-equality without running the tick
	// isn't reachable through the Cmd's func() signature.
}

// TestSyncViewCursorBounded: j/k must stay within [0, len(rows)-1] so a
// stray keypress can't index past the slice.
func TestSyncViewCursorBounded(t *testing.T) {
	s, repo := syncTestRepo(t)
	v := newSyncView(s, repo, "tester")
	rowCount := len(v.rows)
	if rowCount == 0 {
		t.Fatal("expected at least the toggle row in the row list")
	}

	// Walk past the bottom.
	for i := 0; i < rowCount+5; i++ {
		v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	}
	if v.cursor != rowCount-1 {
		t.Errorf("cursor = %d after j x %d, want %d", v.cursor, rowCount+5, rowCount-1)
	}
	// G should land on the last row.
	v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
	if v.cursor != rowCount-1 {
		t.Errorf("cursor = %d after G, want %d", v.cursor, rowCount-1)
	}
	// g should land on row 0 (the toggle).
	v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	if v.cursor != 0 {
		t.Errorf("cursor = %d after g, want 0", v.cursor)
	}
	// k from row 0 stays at row 0.
	v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	if v.cursor != 0 {
		t.Errorf("cursor = %d after k from 0, want 0", v.cursor)
	}
}

// TestSyncViewTabPosition: the shell inserts Sync at index 5 — between
// History and Settings. BACI-287 appended a Notifications tab after
// Settings (kept last so the snapshot tool's pinned Sync=5 / Settings=6
// indices stay stable), bringing the total to 8.
func TestSyncViewTabPosition(t *testing.T) {
	s, repo := syncTestRepo(t)
	m, err := NewModel(s, repo, nil, "", nil)
	if err != nil {
		t.Fatalf("NewModel: %v", err)
	}
	if len(m.tabs) != 8 {
		t.Fatalf("expected 8 tabs, got %d", len(m.tabs))
	}
	if m.tabs[5].name != "Sync" {
		t.Errorf("tabs[5].name = %q, want Sync", m.tabs[5].name)
	}
	if m.tabs[6].name != "Settings" {
		t.Errorf("tabs[6].name = %q, want Settings", m.tabs[6].name)
	}
	if m.tabs[7].name != "Notifications" {
		t.Errorf("tabs[7].name = %q, want Notifications (last tab)", m.tabs[7].name)
	}
}

// mkdirAll is a tiny test helper to build a nested prefix directory
// under a sync clone root.
func mkdirAll(t *testing.T, root string, parts ...string) error {
	t.Helper()
	return os.MkdirAll(filepath.Join(append([]string{root}, parts...)...), 0o755)
}
