// Package wasm carries the gelato demo seed used by two callers:
//
//   - The browser build (cmd/bacio-wasm, GOOS=js GOARCH=wasm) seeds a
//     fresh in-memory store before handing it to the TUI.
//   - The CLI `bacio demo` command seeds a synthetic repo in the
//     local SQLite store so the developer can stress-test the UI
//     against realistic volumes of issues, features, comments, and
//     docs.
//
// The base seed is a believable, lived-in gelato shop: 4 features,
// ~38 issues spread across all states, assignees, tags, review-note
// comments on done items, attached PRs, and 9 linked documents.
// SeedOptions.Copies > 1 duplicates the features/issues/docs N times
// with -copyN suffixes so the same store can hold an order of
// magnitude more rows without hand-authoring new content.
package wasm

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// ----- features ------------------------------------------------------------

type seedFeature struct {
	slug, title, desc string
}

var gelatoFeatures = []seedFeature{
	{
		"winter-2026-flavours",
		"Winter 2026 flavours",
		"Chocolate-centric winter menu — Bacio, Bacio di Noce, Cioccolato, Fondente, plus the sorbetto rotation moving to spring.",
	},
	{
		"valentines-2026-promo",
		"Valentine's 2026 promo",
		"The Bacio × Valentino duo cup. Window display, social posts, paper sleeves printed in Rivalta.",
	},
	{
		"new-pos-system",
		"New POS system",
		"Replacing the ancient Vend till. Vendor evaluation, stock import, receipt printer compatibility, staff training.",
	},
	{
		"sustainability-q1",
		"Sustainability Q1",
		"Compostable spoons, cup-supplier audit, customer composting survey. Pressure from city council deadline in March.",
	},
}

// ----- issues --------------------------------------------------------------

type seedIssue struct {
	title       string
	desc        string
	state       model.State
	featureSlug string   // "" = unassigned
	assignee    string   // "" = unassigned
	tags        []string // optional
	prs         []string // GitHub-style URLs to attach
	comments    []seedComment
}

type seedComment struct {
	author, body string
}

// Authors used across the seed. Roughly: owner, FoH manager, head
// gelatiere, marketing intern, and the agent that opens half the
// tickets.
const (
	authorGeoff       = "@geoff"
	authorAnna        = "@anna"
	authorMarco       = "@marco"
	authorMatteo      = "@matteo"
	authorAgentClaude = "agent-claude"
)

