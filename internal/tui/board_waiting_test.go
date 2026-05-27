package tui

import (
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// TestBoardRendersWaitingLabel (BACI-145) — a waiting card in the TUI
// renders the inline "why is this card waiting?" label on the bottom
// row. The three kinds (no_agent, blocked, delivered) all surface via
// the same boardcards.WaitingStateLabel, so checking the no_agent
// flavour end-to-end is enough to lock in that reload() populates
// waitingStateByIssue and renderColumn writes the label out.
func TestBoardRendersWaitingLabel(t *testing.T) {
	s, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()
	repo, err := s.CreateRepo("MINI", "mini", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	iss, err := s.CreateIssue(repo.ID, nil, "first queued card", "", model.StateTodo, nil, "")
	if err != nil {
		t.Fatalf("create iss: %v", err)
	}
	// Mirror the dispatcher: a queued AddDispatch lands a queued row
	// in agent_dispatches against the linked issue; reload() surfaces
	// the card as waiting via WaitingDispatchForIssue (BACI-255 — the
	// row IS the signal, there's no denormalised cache to set).
	if _, err := s.AddDispatch(store.AddDispatchIn{
		RepoID:        repo.ID,
		IssueID:       &iss.ID,
		Mode:          model.DispatchModePlan,
		CreatedBy:     "supervisor",
		InitialStatus: model.DispatchQueued,
	}); err != nil {
		t.Fatalf("add dispatch: %v", err)
	}

	board, err := newBoardView(s, repo, "tui")
	if err != nil {
		t.Fatalf("new board: %v", err)
	}

	// Sanity: the deriver populated the per-issue state.
	if got, ok := board.waitingStateByIssue[iss.ID]; !ok || got == nil {
		t.Fatalf("waitingStateByIssue missing entry for %d", iss.ID)
	}

	// Render the board big enough that the Todo column fits the card +
	// its three-row body, then assert the label appears verbatim.
	out := board.View(160, 40)
	if !strings.Contains(out, "Waiting for an available agent") {
		t.Errorf("rendered board does not contain the no-agent waiting label\n%s", out)
	}
}
