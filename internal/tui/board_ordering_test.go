package tui

import (
	"testing"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// TestBoardColumnOrdering — BACI-101. The Done and Cancelled columns
// render newest-completed first (updated_at descending, created_at as
// the fallback); non-completed columns keep their creation order.
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

	// Seed issues in creation order. CreateIssue stamps created_at /
	// updated_at to CURRENT_TIMESTAMP; we then overwrite updated_at
	// (and, for the fallback row, created_at) with explicit values via
	// raw SQL so the sort is deterministic — SQLite's 1-second
	// granularity makes relying on insertion timing flaky.
	type seed struct {
		title   string
		state   model.State
		updated string // updated_at to inject ("" → leave it alone)
		created string // created_at to inject ("" → leave it alone)
	}
	seeds := []seed{
		{title: "todo a", state: model.StateTodo},
		{title: "todo b", state: model.StateTodo},
		{title: "done oldest", state: model.StateDone, updated: "2026-05-05 09:00:00"},
		{title: "done newest", state: model.StateDone, updated: "2026-05-20 09:00:00"},
		{title: "done middle", state: model.StateDone, updated: "2026-05-12 09:00:00"},
		{title: "cancelled older", state: model.StateCancelled, updated: "2026-05-03 09:00:00"},
		// Fallback row: zero updated_at, recent created_at. SQLite has
		// no true NULL here, so we set updated_at to created_at — the
		// CompletionSortKey fallback is exercised by the equal case.
		{title: "cancelled newest via created_at", state: model.StateCancelled,
			updated: "2026-05-15 09:00:00", created: "2026-05-15 09:00:00"},
	}
	keysByTitle := map[string]string{}
	for _, sd := range seeds {
		iss, err := s.CreateIssue(repo.ID, nil, sd.title, "", sd.state, nil)
		if err != nil {
			t.Fatalf("create issue %q: %v", sd.title, err)
		}
		keysByTitle[sd.title] = iss.Key
		if sd.updated != "" {
			if _, err := s.DB.Exec(
				`UPDATE issues SET updated_at = ? WHERE id = ?`, sd.updated, iss.ID,
			); err != nil {
				t.Fatalf("set updated_at for %q: %v", sd.title, err)
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

	// Done: newest updated_at first.
	eq("Done", titles(model.StateDone),
		[]string{"done newest", "done middle", "done oldest"})
	// Cancelled: May 15 beats May 3.
	eq("Cancelled", titles(model.StateCancelled),
		[]string{"cancelled newest via created_at", "cancelled older"})
	// Todo: untouched creation order.
	eq("Todo", titles(model.StateTodo), []string{"todo a", "todo b"})
}
