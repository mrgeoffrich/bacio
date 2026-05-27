package stategraph

import (
	"testing"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// TestEdges_NoSelfLoops guards against accidentally adding a state→same-
// state edge in the table. Self-loops are excluded by design — the
// graph's job is to surface *next* states, not stay-where-you-are moves.
func TestEdges_NoSelfLoops(t *testing.T) {
	for _, e := range Edges() {
		if e.From == e.To {
			t.Errorf("self-loop edge in table: %s → %s (%s)", e.From, e.To, e.Category)
		}
	}
}

// TestEdges_NoDuplicates guards against listing the same (from, to)
// pair twice with different categories — the rendering wire scheme picks
// the highest-priority bucket whose state set intersects a prompt's
// allowedStates, so a duplicate would either be silently shadowed (the
// later one ignored) or pick the *wrong* bucket on the second pass.
func TestEdges_NoDuplicates(t *testing.T) {
	type pair struct{ from, to model.State }
	seen := make(map[pair]Category, len(edges))
	for _, e := range Edges() {
		key := pair{e.From, e.To}
		if existing, ok := seen[key]; ok {
			t.Errorf("duplicate edge %s → %s: first category %q, again as %q",
				e.From, e.To, existing, e.Category)
			continue
		}
		seen[key] = e.Category
	}
}

// TestEdges_EveryStateHasOutgoing is the forcing function for new
// states: adding a sixth-or-later state in internal/model/state.go
// without extending the edge table here fails this test, so the planner
// has to decide on its prominence at the same moment.
//
// Exemption list: none today. If a future state is intentionally
// terminal-with-no-rendered-outgoing (e.g. an "archived" state that
// should never surface "next" buttons), add it here with a comment
// explaining why.
func TestEdges_EveryStateHasOutgoing(t *testing.T) {
	terminalExempt := map[model.State]bool{}

	have := make(map[model.State]bool, len(edges))
	for _, e := range Edges() {
		have[e.From] = true
	}
	for _, s := range model.AllStates() {
		if terminalExempt[s] {
			continue
		}
		if !have[s] {
			t.Errorf("state %q has no outgoing edges in the graph (add at least one in graph.go or mark it exempt)", s)
		}
	}
}

// TestEdges_EveryToStateIsValid guards against typos in the table — a
// stringly-typed `model.State("inreview")` would slip through Go's type
// checker (the alias is just a `type State string`) but the result is a
// state name no other code in bacio knows about. Cross-check against
// model.AllStates so the table can't drift undetected.
func TestEdges_EveryToStateIsValid(t *testing.T) {
	valid := make(map[model.State]bool, 6)
	for _, s := range model.AllStates() {
		valid[s] = true
	}
	for _, e := range Edges() {
		if !valid[e.From] {
			t.Errorf("edge from unknown state %q: %s → %s (%s)", e.From, e.From, e.To, e.Category)
		}
		if !valid[e.To] {
			t.Errorf("edge to unknown state %q: %s → %s (%s)", e.To, e.From, e.To, e.Category)
		}
	}
}

// TestEdges_CategoryValid guards against an edge with a category outside
// the {primary, secondary, unusual} set — same typo-class trap as
// TestEdges_EveryToStateIsValid.
func TestEdges_CategoryValid(t *testing.T) {
	valid := make(map[Category]bool, 3)
	for _, c := range Categories() {
		valid[c] = true
	}
	for _, e := range Edges() {
		if !valid[e.Category] {
			t.Errorf("edge with unknown category %q: %s → %s", e.Category, e.From, e.To)
		}
	}
}

// TestNextStatesFrom_Categorises spot-checks the three buckets from
// two states the kanban follow-on popup will hit most often. Update
// these expectations when the graph categorisation changes (it should
// always be a deliberate review, not a silent drift).
func TestNextStatesFrom_Categorises(t *testing.T) {
	cases := []struct {
		from     model.State
		category Category
		want     []model.State
	}{
		{model.StateTodo, Primary, []model.State{model.StateInProgress}},
		{model.StateTodo, Secondary, []model.State{model.StateCancelled}},
		{model.StateTodo, Unusual, nil},

		{model.StateInProgress, Primary, []model.State{model.StateInReview}},
		{model.StateInReview, Primary, []model.State{model.StateDone}},
		{model.StateInReview, Secondary, []model.State{model.StateInProgress, model.StateCancelled}},

		{model.StateDone, Primary, nil},
		{model.StateDone, Unusual, []model.State{model.StateInProgress, model.StateTodo}},

		{model.StateCancelled, Unusual, []model.State{model.StateTodo, model.StateInProgress}},
	}
	for _, tc := range cases {
		got := NextStatesFrom(tc.from, tc.category)
		if !equalStates(got, tc.want) {
			t.Errorf("NextStatesFrom(%q, %q) = %v, want %v", tc.from, tc.category, got, tc.want)
		}
	}
}

// TestEdges_ReturnedSliceIsCopy guards against a caller mutating the
// returned slice and corrupting the package-level table. Callers might
// sort the slice or filter in place; the copy lets them do that without
// affecting subsequent calls.
func TestEdges_ReturnedSliceIsCopy(t *testing.T) {
	a := Edges()
	if len(a) == 0 {
		t.Fatal("Edges() returned an empty slice")
	}
	original := a[0]
	a[0] = Edge{From: model.StateDone, To: model.StateDone, Category: Unusual}
	b := Edges()
	if b[0] != original {
		t.Errorf("Edges() returned a slice that aliased the package table: %v vs %v", b[0], original)
	}
}

func equalStates(a, b []model.State) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
