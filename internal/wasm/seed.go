// Package wasm provides helpers used only by the browser-side build
// (GOOS=js GOARCH=wasm). The big-ticket item here is Seed, which fills
// a fresh in-memory store with a believable gelato kanban — 1 repo,
// 1 feature, 24 issues, a handful of documents, and the audit history
// those Creates produce as a side effect.
//
// Read-only. The TUI demo doesn't write back into the store after this
// runs.
package wasm

import (
	"fmt"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// flavour is one item on the gelato kanban — title goes verbatim into
// the issue, sub is a one-line description carried in the body.
type flavour struct {
	title string
	sub   string
	state model.State
}

var gelatoFlavours = []flavour{
	// Backlog — sorbettos + the cream-adjacent classics waiting to be made.
	{"Banana", "Banana sorbetto", model.StateBacklog},
	{"Pesca e Mango", "Peach & mango sorbetto", model.StateBacklog},
	{"Visciole", "Roman cherry sorbetto", model.StateBacklog},
	{"Limone", "Portofino lemon sorbetto", model.StateBacklog},
	{"Yogurt", "Italian yogurt cultures", model.StateBacklog},
	{"Caramello Salato", "Salted caramel with imported Sicilian sea salt", model.StateBacklog},
	// Todo — cream classics queued up.
	{"Vaniglia", "Vanilla bean with lemon twist", model.StateTodo},
	{"Fior di Latte", "Whole milk", model.StateTodo},
	{"Caffè", "House roasted coffee", model.StateTodo},
	{"Stracciatella", "Fior di latte with chocolate shavings", model.StateTodo},
	{"Crème Caramel", "Sweet caramel custard", model.StateTodo},
	{"Amaretto", "Italian almond cookie", model.StateTodo},
	// In Progress — chocolate stars currently being scooped, including
	// the signature Bacio.
	{"Cioccolato", "Milk chocolate from Torino", model.StateInProgress},
	{"Fondente", "70% dark chocolate from Torino", model.StateInProgress},
	{"Pistacchio", "Imported pistacchio di Bronte", model.StateInProgress},
	{"Bacio", "Milk & dark chocolate from Torino, imported Piedmont hazelnut", model.StateInProgress},
	{"Nutella", "Rich hazelnut & chocolate", model.StateInProgress},
	{"Valentino", "Chocolate-covered strawberry", model.StateInProgress},
	// Done — shipped signatures.
	{"Nocciola", "Imported Piedmont hazelnuts", model.StateDone},
	{"Bacio di Noce", "Decadent milk chocolate infused with Italian walnuts", model.StateDone},
	{"Desire", "Imported amarena cherries", model.StateDone},
	{"Tiramisu", "Espresso, mascarpone, ladyfingers", model.StateDone},
	{"Cannolo", "Sheep's milk cannoli & shell", model.StateDone},
	{"Kinderino", "Fior di latte, chocolate, hazelnut wafers", model.StateDone},
}

// seedDocument captures one of the in-repo docs to attach to the demo.
// Filename, type, and body — bacio's filename → type mapping is
// elsewhere; here we just hand it the type explicitly.
type seedDocument struct {
	filename string
	docType  model.DocumentType
	body     string
}

var gelatoDocs = []seedDocument{
	{
		"winter-2026-flavours.md",
		model.DocTypeProjectInProgress,
		`# Winter 2026 flavours

The winter menu is centered on **chocolate** — Cioccolato, Fondente,
Bacio, and Bacio di Noce share three of the four poster cards. The
sorbettos (Banana, Pesca e Mango, Visciole, Limone) shift to the
"summer" rotation in March.

## Goals

- Bacio (the eponymous flavour) launches Feb 14 — Valentine's promo
  pairs it with the Valentino chocolate-strawberry as a duo cup.
- Pistacchio inventory needs sign-off from the Bronte supplier by
  Jan 20 or we fall back to the Spanish blend.

## Sequencing

| Week | Issues moving        |
|------|----------------------|
| W3   | Cioccolato → done    |
| W4   | Fondente → done      |
| W5   | Bacio → done         |
`,
	},
	{
		"tasting-notes-bacio-di-noce.md",
		model.DocTypeUserDocs,
		`# Tasting notes — Bacio di Noce

> Decadent milk chocolate infused with Italian walnuts.

Two-stage churn: the walnut crumb is folded in at the very end so it
keeps the bite. **Don't** chop them ahead — the oils oxidize within
six hours and the flavour goes muted.

Pairs well with a fortified amber wine (Marsala Superiore) or the
house espresso.
`,
	},
	{
		"retro-2026-q1.md",
		model.DocTypeProjectComplete,
		`# Q1 retro — what worked, what didn't

**Worked:** the dual-tray approach to Pistacchio (one tray of Bronte,
one tray of Sicilian) let us A/B-taste during the soft launch. Bronte
won 11/12 on the panel.

**Didn't:** Banana sorbetto sat in Backlog for the entire quarter.
Either commit a Feb slot for it or move it to "cancelled" with notes.
`,
	},
}

// Seed populates s with the gelato kanban demo data. Returns the
// synthetic repo so callers can pass it into tui.NewModel.
func Seed(s *store.Store) (*model.Repo, error) {
	// Synthetic repo: prefix BACIO so issues key as BACIO-NN to match
	// the marketing kanban; path is plausible-looking but never
	// touched (we're in WASM, no filesystem).
	repo, err := s.CreateRepo("BACIO", "gelateria-bacio", "/Users/you/code/bacio-gelato", "")
	if err != nil {
		return nil, fmt.Errorf("seed: create repo: %w", err)
	}

	// One feature groups the winter menu's chocolate cluster — Cioccolato,
	// Fondente, Bacio, Bacio di Noce. The other flavours stay unassigned.
	feat, err := s.CreateFeature(repo.ID, "winter-2026-flavours",
		"Winter 2026 flavours",
		"The chocolate-centric winter menu — Bacio, Bacio di Noce, Cioccolato, Fondente.")
	if err != nil {
		return nil, fmt.Errorf("seed: create feature: %w", err)
	}

	featuredTitles := map[string]bool{
		"Cioccolato":    true,
		"Fondente":      true,
		"Bacio":         true,
		"Bacio di Noce": true,
	}

	for _, f := range gelatoFlavours {
		var featureID *int64
		if featuredTitles[f.title] {
			featureID = &feat.ID
		}
		if _, err := s.CreateIssue(repo.ID, featureID, f.title, f.sub, f.state, nil); err != nil {
			return nil, fmt.Errorf("seed: create issue %q: %w", f.title, err)
		}
	}

	// Documents — small set so the Documents tab has something to render.
	for _, d := range gelatoDocs {
		if _, err := s.CreateDocument(repo.ID, d.filename, d.docType, d.body, ""); err != nil {
			return nil, fmt.Errorf("seed: create doc %q: %w", d.filename, err)
		}
	}

	return repo, nil
}
