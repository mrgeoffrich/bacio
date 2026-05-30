package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// TestBoardArchiveKeybinds — BACI-68 TUI keybinds. `a` on a terminal
// issue archives immediately; `a` on a non-terminal issue arms a
// confirm prompt (no write until `y`); `A` unarchives. Each path
// records the issue.archive / issue.unarchive audit row TUIs are
// expected to write.
func TestBoardArchiveKeybinds(t *testing.T) {
	s, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	repo, err := s.CreateRepo("MINI", "mini", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	doneIss, err := s.CreateIssue(repo.ID, nil, "done one", "", model.StateDone, nil, "")
	if err != nil {
		t.Fatalf("create done iss: %v", err)
	}
	inProg, err := s.CreateIssue(repo.ID, nil, "active", "", model.StateInReview, nil, "")
	if err != nil {
		t.Fatalf("create inprog iss: %v", err)
	}

	board, err := newBoardView(s, repo, "tui")
	if err != nil {
		t.Fatalf("new board: %v", err)
	}
	if err := focusIssue(board, doneIss.Key); err != nil {
		t.Fatalf("focus done: %v", err)
	}

	// Terminal-state archive — no confirm.
	board.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	got, err := s.GetIssueByID(doneIss.ID)
	if err != nil {
		t.Fatalf("get done iss: %v", err)
	}
	if got.ArchivedAt == nil {
		t.Fatal("done issue should be archived after `a` (no confirm)")
	}
	if !hasHistoryOp(t, s, "issue.archive") {
		t.Fatal("expected an issue.archive audit row")
	}

	// Re-reading after reload: the archived card is gone from the
	// default view, so refocus the still-visible in_progress one.
	if err := focusIssue(board, inProg.Key); err != nil {
		t.Fatalf("focus inprog: %v", err)
	}
	// Non-terminal archive — first `a` arms the prompt, no write.
	board.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if !board.confirmArchive {
		t.Fatal("expected confirmArchive to be armed after `a` on in_progress")
	}
	gotInProg, _ := s.GetIssueByID(inProg.ID)
	if gotInProg.ArchivedAt != nil {
		t.Fatal("in_progress issue should NOT be archived on first `a` — confirm prompt")
	}
	// `y` confirms.
	board.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if board.confirmArchive {
		t.Fatal("confirmArchive should be cleared after y")
	}
	gotInProg, _ = s.GetIssueByID(inProg.ID)
	if gotInProg.ArchivedAt == nil {
		t.Fatal("in_progress issue should be archived after `y`")
	}

	// Toggle the global setting so the archived row is visible,
	// reload, and unarchive via `A`.
	if err := s.SetDisplayShowArchived(true); err != nil {
		t.Fatalf("set show archived: %v", err)
	}
	if err := board.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if err := focusIssue(board, inProg.Key); err != nil {
		t.Fatalf("refocus archived: %v", err)
	}
	board.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("A")})
	gotInProg, _ = s.GetIssueByID(inProg.ID)
	if gotInProg.ArchivedAt != nil {
		t.Fatal("expected `A` to unarchive the issue")
	}
	if !hasHistoryOp(t, s, "issue.unarchive") {
		t.Fatal("expected an issue.unarchive audit row")
	}
}

// TestBoardConfirmArchiveCancelledByOtherKey — a stray key after the
// confirm prompt is armed must cancel the pending archive without a
// write.
func TestBoardConfirmArchiveCancelledByOtherKey(t *testing.T) {
	s, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()
	repo, err := s.CreateRepo("MINI", "mini", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	iss, err := s.CreateIssue(repo.ID, nil, "active", "", model.StateInReview, nil, "")
	if err != nil {
		t.Fatalf("create iss: %v", err)
	}
	board, err := newBoardView(s, repo, "tui")
	if err != nil {
		t.Fatalf("new board: %v", err)
	}
	if err := focusIssue(board, iss.Key); err != nil {
		t.Fatalf("focus: %v", err)
	}
	board.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if !board.confirmArchive {
		t.Fatal("expected confirm armed")
	}
	// A non-y/n key cancels.
	board.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	if board.confirmArchive {
		t.Fatal("expected confirmArchive cleared by stray key")
	}
	got, _ := s.GetIssueByID(iss.ID)
	if got.ArchivedAt != nil {
		t.Fatal("no archive write should have happened")
	}
}
