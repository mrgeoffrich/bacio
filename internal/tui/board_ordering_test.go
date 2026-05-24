package tui

import (
	"testing"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// TestBoardColumnOrdering — BACI-101 + BACI-138. The Done and
// Cancelled columns render newest-completed first; non-completed
// columns keep their creation order.
//
// As of BACI-138 the sort key is the denormalised `terminal_at`
// column (stamped on every state transition into done/cancelled);
// `updated_at` is now only the second-tier fallback for rows whose
// terminal_at is NULL (a row CompletionSortKey is asked about
// without going through the store — see the fakeClient-driven tests
// in internal/boardcards). This test stamps terminal_at directly so
// the sort is keyed on the field the production read path uses.
func TestBoardColumnOrdering(t *testing.T) {
	s, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	repo, err := s.CreateRepo("MINI", "mini", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}

	// Seed issues in creation order. CreateIssue stamps terminal_at to
	// CURRENT_TIMESTAMP for rows that land directly in a terminal
	// state; we then overwrite it with explicit values via raw SQL so
	// the sort is deterministic — SQLite's 1-second granularity makes
	// relying on insertion timing flaky.
	type seed struct {
		title    string
		state    model.State
		terminal string // terminal_at to inject ("" → leave it alone)
		created  string // created_at to inject ("" → leave it alone)
	}
	seeds := []seed{
		{title: "todo a", state: model.StateTodo},
		{title: "todo b", state: model.StateTodo},
		{title: "done oldest", state: model.StateDone, terminal: "2026-05-05 09:00:00"},
		{title: "done newest", state: model.StateDone, terminal: "2026-05-20 09:00:00"},
		{title: "done middle", state: model.StateDone, terminal: "2026-05-12 09:00:00"},
		{title: "cancelled older", state: model.StateCancelled, terminal: "2026-05-03 09:00:00"},
		{title: "cancelled newest", state: model.StateCancelled, terminal: "2026-05-15 09:00:00"},
	}
	keysByTitle := map[string]string{}
	for _, sd := range seeds {
		iss, err := s.CreateIssue(repo.ID, nil, sd.title, "", sd.state, nil)
		if err != nil {
			t.Fatalf("create issue %q: %v", sd.title, err)
		}
		keysByTitle[sd.title] = iss.Key
		if sd.terminal != "" {
			if _, err := s.DB.Exec(
				`UPDATE issues SET terminal_at = ? WHERE id = ?`, sd.terminal, iss.ID,
			); err != nil {
				t.Fatalf("set terminal_at for %q: %v", sd.title, err)
			}
		}
		if sd.created != "" {
			if _, err := s.DB.Exec(
				`UPDATE issues SET created_at = ? WHERE id = ?`, sd.created, iss.ID,
			); err != nil {
				t.Fatalf("set created_at for %q: %v", sd.title, err)
			}
		}
	}

	board, err := newBoardView(s, repo, "tui")
	if err != nil {
		t.Fatalf("new board: %v", err)
	}
	if err := board.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}

	titles := func(st model.State) []string {
		var out []string
		for _, iss := range board.columns[st] {
			out = append(out, iss.Title)
		}
		return out
	}
	eq := func(name string, got, want []string) {
		if len(got) != len(want) {
			t.Fatalf("%s column = %v, want %v", name, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%s column = %v, want %v", name, got, want)
			}
		}
	}

	// Done: newest terminal_at first.
	eq("Done", titles(model.StateDone),
		[]string{"done newest", "done middle", "done oldest"})
	// Cancelled: May 15 beats May 3.
	eq("Cancelled", titles(model.StateCancelled),
		[]string{"cancelled newest", "cancelled older"})
	// Todo: untouched creation order.
	eq("Todo", titles(model.StateTodo), []string{"todo a", "todo b"})
}
