//go:build !js

package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// composeTestBoard sets up an in-memory store + repo + board, registers
// a dummy agent so the BACI-168 compose flow's scope dispatch lands in
// the agent_dispatches table without immediately being bound off the
// queue. Returns the store, repo, and a focused board.
func composeTestBoard(t *testing.T) (*store.Store, *model.Repo, *boardView) {
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
	board, err := newBoardView(s, repo, "tui-test")
	if err != nil {
		t.Fatalf("new board: %v", err)
	}
	return s, repo, board
}

// typeRunes simulates a user typing the given string into whatever
// component holds focus (the compose textarea in these tests). Each
// rune is delivered as its own tea.KeyMsg, matching how the runtime
// emits keystrokes.
func typeRunes(b *boardView, s string) {
	for _, r := range s {
		b.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

// TestBoardCompose_HappyPath is the headline regression: N opens the
// composer, the typed text is captured in the textarea, ctrl+s creates
// a new issue + queues a scope dispatch, the overlay closes, and the
// cursor lands on the new card.
func TestBoardCompose_HappyPath(t *testing.T) {
	s, repo, board := composeTestBoard(t)

	// Open the composer.
	board.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("N")})
	if board.composeOverlay == nil {
		t.Fatal("expected N to open the compose overlay")
	}
	if !board.CapturesInput() {
		t.Fatal("expected CapturesInput=true while the composer is open")
	}
	if !board.HasOverlay() {
		t.Fatal("expected HasOverlay=true while the composer is open")
	}

	const desc = "the new card overlay flickers when switching tabs"
	typeRunes(board, desc)
	if got := board.composeOverlay.ta.Value(); got != desc {
		t.Fatalf("textarea value = %q, want %q", got, desc)
	}

	// Submit.
	board.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	if board.composeOverlay != nil {
		t.Fatal("expected composer to close after ctrl+s")
	}
	if board.err != nil {
		t.Fatalf("unexpected board.err after submit: %v", board.err)
	}

	// One issue should exist with the typed description and a
	// synthesised title (the first line, truncated if needed).
	issues, err := s.ListIssues(store.IssueFilter{RepoID: &repo.ID})
	if err != nil {
		t.Fatalf("list issues: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("expected 1 issue, got %d", len(issues))
	}
	full, err := s.GetIssueByID(issues[0].ID)
	if err != nil {
		t.Fatalf("get issue: %v", err)
	}
	if full.Description != desc {
		t.Errorf("description = %q, want %q", full.Description, desc)
	}
	if full.Title != titleFromDescription(desc) {
		t.Errorf("title = %q, want %q", full.Title, titleFromDescription(desc))
	}
	if full.State != model.StateTodo {
		t.Errorf("state = %s, want %s", full.State, model.StateTodo)
	}

	// One scope dispatch should be queued against the new issue.
	disps, err := s.ListDispatches(store.DispatchFilter{RepoID: &repo.ID})
	if err != nil {
		t.Fatalf("list dispatches: %v", err)
	}
	if len(disps) != 1 {
		t.Fatalf("expected 1 dispatch, got %d", len(disps))
	}
	if string(disps[0].Mode) != "scope" {
		t.Errorf("dispatch mode = %q, want scope", disps[0].Mode)
	}
	if disps[0].IssueID == nil || *disps[0].IssueID != full.ID {
		t.Errorf("dispatch issue_id mismatch")
	}

	// Cursor should land on the new card after the reload.
	if board.selected == nil || board.selected.ID != full.ID {
		t.Errorf("expected cursor on new card %s, got %v", full.Key, board.selected)
	}
}

// TestBoardCompose_EscCancels verifies that esc closes the overlay
// without writing — the user's draft is discarded, the issues table
// stays empty, no dispatch row is queued.
func TestBoardCompose_EscCancels(t *testing.T) {
	s, repo, board := composeTestBoard(t)

	board.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("N")})
	if board.composeOverlay == nil {
		t.Fatal("expected N to open the compose overlay")
	}
	typeRunes(board, "draft text that should be discarded")
	board.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if board.composeOverlay != nil {
		t.Fatal("expected esc to close the composer")
	}

	issues, err := s.ListIssues(store.IssueFilter{RepoID: &repo.ID})
	if err != nil {
		t.Fatalf("list issues: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("expected 0 issues after esc-cancel, got %d", len(issues))
	}
	disps, err := s.ListDispatches(store.DispatchFilter{RepoID: &repo.ID})
	if err != nil {
		t.Fatalf("list dispatches: %v", err)
	}
	if len(disps) != 0 {
		t.Fatalf("expected 0 dispatches after esc-cancel, got %d", len(disps))
	}
}

// TestBoardCompose_EmptyDescriptionNoop locks in the "empty submit is a
// quiet no-op" rule: hitting ctrl+s with only whitespace in the textarea
// leaves the overlay open and writes nothing.
func TestBoardCompose_EmptyDescriptionNoop(t *testing.T) {
	s, repo, board := composeTestBoard(t)

	board.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("N")})
	if board.composeOverlay == nil {
		t.Fatal("expected N to open the compose overlay")
	}
	// Only whitespace.
	typeRunes(board, "   ")
	board.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	if board.composeOverlay == nil {
		t.Fatal("empty submit should leave the composer open")
	}
	if board.composeOverlay.err != nil {
		t.Fatalf("empty submit should not surface an error, got %v", board.composeOverlay.err)
	}

	issues, err := s.ListIssues(store.IssueFilter{RepoID: &repo.ID})
	if err != nil {
		t.Fatalf("list issues: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("expected 0 issues after empty submit, got %d", len(issues))
	}
}

