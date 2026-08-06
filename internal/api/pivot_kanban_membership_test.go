package api_test

// Pins the cross-transport lane-membership contract.
//
// internal/boardcards.BoardCard carries no lane field, so a Kanban card has
// nowhere to record which lane it sits in. Membership therefore rides on the
// CONTAINER — the same shape the sync layer uses (column.yaml lists issue
// uuids) and the same shape desktop/boardservice.go's KanbanColumnDTO
// returns. If this endpoint ever goes back to serving bare lanes, the web
// Kanban becomes unimplementable and the two transports stop satisfying one
// TS contract in desktop/frontend/src/api/contract.ts.

import (
	"net/http"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/api"
	"github.com/mrgeoffrich/bacio/internal/model"
)

// kanbanColumnWire mirrors the wire shape all three lane routes answer with.
type kanbanColumnWire struct {
	UUID     string `json:"uuid"`
	Name     string `json:"name"`
	Position int    `json:"position"`
	Cards    []struct {
		Key      string `json:"key"`
		Position int    `json:"position"`
	} `json:"cards"`
}

func TestKanbanColumnsListCarriesOrderedMembership(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)

	lane, err := s.CreateKanbanColumn(repo.ID, "Doing")
	if err != nil {
		t.Fatalf("CreateKanbanColumn: %v", err)
	}
	other, err := s.CreateKanbanColumn(repo.ID, "Done")
	if err != nil {
		t.Fatalf("CreateKanbanColumn: %v", err)
	}

	// Three cards into one lane, deliberately placed out of creation order
	// so a naive implementation that echoes insertion order fails.
	first := seedIssue(t, s, repo, "first")
	second := seedIssue(t, s, repo, "second")
	third := seedIssue(t, s, repo, "third")
	for pos, iss := range []*model.Issue{third, first, second} {
		if err := s.SetIssueKanbanColumn(iss.ID, &lane.ID, pos); err != nil {
			t.Fatalf("SetIssueKanbanColumn: %v", err)
		}
	}

	resp, raw := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/kanban/columns")
	mustStatus(t, resp, raw, http.StatusOK)
	cols := decodeInto[[]kanbanColumnWire](t, raw)

	byUUID := map[string]kanbanColumnWire{}
	for _, c := range cols {
		byUUID[c.UUID] = c
	}

	got := byUUID[lane.UUID]
	if len(got.Cards) != 3 {
		t.Fatalf("lane %q: want 3 cards, got %d (%s)", got.Name, len(got.Cards), raw)
	}
	want := []string{third.Key, first.Key, second.Key}
	for i, w := range want {
		if got.Cards[i].Key != w {
			t.Errorf("card %d: want %s, got %s — lane order must follow kanban_position", i, w, got.Cards[i].Key)
		}
	}

	// An empty lane must serialise as [] so the React side can map over it
	// without a guard. `null` would be a runtime crash, not a type error.
	empty := byUUID[other.UUID]
	if empty.Cards == nil {
		t.Errorf("an empty lane must serialise cards as [], got null")
	}
	if len(empty.Cards) != 0 {
		t.Errorf("lane %q: want no cards, got %d", empty.Name, len(empty.Cards))
	}
}

func TestKanbanColumnsListOmitsCardsThatAreNotOnTheBoard(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)

	lane, err := s.CreateKanbanColumn(repo.ID, "Doing")
	if err != nil {
		t.Fatalf("CreateKanbanColumn: %v", err)
	}
	onBoard := seedIssue(t, s, repo, "on the board")
	if err := s.SetIssueKanbanColumn(onBoard.ID, &lane.ID, 0); err != nil {
		t.Fatalf("SetIssueKanbanColumn: %v", err)
	}
	// A git repo's issues default to kanban_column_id = NULL — that is what
	// keeps a `todo` card on the Agentic Pipeline Backlog and off the Kanban
	// (locked decision D1). It must not appear in any lane.
	seedIssue(t, s, repo, "pipeline only")

	resp, raw := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/kanban/columns")
	mustStatus(t, resp, raw, http.StatusOK)

	total := 0
	for _, c := range decodeInto[[]kanbanColumnWire](t, raw) {
		total += len(c.Cards)
		for _, card := range c.Cards {
			if card.Key != onBoard.Key {
				t.Errorf("lane %q references %s, which is not on the board", c.Name, card.Key)
			}
		}
	}
	if total != 1 {
		t.Fatalf("want exactly the one opted-in card across all lanes, got %d (%s)", total, raw)
	}
}

func TestKanbanColumnCreateAnswersWithTheListShape(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)

	resp, raw := apiPost(t, ts.URL+"/repos/"+repo.Prefix+"/kanban/columns", `{"name":"Waiting"}`)
	mustStatus(t, resp, raw, http.StatusCreated)

	created := decodeInto[kanbanColumnWire](t, raw)
	if created.Name != "Waiting" {
		t.Fatalf("name: want Waiting, got %q", created.Name)
	}
	// One TS type covers list + create + patch only if every route emits
	// `cards`. A fresh lane is empty, but the key must still be present.
	if created.Cards == nil {
		t.Errorf("create must answer with the same shape as list, including cards: []")
	}
}
