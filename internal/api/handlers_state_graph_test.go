package api_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/api"
)

// TestHandleStateGraph covers the BACI-241 read-only endpoint. The data
// is a Go constant — no per-test seeding needed; the JSON shape is the
// contract the React layer reshapes against.
func TestHandleStateGraph(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})

	resp := do(t, http.MethodGet, ts.URL+"/settings/state-graph", nil, nil)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}

	var body struct {
		States []string `json:"states"`
		Edges  []struct {
			From     string `json:"from"`
			To       string `json:"to"`
			Category string `json:"category"`
		} `json:"edges"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Six known states — todo, in_progress, needs_action, in_review,
	// done, cancelled.
	if len(body.States) != 6 {
		t.Errorf("states: got %d, want 6 (%v)", len(body.States), body.States)
	}
	wantStates := map[string]bool{
		"todo": true, "in_progress": true, "needs_action": true,
		"in_review": true, "done": true, "cancelled": true,
	}
	for _, s := range body.States {
		if !wantStates[s] {
			t.Errorf("unexpected state on the wire: %q", s)
		}
	}

	// Sixteen edges per the BACI-241 planning sketch — spot-check a
	// few headline edges rather than re-listing the whole table (the
	// table itself is unit-tested in internal/stategraph).
	if len(body.Edges) == 0 {
		t.Fatal("no edges on the wire")
	}
	foundTodoInProgressPrimary := false
	foundInReviewDonePrimary := false
	foundDoneTodoUnusual := false
	for _, e := range body.Edges {
		if e.From == "todo" && e.To == "in_progress" && e.Category == "primary" {
			foundTodoInProgressPrimary = true
		}
		if e.From == "in_review" && e.To == "done" && e.Category == "primary" {
			foundInReviewDonePrimary = true
		}
		if e.From == "done" && e.To == "todo" && e.Category == "unusual" {
			foundDoneTodoUnusual = true
		}
		// Category must be one of the three known values — any
		// drift surfaces immediately as a render bug client-side.
		switch e.Category {
		case "primary", "secondary", "unusual":
		default:
			t.Errorf("edge with unknown category %q: %s → %s", e.Category, e.From, e.To)
		}
	}
	if !foundTodoInProgressPrimary {
		t.Error("missing edge: todo → in_progress (primary)")
	}
	if !foundInReviewDonePrimary {
		t.Error("missing edge: in_review → done (primary)")
	}
	if !foundDoneTodoUnusual {
		t.Error("missing edge: done → todo (unusual)")
	}
}

// TestHandleStateGraph_ContentType pins the JSON content-type so a
// future refactor that swaps writeJSON for a raw write doesn't slip a
// text/plain response past the client-side fetch.
func TestHandleStateGraph_ContentType(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})

	resp := do(t, http.MethodGet, ts.URL+"/settings/state-graph", nil, nil)
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type: got %q, want application/json", ct)
	}
}