// TestBoardCompose_CapturesInput is the cookbook-contract regression:
// while the composer is open, `q` is typed into the textarea instead of
// quitting the program. Once the overlay closes, `q` quits again
// through the shell.
func TestBoardCompose_CapturesInput(t *testing.T) {
	s, repo := func() (*store.Store, *model.Repo) {
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
	}()

	m, err := NewModel(s, repo, nil, "", nil)
	if err != nil {
		t.Fatalf("new model: %v", err)
	}
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})

	// Board is tab 0 by default; open the composer with `N`.
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("N")})
	board := m.tabs[0].v.(*boardView)
	if board.composeOverlay == nil {
		t.Fatal("expected N (via the shell) to open the composer")
	}
	if !board.CapturesInput() {
		t.Fatal("expected CapturesInput=true while the composer is open")
	}

	// `q` through the shell must NOT quit while the composer captures
	// input — it must land in the textarea as a literal keystroke.
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd != nil {
		if _, isQuit := cmd().(tea.QuitMsg); isQuit {
			t.Fatal("`q` quit the program while the composer was open")
		}
	}
	if !strings.Contains(board.composeOverlay.ta.Value(), "q") {
		t.Fatalf("expected `q` in textarea, got %q", board.composeOverlay.ta.Value())
	}

	// esc to close.
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if board.composeOverlay != nil {
		t.Fatal("expected esc to close the composer")
	}
	if board.CapturesInput() {
		t.Fatal("expected CapturesInput=false once the composer closes")
	}
	// Now `q` should quit the program.
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd == nil {
		t.Fatal("expected `q` to quit once the composer is closed")
	}
	if _, isQuit := cmd().(tea.QuitMsg); !isQuit {
		t.Fatal("expected a tea.QuitMsg from `q` once the composer is closed")
	}
}

// TestBoardCompose_NDoesntOpenDuringArchiveConfirm guards the BACI-168
// disambiguation rule: when an archive confirm is armed (after `a` on
// a non-terminal card), capital `N` must not open the composer
// underneath the half-armed confirm. The confirm itself clears (the
// catch-all guard treats N as a stray key now), but no composer opens.
func TestBoardCompose_NDoesntOpenDuringArchiveConfirm(t *testing.T) {
	s, repo, board := composeTestBoard(t)

	// Create an in_progress issue (non-terminal — `a` arms the confirm).
	iss, err := s.CreateIssue(repo.ID, nil, "active", "", model.StateInProgress, nil)
	if err != nil {
		t.Fatalf("create iss: %v", err)
	}
	if err := board.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if err := focusIssue(board, iss.Key); err != nil {
		t.Fatalf("focus: %v", err)
	}

	board.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if !board.confirmArchive {
		t.Fatal("expected `a` to arm the archive confirm")
	}
	board.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("N")})
	if board.composeOverlay != nil {
		t.Fatal("N during an armed archive confirm should NOT open the composer")
	}
	if board.confirmArchive {
		t.Fatal("expected `N` to clear the pending archive confirm")
	}
}

// TestTitleFromDescription locks in the BACI-168 title synthesis
// helper: first non-empty line, trimmed, capped at composeTitleMaxCells
// cells. Empty / whitespace-only input falls back to the literal
// "(scope)" so the downstream CreateIssue "title is required" check
// can't trip.
func TestTitleFromDescription(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "hello world", "hello world"},
		{"trimmed", "   hello   ", "hello"},
		{"first-line", "hello\nworld", "hello"},
		{"leading-newlines", "\n\nhello", "hello"},
		{"empty", "", "(scope)"},
		{"whitespace-only", "   \n\t  ", "(scope)"},
		{
			"under-cell-cap",
			strings.Repeat("a", composeTitleMaxCells),
			strings.Repeat("a", composeTitleMaxCells),
		},
		{
			"over-cell-cap",
			strings.Repeat("a", composeTitleMaxCells+10),
			strings.Repeat("a", composeTitleMaxCells),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := titleFromDescription(tc.in)
			if got != tc.want {
				t.Fatalf("titleFromDescription(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestTruncateCells covers the cell-aware helper directly so the
// CJK / emoji branch has explicit coverage. lipgloss.Width counts CJK
// as 2 cells/rune and most emoji as 2 cells/rune; the truncator must
// stop short rather than emitting a partial wide glyph.
func TestTruncateCells(t *testing.T) {
	// 30 CJK characters at 2 cells each = 60 cells.
	cjk := strings.Repeat("漢", 30)
	if got := truncateCells(cjk, 60); got != cjk {
		t.Fatalf("60-cell CJK input should pass through unchanged, got len=%d", len([]rune(got)))
	}
	// 31 CJK characters = 62 cells; cap at 60 should drop the last one.
	cjkOver := strings.Repeat("漢", 31)
	got := truncateCells(cjkOver, 60)
	if r := []rune(got); len(r) != 30 {
		t.Fatalf("expected truncation to 30 runes (60 cells), got %d runes", len(r))
	}
	// Empty / zero cap.
	if got := truncateCells("anything", 0); got != "" {
		t.Fatalf("zero cap should return empty, got %q", got)
	}
	if got := truncateCells("", 10); got != "" {
		t.Fatalf("empty input should stay empty, got %q", got)
	}
}
