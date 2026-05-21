package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// TestEnterOnAttachmentOpensDoc covers BACI-13: pressing enter on an
// issue's attached document in the board's card overlay should open that
// document fullscreen in the Documents tab — not dump the user on the
// bare Documents list. The regression case is a document linked while
// the TUI is already running: the board refreshes its attachment list on
// navigation, but the Documents view only loads on construction, so a
// stale list made selectByFilename silently miss.
func TestEnterOnAttachmentOpensDoc(t *testing.T) {
	s, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	repo, err := s.CreateRepo("MINI", "mini", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	iss, err := s.CreateIssue(repo.ID, nil, "Issue with a doc", "", model.StateTodo, nil)
	if err != nil {
		t.Fatalf("create issue: %v", err)
	}

	// Build the model first — the Documents view loads an empty list.
	// nil elector: the test acts as leader, no election needed.
	m, err := NewModel(s, repo, nil, "")
	if err != nil {
		t.Fatalf("new model: %v", err)
	}
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})

	// Now link a document, mimicking a `bacio doc link` run in another
	// terminal while the TUI is open.
	doc, err := s.CreateDocument(repo.ID, "spec.md", model.DocTypeArchitecture, "# Spec\n\nbody", "")
	if err != nil {
		t.Fatalf("create doc: %v", err)
	}
	if _, err := s.LinkDocument(doc.ID, store.LinkTarget{IssueID: &iss.ID}, "why"); err != nil {
		t.Fatalf("link doc: %v", err)
	}

	// Focus the issue and force the board to re-pull its attachment list
	// (what a refresh tick or navigation does in real use).
	board := m.tabs[0].v.(*boardView)
	board.selected = nil
	if err := focusIssue(board, iss.Key); err != nil {
		t.Fatalf("focus issue: %v", err)
	}
	if len(board.docLinks) != 1 {
		t.Fatalf("expected 1 attachment on the board, got %d", len(board.docLinks))
	}

	// Open the card overlay and focus the attachments pane.
	board.overlay = true
	board.overlayFocus = paneAttachments

	// Enter on the attachment fires a command yielding an openDocMsg.
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected a command from enter on an attachment")
	}
	msg := cmd()
	if _, ok := msg.(openDocMsg); !ok {
		t.Fatalf("expected openDocMsg, got %T", msg)
	}
	m.Update(msg)

	docs := m.tabs[2].v.(*docsView)
	if m.active != 2 {
		t.Fatalf("expected Documents tab active, got tab %d", m.active)
	}
	if !docs.overlay {
		t.Fatal("expected the document overlay to be open after enter on the attachment")
	}
	if docs.loaded == nil || docs.loaded.Filename != "spec.md" {
		t.Fatalf("expected loaded doc spec.md, got %+v", docs.loaded)
	}
}