var gelatoIssues = []seedIssue{

	// --- Todo: sorbettos + cream classics queued up --------------------

	{
		title: "Banana", desc: "Banana sorbetto. First-batch panel notes are in `docs/banana-tasting-2026-01-08.md` — kids loved it, adults split.",
		state: model.StateTodo, assignee: authorMarco,
		tags: []string{"experimental", "kids"},
		comments: []seedComment{
			{authorAnna, "Three Saturdays in a row, the four-year-olds asked for 'the yellow one'. Worth keeping warm."},
		},
	},
	{
		title: "Pesca e Mango", desc: "Peach & mango sorbetto. Slushy on refreeze — needs more inverted sugar.",
		state: model.StateTodo, assignee: authorMarco,
		tags: []string{"experimental", "qa"},
	},
	{
		title: "Visciole", desc: "Roman cherry sorbetto. Visciole are seasonal — sourcing windowed Apr–Jun.",
		state: model.StateTodo,
		tags:  []string{"supplier"},
	},
	{
		title: "Limone", desc: "Portofino lemon sorbetto. Switching from amalfi to portofino lemons; trial run pending.",
		state: model.StateTodo, assignee: authorMarco,
		tags: []string{"supplier", "experimental"},
	},
	{
		title: "Yogurt", desc: "Italian yogurt cultures. **agent-claude is waiting on a call**: Jersey vs Holstein ratio for the fat content — dairy supplier offered both but needs the spec before they'll quote. Marco, can you weigh in?",
		state: model.StateNeedsAction, assignee: authorAgentClaude,
		tags: []string{"blocked", "supplier"},
		comments: []seedComment{
			{authorAgentClaude, "Reached the limit of what I can decide on my own — fat content affects mouthfeel which is a Marco-flavour call, not an agent call. Parked here until we get a steer."},
		},
	},
	{
		title: "Caramello Salato", desc: "Salted caramel with imported Sicilian sea salt. Last batch crystallised on cooling — try lower-temp pour.",
		state: model.StateTodo, assignee: authorMarco,
		tags: []string{"qa"},
		comments: []seedComment{
			{authorMarco, "Pour temp was 121°C. Lab notes say 117 — operator error. Will retry Tue."},
		},
	},

	// --- Todo: extra classics queued up --------------------------------

	{
		title: "Vaniglia", desc: "Vanilla bean with lemon twist. Vanilla supplier sent Madagascar Grade A — confirm pricing before committing 2026 calendar.",
		state: model.StateTodo, assignee: authorMarco,
		tags: []string{"supplier"},
	},
	{
		title: "Fior di Latte", desc: "Whole milk. House base — keep recipe stable, no changes.",
		state: model.StateTodo,
	},
	{
		title: "Caffè", desc: "House roasted coffee. Working with Lavazza Tierra blend.",
		state: model.StateTodo, assignee: authorMarco,
	},
	{
		title: "Stracciatella", desc: "Fior di latte with chocolate shavings. Need a sharper chocolate ribbon — current one is too thick, doesn't crack.",
		state: model.StateTodo, assignee: authorMarco,
		tags: []string{"qa"},
	},
	{
		title: "Crème Caramel", desc: "Sweet caramel custard. Egg ratio TBD — last test was eggy on the finish.",
		state: model.StateTodo, assignee: authorMarco,
	},
	{
		title: "Amaretto", desc: "Italian almond cookie inclusion. Currently using shop-baked amaretti but it's a time sink — evaluate buying in.",
		state: model.StateTodo,
		tags:  []string{"experimental"},
	},

	// --- In Progress: chocolate stars + the signature ------------------

	{
		title: "Cioccolato", desc: "Milk chocolate from Torino. Batch QA from Mon: tested at 11.2% fat (target 12) — redo with 8% bump in cocoa butter.",
		state: model.StateInProgress, featureSlug: "winter-2026-flavours", assignee: authorMarco,
		tags: []string{"qa"},
		comments: []seedComment{
			{authorAgentClaude, "Scheduled a re-batch for Thu 09:00. Set a calendar reminder for QA panel at 14:00."},
		},
	},
	{
		title: "Fondente", desc: "70% dark chocolate from Torino. Tempering protocol agreed with Marco — needs the slow cool curve.",
		state: model.StateInProgress, featureSlug: "winter-2026-flavours", assignee: authorMarco,
	},
	{
		title: "Pistacchio", desc: "Imported pistacchio di Bronte. Supplier call notes in `docs/pistacchio-bronte-call.md` — they want a 6-month commitment.",
		state: model.StateInProgress, assignee: authorGeoff,
		tags: []string{"supplier", "rush"},
		comments: []seedComment{
			{authorGeoff, "If Bronte falls through, fallback is Spanish (Almería). Slightly less perfumed but cheaper."},
		},
	},
	{
		title: "Bacio", desc: "Milk & dark chocolate from Torino, imported Piedmont hazelnut. **The signature flavour.** Launches Feb 14 as part of the Valentine's promo (duo cup with Valentino).",
		state: model.StateInProgress, featureSlug: "winter-2026-flavours", assignee: authorMarco,
		tags: []string{"rush"},
		comments: []seedComment{
			{authorMarco, "Tempering curve finalised. Hazelnut paste arriving Wed."},
			{authorAnna, "Window display brief is in `docs/valentines-promo-plan.md` — confirming with Matteo by Fri."},
		},
	},
	{
		title: "Nutella", desc: "Rich hazelnut & chocolate variant — distinct from Bacio (less dark chocolate, more cream).",
		state: model.StateInProgress, assignee: authorMarco,
	},
	{
		title: "Valentino", desc: "Chocolate-covered strawberry. Paired with Bacio for the Feb 14 duo cup.",
		state: model.StateInProgress, featureSlug: "valentines-2026-promo", assignee: authorMarco,
		tags: []string{"rush"},
	},

	// --- Done: shipped flavours, with review notes ---------------------

	{
		title: "Nocciola", desc: "Imported Piedmont hazelnuts. Tested under blind panel against Bronte hazelnuts — Piedmont won 8/9.",
		state: model.StateDone, assignee: authorMarco,
		tags: []string{"qa", "supplier"},
		comments: []seedComment{
			{authorMarco, "Approved 2026-01-28. Panel scored 9.1/10. Notes: 'roasted note dominant, sweetness clean'. Keep on rotation."},
		},
	},
	{
		title: "Bacio di Noce", desc: "Decadent milk chocolate infused with Italian walnuts. Two-stage churn — walnut crumb folded at the end.",
		state: model.StateDone, featureSlug: "winter-2026-flavours", assignee: authorMarco,
		tags: []string{"qa"},
		comments: []seedComment{
			{authorAnna, "Customer feedback after week 1: 'tastes like Christmas in a cup'. Repeat order from the Bar Roma table x4."},
			{authorMarco, "**Sourcing note for next batch:** get walnuts from Sorrento, not Bari — oils last 2× longer."},
		},
	},
	{
		title: "Desire", desc: "Imported amarena cherries. Cherry quantity bumped to 14% (was 11%) — gives a clearer cherry hit at the end.",
		state: model.StateDone, assignee: authorMarco,
		tags: []string{"qa"},
		comments: []seedComment{
			{authorMarco, "Approved 2026-01-30. Two customers asked if we could sell the syrup separately. Maybe? Spin out as a follow-up ticket."},
		},
	},
	{
		title: "Tiramisu", desc: "Espresso, mascarpone, ladyfingers. Espresso source: Lavazza Tierra. Ladyfingers from Pasticceria Marchetti.",
		state: model.StateDone, assignee: authorMarco,
		tags: []string{"qa"},
		comments: []seedComment{
			{authorMarco, "Note for next batch: warm the mascarpone 15min before mixing or it clumps. Lesson learned the hard way."},
		},
	},
	{
		title: "Cannolo", desc: "Sheep's milk ricotta with cannoli shell crumb. Supplier is Pastificio Sicilia — confirmed monthly delivery cadence.",
		state: model.StateDone, assignee: authorMarco,
		tags: []string{"supplier", "qa"},
		comments: []seedComment{
			{authorAnna, "Three customers asked 'are these really shells?' — yes, broken cannoli shells. Keep the answer at the counter handy."},
		},
	},
	{
		title: "Kinderino", desc: "Fior di latte, milk chocolate ribbons, hazelnut wafer crumb. Aimed at the under-10 crowd.",
		state: model.StateDone, assignee: authorMatteo,
		tags: []string{"kids", "qa"},
		comments: []seedComment{
			{authorAnna, "Highest sell-through of the kid menu — outsold Banana 3:1 in the first month."},
			{authorMatteo, "Got photo permission from two families for the Instagram post — saved to `docs/social-photo-releases.md`."},
		},
	},

	// --- Valentine's promo (separate feature) --------------------------

	{
		title: "Duo cup sleeve design", desc: "Paper sleeve wrapping the Bacio × Valentino duo cup. Red & black, gelato-shop typography.",
		state: model.StateTodo, featureSlug: "valentines-2026-promo", assignee: authorAnna,
		tags: []string{"rush", "social"},
	},
	{
		title: "Sleeve print run — 2000 units", desc: "Print supplier in Rivalta. Quote in `docs/valentines-promo-plan.md`. Need design locked Mon to hit Feb 5 delivery.",
		state: model.StateInProgress, featureSlug: "valentines-2026-promo", assignee: authorAnna,
		tags: []string{"rush", "supplier"},
		prs:  []string{"https://github.com/gelateria-bacio/promo-assets/pull/12"},
	},
	{
		title: "Window display mockup", desc: `"rosso e nero" theme. Reuses the Bacio mark blown up to 60cm. Mock-up due Wed.`,
		state: model.StateTodo, featureSlug: "valentines-2026-promo", assignee: authorGeoff,
		tags: []string{"rush", "social"},
	},
	{
		title: "Social posts Feb 1 / 7 / 14", desc: "Three-post sequence: tease (Feb 1), behind-the-scenes (Feb 7), launch (Feb 14). Captions drafted in `docs/valentines-promo-plan.md`.",
		state: model.StateTodo, featureSlug: "valentines-2026-promo", assignee: authorMatteo,
		tags: []string{"rush", "social"},
	},
	{
		title: "Coffee discount on duo combo", desc: "10% off espresso when ordered with the duo cup. Till rules updated, tested at FOH.",
		state: model.StateDone, featureSlug: "valentines-2026-promo", assignee: authorAnna,
		tags: []string{"social"},
		comments: []seedComment{
			{authorAnna, "Approved 2026-02-01. Coffee combo took 22% of duo orders in the soft launch weekend. Strong."},
		},
	},

	// --- POS system replacement ----------------------------------------

	{
		title: "Vendor comparison: Lightspeed vs Toast vs Square", desc: "Three-way bake-off. Lightspeed wins on stock import; Toast on receipt printer compat; Square on UX. Recommendation in linked doc.",
		state: model.StateDone, featureSlug: "new-pos-system", assignee: authorAgentClaude,
		tags: []string{"qa"},
		comments: []seedComment{
			{authorGeoff, "Approved 2026-01-22. Going with **Lightspeed** — stock import lifecycle was the deciding factor (we manage 60+ SKUs)."},
			{authorAgentClaude, "Decision rationale + dissenting bits captured in `docs/pos-vendor-comparison.md` for posterity."},
		},
	},
	{
		title: "Migration plan from Vend", desc: "Three-week cutover plan: data export → staff training → soft launch. Pencilled in for Mar 1.",
		state: model.StateInProgress, featureSlug: "new-pos-system", assignee: authorGeoff,
		tags: []string{"rush"},
	},
	{
		title: "Stock count import script", desc: "Convert Vend CSV to Lightspeed JSON. SKU mapping is the gnarly bit — Vend's are alphanumeric, Lightspeed wants numeric.",
		state: model.StateInProgress, featureSlug: "new-pos-system", assignee: authorAgentClaude,
		prs:   []string{"https://github.com/gelateria-bacio/pos-migrate/pull/3"},
	},
	{
		title: "Receipt printer compat test", desc: "Star TSP143IIIBI — works with Lightspeed out of the box, but the duo-cup discount line wraps weirdly. Patch in PR.",
		state: model.StateInReview, featureSlug: "new-pos-system", assignee: authorAnna,
		prs:   []string{"https://github.com/gelateria-bacio/pos-migrate/pull/7"},
		comments: []seedComment{
			{authorAnna, "Test prints attached. Geoff/Matteo to eyeball Tue."},
		},
	},
	{
		title: "Staff training session", desc: "1-hour walkthrough of the new till. Schedule the FoH team in pairs — front counter must stay open.",
		state: model.StateTodo, featureSlug: "new-pos-system", assignee: authorGeoff,
	},

	// --- Sustainability ------------------------------------------------

	{
		title: "Source compostable spoons", desc: "Wood is too splintery (customer complaints), bamboo is too soft. PLA might work — sample request out to three suppliers.",
		state: model.StateInProgress, featureSlug: "sustainability-q1", assignee: authorMatteo,
		tags: []string{"blocked", "supplier"},
	},
	{
		title: "Audit current packaging waste", desc: "Weekly bin weight tracking for 4 weeks. Plastic cup is 62% of waste by mass — biggest lever.",
		state: model.StateDone, featureSlug: "sustainability-q1", assignee: authorAgentClaude,
		tags: []string{"qa"},
		comments: []seedComment{
			{authorAgentClaude, "Findings in `docs/packaging-waste-audit.md`. **Biggest single change** would be switching the 250ml cup from PP to PLA — drops total waste by ~50%."},
		},
	},
	{
		title: "Switch cup supplier to PaperBoy", desc: "PaperBoy do PLA-lined paper cups, council-approved, 18% more expensive than current.",
		state: model.StateTodo, featureSlug: "sustainability-q1", assignee: authorGeoff,
		tags: []string{"supplier"},
	},
	{
		title: `Customer survey: "do you actually compost?"`, desc: "Question-of-the-month at the counter. Cancelled because answer was obvious from foot traffic (we're in a council pickup area).",
		state: model.StateCancelled, featureSlug: "sustainability-q1", assignee: authorAnna,
	},
}

