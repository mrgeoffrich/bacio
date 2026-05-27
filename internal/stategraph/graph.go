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
// The canonical edge table lives in graph.yaml next to this file — edit
// that file to add or recategorise an edge. The YAML is embedded into
// the binary via //go:embed; the graph_test.go invariants run on the
// next build.
//
// The package sits atop internal/model — it imports model.State, never
// the other way round — so adding a new state in internal/model/state.go
// is forcing function: the unit tests below fail until the new state has
// at least one outgoing edge in graph.yaml.
package stategraph

import (
	"bytes"
	_ "embed"
	"fmt"

	"go.yaml.in/yaml/v4"

	"github.com/mrgeoffrich/bacio/internal/model"
)

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

//go:embed graph.yaml
var graphYAML []byte

// edges is the canonical edge table, flattened at package init from
// graph.yaml. The YAML shape is map[from]map[category][]to; we flatten
// here by walking AllStates() × Categories() so the resulting slice has
// a deterministic order (matching the display-priority order callers
// already use for the wire shape and for NextStatesFrom).
var edges = loadEdges()

func loadEdges() []Edge {
	var raw map[model.State]map[Category][]model.State
	dec := yaml.NewDecoder(bytes.NewReader(graphYAML))
	dec.KnownFields(true)
	if err := dec.Decode(&raw); err != nil {
		panic(fmt.Sprintf("stategraph: parse graph.yaml: %v", err))
	}

	// Pre-validate top-level keys — unknown from-states or categories
	// would otherwise be silently skipped by the AllStates/Categories
	// walk below and never reach the graph_test.go invariants,
	// defeating the typo-trap.
	allStates := make(map[model.State]bool, len(model.AllStates()))
	for _, s := range model.AllStates() {
		allStates[s] = true
	}
	allCats := make(map[Category]bool, len(Categories()))
	for _, c := range Categories() {
		allCats[c] = true
	}
	for from, cats := range raw {
		if !allStates[from] {
			panic(fmt.Sprintf("stategraph: graph.yaml has unknown from-state %q", from))
		}
		for cat := range cats {
			if !allCats[cat] {
				panic(fmt.Sprintf("stategraph: graph.yaml has unknown category %q under %q", cat, from))
			}
		}
	}

	var out []Edge
	for _, from := range model.AllStates() {
		cats, ok := raw[from]
		if !ok {
			continue
		}
		for _, cat := range Categories() {
			for _, to := range cats[cat] {
				out = append(out, Edge{From: from, To: to, Category: cat})
			}
		}
	}
	return out
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
// declaration order in graph.yaml so callers that render UI from the
// slice get a stable visual sequence.
func NextStatesFrom(from model.State, category Category) []model.State {
	var out []model.State
	for _, e := range edges {
		if e.From == from && e.Category == category {
			out = append(out, e.To)
		}
	}
	return out
}
