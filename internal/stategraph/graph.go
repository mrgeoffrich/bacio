// Package stategraph defines a single canonical graph of "standard"
// issue state transitions, exposed to the UI as a display hint — not an
// enforcement layer. The store still accepts any state-to-state move; the
// graph just tells UI surfaces which next-states are sensible to show
// prominently, show under a "more" affordance, or hide unless asked.
//
// BACI-241 introduced this package so surfaces that need to offer "what
// next?" (the follow-on / queue-next dispatch popup on the kanban card
// today, board-card right-click menus and dispatch-mode pickers later)
// can read one shared edge list instead of each hardcoding its own.
//
// The package sits atop internal/model — it imports model.State, never
// the other way round — so adding a new state in internal/model/state.go
// is forcing function: the unit tests below fail until the new state has
// at least one outgoing edge in the table.
package stategraph

import "github.com/mrgeoffrich/bacio/internal/model"

// Category labels the prominence of an edge. The category is purely a
// display hint — every (from, to) pair listed in Edges() is technically
// allowed by the store, the categorisation only affects rendering.
//
//   - Primary   — the happy-path next step. Shown first / largest.
//   - Secondary — sensible but not the default (escape hatches, branch
//     points).
//   - Unusual   — rarely the right call; hide behind a "more" affordance.
type Category string

const (
	Primary   Category = "primary"
	Secondary Category = "secondary"
	Unusual   Category = "unusual"
)

// Categories returns the canonical category list in display priority
// order (highest prominence first). Callers iterating the buckets — for
// example the kanban follow-on popup that asks "which is the highest-
// priority bucket whose state set intersects this prompt's allowedStates?"
// — should walk this slice rather than enumerating the constants
// themselves.
func Categories() []Category {
	return []Category{Primary, Secondary, Unusual}
}

// Edge is one (from, to, category) triple in the graph. Self-loops are
// excluded — the table is reviewed at compile time and the unit tests
// guard against accidental re-introduction.
type Edge struct {
	From     model.State `json:"from"`
	To       model.State `json:"to"`
	Category Category    `json:"category"`
}

// edges is the canonical edge table. Reviewers diff this slice the same
// way they diff the state-name constants in internal/model/state.go —
// the table is the source of truth.
//
// The starting categorisation came from the BACI-241 ticket sketch,
// refined during planning. Self-loops are excluded.
var edges = []Edge{
	// From todo: the happy path is to start work; cancelling unblocked
	// scope is the only other sensible move at this point.
	{From: model.StateTodo, To: model.StateInProgress, Category: Primary},
	{From: model.StateTodo, To: model.StateCancelled, Category: Secondary},

	// From in_progress: review is the happy path. The four secondary
	// branches cover "blocked on the user", "shipped without review",
	// "released back to the queue", and "abandoned mid-flight".
	{From: model.StateInProgress, To: model.StateInReview, Category: Primary},
	{From: model.StateInProgress, To: model.StateNeedsAction, Category: Secondary},
	{From: model.StateInProgress, To: model.StateDone, Category: Secondary},
	{From: model.StateInProgress, To: model.StateTodo, Category: Secondary},
	{From: model.StateInProgress, To: model.StateCancelled, Category: Secondary},

	// From needs_action: the user has responded, work resumes; or the
	// ticket is abandoned because the answer was "drop it".
	{From: model.StateNeedsAction, To: model.StateInProgress, Category: Primary},
	{From: model.StateNeedsAction, To: model.StateCancelled, Category: Secondary},

	// From in_review: ship is the happy path; review-failed sends it
	// back to in_progress; the cancel escape hatch is the same as
	// every other live state.
	{From: model.StateInReview, To: model.StateDone, Category: Primary},
	{From: model.StateInReview, To: model.StateInProgress, Category: Secondary},
	{From: model.StateInReview, To: model.StateCancelled, Category: Secondary},

	// From done / cancelled: re-opening is unusual. The two terminal
	// states each carry two unusual edges so a future "re-open this
	// ticket" affordance still has data to render from.
	{From: model.StateDone, To: model.StateInProgress, Category: Unusual},
	{From: model.StateDone, To: model.StateTodo, Category: Unusual},
	{From: model.StateCancelled, To: model.StateTodo, Category: Unusual},
	{From: model.StateCancelled, To: model.StateInProgress, Category: Unusual},
}

// Edges returns a copy of the canonical edge slice. Callers may sort or
// filter the returned slice freely — the copy keeps the table immutable
// from outside the package.
func Edges() []Edge {
	out := make([]Edge, len(edges))
	copy(out, edges)
	return out
}

// NextStatesFrom returns the set of `to` states reachable from `from`
// via an edge in the given category. The result is a slice (not a map)
// for ergonomics on the call side — the per-state-graph result is tiny
// (≤5 entries) and callers usually walk it linearly. Order matches the
// declaration order in the edge table so callers that render UI from
// the slice get a stable visual sequence.
func NextStatesFrom(from model.State, category Category) []model.State {
	var out []model.State
	for _, e := range edges {
		if e.From == from && e.Category == category {
			out = append(out, e.To)
		}
	}
	return out
}