// ----- documents -----------------------------------------------------------

type seedDoc struct {
	filename string
	docType  model.DocumentType
	body     string
	// linkTo: optional, points to a feature slug ("") or issue title.
	linkFeature string
	linkIssue   string
	linkDesc    string
}

var gelatoDocs = []seedDoc{
	{
		filename: "winter-2026-flavours.md",
		docType:  model.DocTypeProjectInProgress,
		linkFeature: "winter-2026-flavours",
		linkDesc:    "Winter menu planning",
		body: `# Winter 2026 flavours

The winter menu is centered on **chocolate** — Cioccolato, Fondente,
Bacio, and Bacio di Noce share three of the four poster cards. The
sorbettos (Banana, Pesca e Mango, Visciole, Limone) shift to the
"summer" rotation in March.

## Goals

- **Bacio** (the eponymous flavour) launches Feb 14 — Valentine's
  promo pairs it with the Valentino chocolate-strawberry as a duo cup.
- Pistacchio inventory needs sign-off from the Bronte supplier by
  Jan 20 or we fall back to the Spanish (Almería) blend.

## Sequencing

| Week | Issues moving        |
|------|----------------------|
| W3   | Cioccolato → done    |
| W4   | Fondente → done      |
| W5   | Bacio → done         |

## Risks

- Bronte pistachio supplier wants a 6-month commitment (BACIO-15).
- Tempering room is at 18°C — historically drifts to 20°C in March;
  invest in a better thermostat if winter run extends.
`,
	},
	{
		filename: "tasting-notes-bacio-di-noce.md",
		docType:  model.DocTypeUserDocs,
		linkIssue: "Bacio di Noce",
		linkDesc:  "Tasting panel notes for sign-off",
		body: `# Tasting notes — Bacio di Noce

> Decadent milk chocolate infused with Italian walnuts.

Two-stage churn: the walnut crumb is folded in at the very end so it
keeps the bite. **Don't** chop them ahead — the oils oxidise within
six hours and the flavour goes muted.

Pairs well with a fortified amber wine (Marsala Superiore) or the
house espresso.

## Panel scores (n=9, 2026-01-28)

| Tester | Score (/10) | Notes                                |
|--------|-------------|--------------------------------------|
| Anna   | 9.5         | "Walnut up front, chocolate behind"  |
| Marco  | 9.0         | "Mouthfeel is right"                 |
| Geoff  | 9.2         | "Slightly under-sweet, balanced"     |
| Customer panel (n=6) | 9.0 avg | "Christmas in a cup" (x2) |
`,
	},
	{
		filename: "retro-2026-q1.md",
		docType:  model.DocTypeProjectComplete,
		body: `# Q1 retro — what worked, what didn't

**Worked**

- The dual-tray approach to Pistacchio (one tray of Bronte, one of
  Spanish) let us A/B-taste during the soft launch. Bronte won 11/12
  on the panel — locked in the supplier.
- agent-claude opening tickets the day-of-incident kept the audit log
  clean. Easier to write retros from filed-as-you-go than from memory.

**Didn't**

- Banana sorbetto never got off the Todo column the entire quarter.
  Either commit a Feb slot for it or move it to Cancelled with notes.
  Decision pending.
- Tempering room thermostat drifted twice — alarm went off at 03:00
  on a Sunday. Need a better setup before summer.

**Action items**

- BACIO-37: Switch cup supplier (in flight)
- BACIO-1: Decide on Banana — slot or kill
- Off-ticket: replace tempering thermostat by Mar 1
`,
	},
	{
		filename: "valentines-promo-plan.md",
		docType:  model.DocTypeProjectInPlanning,
		linkFeature: "valentines-2026-promo",
		linkDesc:    "Promo planning + copy drafts",
		body: `# Valentine's 2026 — promo plan

## The duo cup

Bacio + Valentino. Two scoops, one cup, paper sleeve in red and black.

## Print run

- Vendor: **Tipografia Rivalta** (Anna's contact via Pasticceria Marchetti)
- Quantity: 2000 sleeves
- Deadline: design locked Mon 2026-02-02, delivery Thu 2026-02-05
- Cost: €0.18/sleeve, €360 total — within Q1 marketing budget

## Social cadence

| Date  | Post                                       | Channel        |
|-------|--------------------------------------------|----------------|
| Feb 1 | Tease: close-up of Bacio on a red plate    | IG, FB         |
| Feb 7 | Behind-the-scenes: Marco tempering         | IG reels       |
| Feb 14| Launch: customers holding the duo cup      | IG, FB, X      |

## In-store

- Window display: "rosso e nero" theme, 60cm Bacio mark backlit
- Counter: chalkboard message + duo cup pricing
- Coffee discount: 10% off espresso with any duo order
`,
	},
	{
		filename: "pos-vendor-comparison.md",
		docType:  model.DocTypeArchitecture,
		linkFeature: "new-pos-system",
		linkDesc:    "Three-way vendor evaluation",
		body: `# POS vendor comparison

Three-way bake-off for the Vend replacement. Evaluation framework:
stock import, receipt printer compat, FoH UX, monthly cost, lock-in
risk.

| Vendor      | Stock | Printer | UX  | Cost   | Lock-in | Total |
|-------------|-------|---------|-----|--------|---------|-------|
| Lightspeed  | 9     | 7       | 7   | €89/mo | Medium  | **30** |
| Toast       | 6     | 9       | 6   | €99/mo | High    | 27 |
| Square      | 5     | 8       | 9   | €70/mo | Low     | 28 |

## Decision

**Lightspeed**, on the strength of stock import. We manage 60+ SKUs
(flavour x size x ingredient) and the import lifecycle was the only
one that didn't require us to re-key inventory every week.

## Dissent

Matteo argued for Square on UX grounds — the till is what every
new hire learns first, and the easier it is to learn, the less Anna
has to babysit. Counterpoint accepted; mitigation is the staff
training session (BACIO-34) and a back-of-counter cheatsheet.
`,
	},
	{
		filename: "pistacchio-bronte-call.md",
		docType:  model.DocTypeUserDocs,
		linkIssue: "Pistacchio",
		linkDesc:  "Supplier call notes 2026-01-15",
		body: `# Pistacchio di Bronte — supplier call 2026-01-15

Geoff + Marco on the call; supplier rep was Giulia from Bronte Co-op.

## Asks

- 6-month commitment (Mar–Aug) at €38/kg flat
- Quarterly delivery, with the option to bump to monthly if our
  volume goes over 40kg/quarter
- They will not undercut the price they offer their other Torino
  buyers (Caffè San Marco, Pasticceria d'Oro)

## Our position

- We want a 3-month commitment first, then auto-renew. Giulia agreed
  in principle, contract draft pending from their side.
- Confirmed the 8% protein floor — earlier shipments were edging down
  to 7% in dry years.

## Risks

- If the contract falls through, fallback is Almería pistachios at
  €29/kg. Marco notes the perfume is less prominent and the panel
  rated it a full point lower. Acceptable as a stop-gap, not as
  the long-term plan.
`,
	},
	{
		filename: "packaging-waste-audit.md",
		docType:  model.DocTypeProjectComplete,
		linkFeature: "sustainability-q1",
		linkDesc:    "Q4 weekly waste audit",
		body: `# Packaging waste audit — Q4 2025

Four weeks of weekly waste bin weighings, sorted by stream:

| Stream                | Weekly avg (kg) | Share |
|-----------------------|-----------------|-------|
| PP cups (250ml)       | 8.4             | 62%   |
| Plastic spoons        | 2.1             | 16%   |
| Paper napkins         | 1.6             | 12%   |
| Cardboard (delivery)  | 1.4             | 10%   |

## Conclusion

The 250ml cup dominates. Swapping to **PaperBoy PLA-lined paper cups**
drops total waste by ~50% (paper cups are still waste, but the
council picks them up as part of organics rather than landfill).

## Adjacent: spoons

Wood spoons were tried and rejected (splinters). Bamboo was too
soft. PLA spoons are next — three samples in the post.
`,
	},
	{
		filename: "supplier-list.md",
		docType:  model.DocTypeUserDocs,
		body: `# Supplier list (current)

| Ingredient / item        | Supplier                  | Contact   |
|--------------------------|---------------------------|-----------|
| Pistachios (Bronte)      | Bronte Co-op              | Giulia    |
| Hazelnuts (Piedmont)     | Nocciole Langhe           | Stefano   |
| Walnuts                  | Sorrento Walnut Coop      | Cristina  |
| Sicilian sea salt        | Saline di Trapani         | (online)  |
| Amarena cherries         | Fabbri Vignola            | (online)  |
| Mascarpone               | Caseificio Lombardo       | Andrea    |
| Whole milk               | Fattoria Giovanna         | Paolo     |
| Coffee beans (Lavazza)   | Lavazza Tierra            | (online)  |
| Ladyfingers              | Pasticceria Marchetti     | Marco M.  |
| Cannoli shells           | Pastificio Sicilia        | (online)  |
| 250ml cups (PP, current) | Imballaggi Lombardia      | Daniele   |
| 250ml cups (PLA, trial)  | PaperBoy                  | (online)  |
| Print/sleeves            | Tipografia Rivalta        | Anna's    |
`,
	},
	{
		filename: "staff-roster-week-12.md",
		docType:  model.DocTypeUserDocs,
		body: `# Staff roster — Week 12 (2026-03-15..03-21)

| Day  | Open–13:00      | 13:00–18:00      | 18:00–close      |
|------|-----------------|------------------|------------------|
| Mon  | Anna            | Anna + Matteo    | Matteo           |
| Tue  | Marco (kitchen) | Anna             | Anna             |
| Wed  | Anna + Geoff    | Matteo           | Anna             |
| Thu  | Anna            | Anna             | Geoff            |
| Fri  | Marco (kitchen) | Anna + Matteo    | Matteo           |
| Sat  | Anna + Matteo   | Anna + Matteo + Geoff | Anna + Matteo |
| Sun  | Anna            | Matteo           | closed           |

Marco only at front-of-house if I (Anna) am off — he panics with
the till. Geoff covers the awkward Wed–Thu evening when Anna is at
her Italian class.
`,
	},
}

