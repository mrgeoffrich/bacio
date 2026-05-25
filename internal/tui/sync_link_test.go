//go:build !js

package tui

// Tests for the BACI-112 phantom-link overlay on the sync view —
// keybinding gating, CapturesInput flip, error-on-empty-path, and
// render verification.

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
	bsync "github.com/mrgeoffrich/bacio/internal/sync"
)

// syncTestRepoWithPhantom seeds a store carrying a sync_remotes row,
// an on-disk sync clone with a phantom-prefix folder, and a phantom
// repos row — the fixture every BACI-112 overlay test needs.
//
// The phantom prefix is intentionally different from the "real" repo
// prefix the syncView is opened against so the cursor lands on a
// member row whose Status is Phantom (not Linked).
func syncTestRepoWithPhantom(t *testing.T) (*store.Store, *model.Repo) {
	t.Helper()
	s, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	// The view's "current repo" — a real repo, not the phantom.
	current, err := s.CreateRepo("HOME", "home", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create current repo: %v", err)
	}
	// Sync clone with both HOME (linked) and GUEST (phantom).
	clone := t.TempDir()
	if err := mkdirAll(t, clone, "repos", "HOME"); err != nil {
		t.Fatalf("mkdir HOME folder: %v", err)
	}
	if err := mkdirAll(t, clone, "repos", "GUEST"); err != nil {
		t.Fatalf("mkdir GUEST folder: %v", err)
	}
	if err := s.UpsertSyncRemote("git@example.com:team/shared.git", clone); err != nil {
		t.Fatalf("UpsertSyncRemote: %v", err)
	}
	// Phantom row for GUEST.
	if _, err := s.CreatePhantomRepo("uuid-GUEST", "GUEST", "guest", "git@example.com:user/guest.git"); err != nil {
		t.Fatalf("CreatePhantomRepo: %v", err)
	}
	return s, current
}

// findPhantomRowIdx returns the index of the first syncRowRepoMember
// row whose member is a phantom — the row the `l` keybinding gates on.
func findPhantomRowIdx(t *testing.T, v *syncView) int {
	t.Helper()
	for i, r := range v.rows {
		if r.Kind == syncRowRepoMember && r.Member != nil && r.Member.Status == bsync.StatusPhantom {
			return i
		}
	}
	t.Fatal("no phantom member row in v.rows")
	return -1
}

// TestSyncLinkOverlayOpensOnPhantomRow: pressing `l` on a phantom row
// opens the overlay; CapturesInput and HasOverlay flip true; the
// help line switches to the overlay's terse hint.
func TestSyncLinkOverlayOpensOnPhantomRow(t *testing.T) {
	s, repo := syncTestRepoWithPhantom(t)
	v := newSyncView(s, repo, "tester")
	v.cursor = findPhantomRowIdx(t, v)

	if v.HasOverlay() {
		t.Fatal("precondition: HasOverlay should be false before pressing l")
	}
	v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	if !v.HasOverlay() {
		t.Fatal("HasOverlay() = false after pressing l on phantom row")
	}
	if !v.CapturesInput() {
		t.Fatal("CapturesInput() = false while overlay open")
	}
	if v.link == nil || v.link.phantom == nil || v.link.phantom.Prefix != "GUEST" {
		t.Errorf("link overlay phantom = %+v, want prefix=GUEST", v.link)
	}
	help := v.Help()
	if !strings.Contains(help, "esc cancel") {
		t.Errorf("Help() while overlay open = %q, want it to mention `esc cancel`", help)
	}
}

// TestSyncLinkOverlayIgnoredOnNonPhantomRow: pressing `l` on the
// toggle row (or any non-phantom row) is a no-op.
func TestSyncLinkOverlayIgnoredOnNonPhantomRow(t *testing.T) {
	s, repo := syncTestRepoWithPhantom(t)
	v := newSyncView(s, repo, "tester")
	v.cursor = 0 // toggle row

	v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	if v.HasOverlay() {
		t.Errorf("l on toggle row opened overlay, expected no-op")
	}
}

// TestSyncLinkOverlayEscCloses: pressing esc while the overlay is open
// closes it and the j/k navigation flow resumes.
func TestSyncLinkOverlayEscCloses(t *testing.T) {
	s, repo := syncTestRepoWithPhantom(t)
	v := newSyncView(s, repo, "tester")
	v.cursor = findPhantomRowIdx(t, v)
	v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	if !v.HasOverlay() {
		t.Fatal("precondition: overlay should be open after l")
	}
	v.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if v.HasOverlay() {
		t.Errorf("esc didn't close the overlay")
	}
}

// TestSyncLinkOverlayEmptyPathError: pressing enter with no path
// surfaces an inline "path is required" error on the overlay rather
// than calling the store.
func TestSyncLinkOverlayEmptyPathError(t *testing.T) {
	s, repo := syncTestRepoWithPhantom(t)
	v := newSyncView(s, repo, "tester")
	v.cursor = findPhantomRowIdx(t, v)
	v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})

	v.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if v.link == nil {
		t.Fatal("enter with empty path closed the overlay (expected to keep it open with err)")
	}
	if v.link.err == nil || !strings.Contains(v.link.err.Error(), "path is required") {
		t.Errorf("link.err = %v, want one containing 'path is required'", v.link.err)
	}
}

// TestSyncLinkOverlayHappyPath: type an absolute path to a real git
// working tree + press enter → overlay closes, the phantom is gone
// from the registry, the row list now shows the linked status.
func TestSyncLinkOverlayHappyPath(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not on PATH: %v", err)
	}
	s, repo := syncTestRepoWithPhantom(t)
	v := newSyncView(s, repo, "tester")
	v.cursor = findPhantomRowIdx(t, v)

	// Set up the target working tree.
	target := filepath.Join(t.TempDir(), "guest-checkout")
	runGitForTest(t, "", "init", "-b", "main", target)
	runGitForTest(t, target, "config", "user.email", "tester@example.invalid")
	runGitForTest(t, target, "config", "user.name", "tester")

	v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	if v.link == nil {
		t.Fatal("overlay didn't open")
	}
	// Type the path one character at a time via the textinput.
	v.link.pathIn.SetValue(target)
	v.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if v.link != nil {
		t.Fatalf("overlay still open after successful link (err=%v)", v.link.err)
	}
	// The GUEST row should no longer be a phantom in the reloaded
	// registry.
	for _, sr := range v.registry.SyncRepos {
		for _, m := range sr.Members {
			if m.Prefix == "GUEST" && m.Status == bsync.StatusPhantom {
				t.Errorf("GUEST still marked phantom after link: %+v", m)
			}
		}
	}
}

// runGitForTest shells out to git with the args; mirrors the per-file
// helper used in other tui tests. Inlined here because the sync_test.go
// fixture doesn't need git.
func runGitForTest(t *testing.T, cwd string, args ...string) {
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
