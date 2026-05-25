//go:build !js

package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mrgeoffrich/bacio/internal/identity"
	"github.com/mrgeoffrich/bacio/internal/store"
	bsync "github.com/mrgeoffrich/bacio/internal/sync"
)

// seedPhantomFixture seeds the in-memory store with a sync_remotes row
// whose local clone carries one phantom prefix (PHTM), plus a real
// git working tree to bind to. Returns the absolute project path —
// the test types each rune of it into the overlay.
func seedPhantomFixture(t *testing.T, s *store.Store) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	clone := t.TempDir()
	if err := os.MkdirAll(filepath.Join(clone, "repos", "PHTM"), 0o755); err != nil {
		t.Fatalf("mkdir clone: %v", err)
	}
	if err := s.UpsertSyncRemote("git@example.com:team/sync.git", clone); err != nil {
		t.Fatalf("UpsertSyncRemote: %v", err)
	}
	if _, err := s.CreatePhantomRepo(identity.New(), "PHTM", "Phantom Project", ""); err != nil {
		t.Fatalf("CreatePhantomRepo: %v", err)
	}
	project := filepath.Join(t.TempDir(), "phantom-project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	cmd := exec.Command("git", "init", "--quiet", project)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	return project
}

// findCursorOnPhantom advances the cursor with j keypresses until it
// lands on a phantom syncRowRepoMember. Fails the test if no phantom
// row is reachable — the fixture should always seed one.
func findCursorOnPhantom(t *testing.T, v *syncView) {
	t.Helper()
	for i := 0; i < len(v.rows); i++ {
		r := v.rows[v.cursor]
		if r.Kind == syncRowRepoMember && r.Member != nil && r.Member.Status == bsync.StatusPhantom {
			return
		}
		v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	}
	t.Fatal("no phantom syncRowRepoMember reachable from cursor")
}

// TestSyncViewPhantomLinkOverlay: pressing `l` on a phantom row opens
// the overlay; CapturesInput flips true; typing fills the path; enter
// submits and the row transitions out of phantom.
func TestSyncViewPhantomLinkOverlay(t *testing.T) {
	s, repo := syncTestRepo(t)
	projectPath := seedPhantomFixture(t, s)

	v := newSyncView(s, repo, "tester")
	if v.CapturesInput() {
		t.Fatal("precondition: CapturesInput should be false before opening")
	}
	findCursorOnPhantom(t, v)

	// `l` opens the overlay.
	v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	if v.link == nil {
		t.Fatal("expected the overlay to open after `l` on a phantom row")
	}
	if !v.CapturesInput() {
		t.Fatal("CapturesInput should be true while the overlay is open")
	}
	// Type each rune of the path. Bubbletea ships runes one keypress
	// at a time in real life — pre-bundling them into one KeyMsg
	// would short-circuit the rune-append branch in updateLinkOverlay.
	for _, r := range projectPath {
		v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if v.link.path != projectPath {
		t.Fatalf("overlay path = %q; want %q", v.link.path, projectPath)
	}
	// Render snapshot carries the overlay header + the path.
	out := v.View(140, 30)
	if !strings.Contains(out, "Link PHTM") {
		t.Errorf("View() missing overlay header 'Link PHTM':\n%s", out)
	}
	if !strings.Contains(out, "enter submit") {
		t.Errorf("View() missing overlay footer hint:\n%s", out)
	}

	// Enter submits.
	v.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if v.link != nil {
		t.Fatalf("expected overlay to close on submit; got %+v", v.link)
	}
	if v.CapturesInput() {
		t.Fatal("CapturesInput should be false after submit")
	}
	// Row is no longer phantom.
	repoRow, err := s.GetRepoByPrefix("PHTM")
	if err != nil {
		t.Fatalf("GetRepoByPrefix: %v", err)
	}
	if repoRow.Path != projectPath {
		t.Fatalf("PHTM.path = %q after link; want %q", repoRow.Path, projectPath)
	}
}

// TestSyncViewPhantomLinkOverlayEscape: opening the overlay then
// pressing esc closes it without writing.
func TestSyncViewPhantomLinkOverlayEscape(t *testing.T) {
	s, repo := syncTestRepo(t)
	_ = seedPhantomFixture(t, s)
	v := newSyncView(s, repo, "tester")
	findCursorOnPhantom(t, v)
	v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	if v.link == nil {
		t.Fatal("expected overlay to open")
	}
	v.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if v.link != nil {
		t.Fatal("esc should have closed the overlay")
	}
	// Phantom row stays untouched.
	repoRow, err := s.GetRepoByPrefix("PHTM")
	if err != nil {
		t.Fatalf("GetRepoByPrefix: %v", err)
	}
	if repoRow.Path != "" {
		t.Fatalf("esc should not have linked the phantom; path = %q", repoRow.Path)
	}
}

// TestSyncViewLinkKeyNoopOnNonPhantom: pressing `l` on a non-phantom
// row (e.g. the toggle row at cursor 0) is a no-op — no overlay opens.
func TestSyncViewLinkKeyNoopOnNonPhantom(t *testing.T) {
	s, repo := syncTestRepo(t)
	_ = seedPhantomFixture(t, s)
	v := newSyncView(s, repo, "tester")
	// Cursor starts at 0 (the toggle row).
	v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	if v.link != nil {
		t.Fatalf("`l` on a non-phantom row should be a no-op; got %+v", v.link)
	}
}