// ----- Seed ----------------------------------------------------------------

// SeedOptions controls which synthetic repo the seed lands in and
// how many duplicate passes of the gelato content to load. Zero
// values default to the legacy WASM-demo behaviour (BACIO prefix,
// single copy) so callers that don't care can pass SeedOptions{}.
type SeedOptions struct {
	// Prefix is the 4-char repo prefix. Empty defaults to "BACIO".
	Prefix string
	// Name is the repo's display name. Empty defaults to "gelateria-bacio".
	Name string
	// Path is the repo's filesystem path. Empty defaults to a synthetic
	// "/Users/you/code/bacio-gelato" (kept for backward compatibility
	// with the WASM demo). The CLI demo command supplies a synthetic
	// path namespaced by prefix so multiple demo repos can coexist.
	Path string
	// Copies controls how many times to layer the gelato feature/issue/doc
	// set over the same repo. Values <=1 mean "single copy" (the base
	// seed). Higher values append "-copyN" / " (copy N)" suffixes to
	// slugs, titles, and filenames so the rows stay unique.
	Copies int
}

// Seed populates s with the lived-in gelato demo data. Returns the
// synthetic repo so callers can pass it into tui.NewModel.
func Seed(s *store.Store, opts SeedOptions) (*model.Repo, error) {
	prefix := opts.Prefix
	if prefix == "" {
		prefix = "BACIO"
	}
	name := opts.Name
	if name == "" {
		name = "gelateria-bacio"
	}
	path := opts.Path
	if path == "" {
		path = "/Users/you/code/bacio-gelato"
	}
	copies := opts.Copies
	if copies < 1 {
		copies = 1
	}

	repo, err := s.CreateRepo(prefix, name, path, "")
	if err != nil {
		return nil, fmt.Errorf("seed: create repo: %w", err)
	}

	for i := 0; i < copies; i++ {
		if err := seedOnePass(s, repo, i); err != nil {
			return nil, err
		}
	}

	// Audit-log events are only written for the base pass — they
	// reference specific issue titles and would produce noisy
	// duplicates if replayed per copy. They populate the History tab
	// with realistic-looking lines for the user clicking around.
	seedHistoryEvents(s, repo)

	return repo, nil
}

// seedOnePass writes one layer of features, issues, comments, PRs, and
// docs. copyIdx=0 keeps the original slugs/titles/filenames; copyIdx>=1
// suffixes them with -copyN / " (copy N)" so a multi-pass seed avoids
// UNIQUE collisions on (repo, slug), (repo, filename), and the seed's
// own issue-title-keyed link map.
func seedOnePass(s *store.Store, repo *model.Repo, copyIdx int) error {
	slugSfx, titleSfx, fileSfx := copySuffixes(copyIdx)

	featureIDBySlug := map[string]int64{}
	for _, f := range gelatoFeatures {
		slug := f.slug + slugSfx
		title := f.title + titleSfx
		feat, err := s.CreateFeature(repo.ID, slug, title, f.desc)
		if err != nil {
			return fmt.Errorf("seed: create feature %q: %w", slug, err)
		}
		featureIDBySlug[f.slug] = feat.ID
	}

	issueIDByTitle := map[string]int64{}
	for _, iss := range gelatoIssues {
		var featureID *int64
		if iss.featureSlug != "" {
			if id, ok := featureIDBySlug[iss.featureSlug]; ok {
				featureID = &id
			}
		}
		title := iss.title + titleSfx
		created, err := s.CreateIssue(repo.ID, featureID, title, iss.desc, iss.state, iss.tags)
		if err != nil {
			return fmt.Errorf("seed: create issue %q: %w", title, err)
		}
		issueIDByTitle[iss.title] = created.ID

		if iss.assignee != "" {
			if err := s.SetIssueAssignee(created.ID, iss.assignee); err != nil {
				return fmt.Errorf("seed: assign issue %q: %w", title, err)
			}
		}
		for _, url := range iss.prs {
			if _, err := s.AttachPR(created.ID, url); err != nil {
				return fmt.Errorf("seed: attach PR to %q: %w", title, err)
			}
		}
		for _, c := range iss.comments {
			if _, err := s.CreateComment(created.ID, c.author, c.body); err != nil {
				return fmt.Errorf("seed: create comment on %q: %w", title, err)
			}
		}
	}

	for _, d := range gelatoDocs {
		filename := suffixFilename(d.filename, fileSfx)
		doc, err := s.CreateDocument(repo.ID, filename, d.docType, d.body, "")
		if err != nil {
			return fmt.Errorf("seed: create doc %q: %w", filename, err)
		}
		if d.linkFeature != "" {
			if id, ok := featureIDBySlug[d.linkFeature]; ok {
				if _, err := s.LinkDocument(doc.ID, store.LinkTarget{FeatureID: &id}, d.linkDesc); err != nil {
					return fmt.Errorf("seed: link doc %q to feature %q: %w", filename, d.linkFeature, err)
				}
			}
		}
		if d.linkIssue != "" {
			if id, ok := issueIDByTitle[d.linkIssue]; ok {
				if _, err := s.LinkDocument(doc.ID, store.LinkTarget{IssueID: &id}, d.linkDesc); err != nil {
					return fmt.Errorf("seed: link doc %q to issue %q: %w", filename, d.linkIssue, err)
				}
			}
		}
	}
	return nil
}

// copySuffixes returns the slug/title/filename suffix for the given
// copy index. Copy 0 is the base pass and uses no suffix; copies 1..
// append "-copy2", " (copy 2)", "-copy2" respectively (the +1 keeps
// the user-visible numbering 1-based so "copy 2" reads as "the second
// copy" rather than "the copy with index 1").
func copySuffixes(copyIdx int) (slug, title, file string) {
	if copyIdx == 0 {
		return "", "", ""
	}
	n := copyIdx + 1
	return fmt.Sprintf("-copy%d", n),
		fmt.Sprintf(" (copy %d)", n),
		fmt.Sprintf("-copy%d", n)
}

// suffixFilename inserts the copy suffix before the file extension.
// "winter-2026-flavours.md" + "-copy2" → "winter-2026-flavours-copy2.md".
// Files without an extension just get the suffix appended.
func suffixFilename(name, sfx string) string {
	if sfx == "" {
		return name
	}
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	return stem + sfx + ext
}

// seedHistoryEvents writes a representative slice of audit-log lines
// for the base pass. Labels are prefixed with repo.Prefix so the
// entries make sense regardless of which prefix the caller picked.
func seedHistoryEvents(s *store.Store, repo *model.Repo) {
	recordEvent := func(actor, op, kind, label, details string) {
		_ = s.RecordHistory(model.HistoryEntry{
			RepoID: &repo.ID, RepoPrefix: repo.Prefix,
			Actor: actor, Op: op, Kind: kind, TargetLabel: label, Details: details,
		})
	}
	recordEvent(authorGeoff, "init", "repo", repo.Name, "prefix="+repo.Prefix)
	recordEvent(authorAgentClaude, "feature.add", "feature", "winter-2026-flavours", "")
	recordEvent(authorAgentClaude, "issue.add", "issue", repo.Prefix+"-13 Cioccolato", "feature=winter-2026-flavours")
	recordEvent(authorAgentClaude, "issue.add", "issue", repo.Prefix+"-19 Bacio", "feature=winter-2026-flavours")
	recordEvent(authorMarco, "issue.state", "issue", repo.Prefix+"-19 Bacio", "todo → in_progress")
	recordEvent(authorMarco, "issue.assign", "issue", repo.Prefix+"-19 Bacio", "→ @marco")
	recordEvent(authorAgentClaude, "issue.state", "issue", "Yogurt", "in_progress → needs_action")
	recordEvent(authorAgentClaude, "feature.add", "feature", "valentines-2026-promo", "")
	recordEvent(authorAnna, "issue.add", "issue", "Duo cup sleeve design", "feature=valentines-2026-promo")
	recordEvent(authorAnna, "pr.attach", "issue", "Sleeve print run — 2000 units", "https://github.com/gelateria-bacio/promo-assets/pull/12")
	recordEvent(authorGeoff, "feature.add", "feature", "new-pos-system", "")
	recordEvent(authorAgentClaude, "issue.add", "issue", "Vendor comparison: Lightspeed vs Toast vs Square", "feature=new-pos-system")
	recordEvent(authorAgentClaude, "doc.add", "document", "pos-vendor-comparison.md", "type=architecture")
	recordEvent(authorGeoff, "issue.state", "issue", "Vendor comparison: Lightspeed vs Toast vs Square", "in_review → done")
	recordEvent(authorGeoff, "comment.add", "issue", "Vendor comparison: Lightspeed vs Toast vs Square", "approval — going with Lightspeed")
	recordEvent(authorAgentClaude, "pr.attach", "issue", "Stock count import script", "https://github.com/gelateria-bacio/pos-migrate/pull/3")
	recordEvent(authorAnna, "pr.attach", "issue", "Receipt printer compat test", "https://github.com/gelateria-bacio/pos-migrate/pull/7")
	recordEvent(authorAnna, "issue.state", "issue", "Receipt printer compat test", "in_progress → in_review")
	recordEvent(authorMatteo, "feature.add", "feature", "sustainability-q1", "")
	recordEvent(authorAgentClaude, "doc.add", "document", "packaging-waste-audit.md", "type=project_complete")
	recordEvent(authorGeoff, "issue.state", "issue", "Audit current packaging waste", "in_progress → done")
	recordEvent(authorAnna, "issue.state", "issue", `Customer survey: "do you actually compost?"`, "todo → cancelled")
	recordEvent(authorMarco, "comment.add", "issue", "Nocciola", "review approval — 9.1/10")
	recordEvent(authorAnna, "comment.add", "issue", "Bacio di Noce", "customer feedback — 'Christmas in a cup'")
	recordEvent(authorMarco, "comment.add", "issue", "Desire", "approved 2026-01-30; cherry quantity bumped to 14%")
	recordEvent(authorMarco, "comment.add", "issue", "Tiramisu", "note: warm mascarpone 15min before mixing")
	recordEvent(authorAnna, "comment.add", "issue", "Cannolo", "FoH q&a: yes those are real cannoli shells")
	recordEvent(authorMatteo, "comment.add", "issue", "Kinderino", "photo permissions secured for IG launch")
	recordEvent(authorAnna, "comment.add", "issue", "Coffee discount on duo combo", "approved — 22% combo attach rate in soft launch")
	recordEvent(authorMarco, "issue.state", "issue", "Cioccolato", "fat content 11.2% — re-batch Thu 09:00")
	recordEvent(authorAgentClaude, "comment.add", "issue", "Cioccolato", "scheduled re-batch + QA panel reminder for Thu 14:00")
	recordEvent(authorGeoff, "comment.add", "issue", "Pistacchio", "fallback plan locked: Almería pistachios at €29/kg if Bronte deal falls through")
}
