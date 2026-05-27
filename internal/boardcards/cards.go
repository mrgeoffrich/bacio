// Package boardcards assembles the per-issue BoardCard used by every
// kanban surface (desktop Wails BoardService, web bundle via REST). The
// single source of truth lived in desktop/boardservice.go until it was
// hoisted here so the bacio api can serve the same shape over REST
// without the browser reshaping raw model.Issue rows.
//
// Assemble takes a client.Client and a repo filter (a single repo or
// nil for cross-repo). All reads are bulked — one ListIssues + one
// ListOpenClaims per scope, plus an extra ListAgentSessions /
// ListTodosBySessions / RepoDispatches / ListPromptTemplates pass when
// any claim is open (which is the only state the active-verb / tasks
// progress fields are visible in). A board with no taken cards stays
// at two queries.
//
// JSON tags are camelCase to match the desktop's pre-extraction wire
// format so the existing TS bindings and api.http.ts reshape are
// preserved — the web frontend can read either without reshape.
package boardcards

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// descriptionExcerptRunes (BACI-171) caps the per-card description
// excerpt at ~140 runes — wide enough for two lines at the tray's
// 320px width, narrow enough not to bloat the kanban poll payload
// (~30 cards × 140 chars is a few KB per refresh).
const descriptionExcerptRunes = 140

// stateLabels maps each bacio issue state to a human-friendly column label.
// Duplicated from desktop/boardservice.go's stateLabels rather than imported
// (the desktop is a separate Go module) — keep in sync.
var stateLabels = map[model.State]string{
	model.StateTodo:        "Todo",
	model.StateInProgress:  "In Progress",
	model.StateNeedsAction: "Needs Action",
	model.StateInReview:    "In Review",
	model.StateDone:        "Done",
	model.StateCancelled:   "Cancelled",
}

func StateLabel(s model.State) string {
	if l, ok := stateLabels[s]; ok {
		return l
	}
	return string(s)
}

// CompletionSortKey returns the timestamp used to order an issue
// within the Done / Cancelled columns: terminal_at (the dedicated
// "moved into done/cancelled" timestamp, BACI-138), else updated_at,
// else created_at. Newest-first ordering sorts descending.
//
// terminal_at is the load-bearing field — it's stamped by every state
// transition INTO done/cancelled and cleared on every transition OUT,
// so a tag / title / description edit on a closed issue no longer
// reshuffles the column (the BACI-101 proxy the BACI-138 brief
// flagged). The updated_at / created_at fallback handles two narrow
// cases: (1) the migration backfill seeds terminal_at = updated_at
// for pre-BACI-138 closed rows, but a fresh DB created before the
// row migrated could still race with the read; (2) defence in depth
// for unit tests that build a *model.Issue by hand without setting
// TerminalAt.
func CompletionSortKey(iss *model.Issue) time.Time {
	if iss == nil {
		return time.Time{}
	}
	if iss.TerminalAt != nil && !iss.TerminalAt.IsZero() {
		return *iss.TerminalAt
	}
	if !iss.UpdatedAt.IsZero() {
		return iss.UpdatedAt
	}
	return iss.CreatedAt
}

// IsCompletedColumn reports whether a state is one of the two columns
// BACI-101 sorts newest-first (Done, Cancelled).
func IsCompletedColumn(s model.State) bool {
	return s == model.StateDone || s == model.StateCancelled
}

// WaitingKind enumerates the three reasons a card may be displaying a
// waiting spinner — together with the optional Mode/ActionLabel on
// WaitingState they're the data the kanban / TUI render as the inline
// "why is this card waiting?" label (BACI-145).
type WaitingKind string

const (
	// WaitingQueuedNoAgent: a `queued` dispatch is sitting in the
	// per-(repo, mode) FIFO; no free agent has picked it up yet.
	// Label: "Waiting for an available agent".
	WaitingQueuedNoAgent WaitingKind = "queued_no_agent"
	// WaitingQueuedBlocked: a `queued` (or pending) dispatch is gated by
	// the template's concurrency_limit — the per-(repo, mode) cap is
	// full so the matcher won't bind it yet. Label: "Waiting on <Mode>
	// job to finish" (Mode is the same mode as this card's dispatch —
	// the cap is per-template).
	WaitingQueuedBlocked WaitingKind = "queued_blocked"
	// WaitingDelivered: the worker has taken the Task in hand (BACI-130);
	// cancel-after-delivery is rejected at the store boundary so the
	// card renders the spinner without the cancel affordance. Label:
	// "Worker has the <Mode> job".
	WaitingDelivered WaitingKind = "delivered"
)

// WaitingState is the structured "why is this card waiting?" projection
// surfaced on every kanban card with an active dispatch (BACI-145).
// Replaces the two booleans WaitingForClaim / WaitingDispatchDelivered
// that the React tree and TUI previously pivoted on — the booleans
// couldn't carry the active dispatch's mode for the label.
//
// Nil when the card isn't waiting. When non-nil, Kind is always
// populated; Mode/ActionLabel are set for every kind that names a job
// in the label (i.e. queued_blocked and delivered), and best-effort on
// queued_no_agent — the mode is known but the label doesn't render it.
type WaitingState struct {
	Kind        WaitingKind        `json:"kind"`
	Mode        model.DispatchMode `json:"mode,omitempty"`
	ActionLabel string             `json:"actionLabel,omitempty"`
}

// BoardCard is one kanban card — one bacio issue, shaped for the
// imported UI kit. Fields beyond the issue itself: Taken (an agent
// holds an open claim on this issue), WaitingState (a dispatch is
// queued/pending/delivered; BACI-145), ActiveVerb (the lower-cased
// prompt-template label of the newest open claim's dispatch — e.g.
// "designing", "planning"), and TodosDone / TodosTotal (the TodoWrite
// progress of the claiming session).
type BoardCard struct {
	Key         string   `json:"key"`
	Column      string   `json:"column"`
	ColumnLabel string   `json:"columnLabel"`
	Title       string   `json:"title"`
	// DescriptionExcerpt (BACI-171) is a short (~140-rune) excerpt of
	// the issue's description used by the bottom-right ActivityTray to
	// render a one-or-two-line summary per entry without an extra
	// per-card brief fetch on every change. Truncates on a word
	// boundary with a trailing "…"; empty (and omitted from JSON) when
	// the issue's description is empty. The kanban card itself ignores
	// this field — the tray is the sole consumer.
	DescriptionExcerpt string   `json:"descriptionExcerpt,omitempty"`
	Tags               []string `json:"tags"`
	Assignees          []string `json:"assignees"`
	Claude             bool     `json:"claude"`
	Taken              bool     `json:"taken"`
	// FeatureEmoji (BACI-172) is the per-feature glyph denormalised
	// from the issue's joined feature row in store.issueSelect. Empty
	// (and omitted from JSON) when the issue has no feature, or the
	// feature has no emoji set — the kanban card renders the slot
	// only when truthy.
	FeatureEmoji string `json:"featureEmoji,omitempty"`
	// FeatureBranchName (BACI-231) is the parent feature's integration
	// branch (e.g. "feat/auth") denormalised from the issue's joined
	// feature row. Empty (and omitted from JSON) when the card belongs
	// to no feature or to a feature that ships straight to main — the
	// kanban card renders the branch chip and the ActivityTray groups
	// cards by branch only when this is truthy.
	FeatureBranchName string `json:"featureBranchName,omitempty"`
	// WaitingState (BACI-145) carries the structured reason a card is
	// rendering the spinner — queued without an agent, queued but
	// blocked by the template's concurrency cap, or delivered to the
	// worker. Replaces the two booleans WaitingForClaim and
	// WaitingDispatchDelivered the BoardCard previously carried. Nil
	// (and omitted from JSON) when the card isn't waiting — keeps the
	// wire payload lean on untaken / settled cards.
	WaitingState *WaitingState `json:"waitingState,omitempty"`
	// ActiveVerb is the lower-cased display label of the prompt template
	// behind the newest open claim's most recent non-cancelled dispatch
	// — e.g. "designing", "planning", or a custom template's lowered
	// Name. Empty when the issue isn't taken, when no dispatch matches,
	// or when the template was deleted.
	ActiveVerb string `json:"activeVerb,omitempty"`
	// TodosDone and TodosTotal mirror the TodoWrite progress of the
	// session that holds the newest open claim on this issue. Both
	// zero when the issue isn't taken or the session never wrote a
	// TodoList. The UI hides the Tasks counter when TodosTotal == 0.
	TodosDone  int `json:"todosDone"`
	TodosTotal int `json:"todosTotal"`
	// OpenQuestions are the BACI-53 ask_user_question rows posed by
	// the winning claim's session against THIS issue. Drives the
	// "? N — <header>" pill on the kanban card and the click-to-open
	// answer modal. Empty when the issue isn't taken or the agent
	// hasn't asked anything yet. The full QuestionPayload is fetched
	// on demand via /agents/questions/{id} when the user opens the
	// modal — the bare ID + a short summary is enough to render the
	// badge.
	OpenQuestions []BoardCardQuestion `json:"openQuestions,omitempty"`
	// Todos (BACI-75) carries the per-task TodoWrite rows of the
	// session that holds the newest open claim, scoped to THIS card's
	// issue. Same data the TodosDone/TodosTotal counts summarise —
	// surfaced so the kanban card's expandable "Tasks n/m" pill can
	// render the list inline without a follow-up fetch. Empty (and
	// omitted from JSON) on untaken cards / when the session never
	// wrote a TodoList for this issue.
	Todos []BoardCardTodo `json:"todos,omitempty"`
	// Archived (BACI-68) mirrors the underlying issue's archived_at —
	// true iff the column is non-NULL. When display.show_archived is
	// on, the card surfaces with this flag set so the UI can render
	// it visibly muted (lower opacity, "archived" pill, etc.). When
	// display.show_archived is off, archived rows are filtered out
	// earlier by the IncludeArchived parameter on Assemble — so this
	// is only ever true on a board the user has explicitly opted in
	// to seeing.
	Archived bool `json:"archived,omitempty"`
	// BlockedBy (BACI-114) carries the keys + states of every open-
	// state `blocks` edge pointing AT this issue. Empty (and omitted
	// from JSON) when the issue is unblocked. Used by the kanban
	// blocked-icon indicator and the click-to-show-blockers popover
	// on each card; the issue-workspace rail's full relations panel
	// goes through the brief's RelationsDTO instead so the chip
	// surface covers all four edge types, not just blocks.
	BlockedBy []BoardCardBlocker `json:"blockedBy,omitempty"`
	// TranscriptDocCount (BACI-141) is the number of `.jsonl`
	// transcript documents linked to this issue — typed `transcript`
	// rows plus legacy `project_complete` rows whose filename matches
	// the bacio-transcript naming convention. Drives the kanban
	// combined eval/transcript indicator. Omitempty so unaffected
	// cards stay compact on the wire.
	TranscriptDocCount int `json:"transcriptDocCount,omitempty"`
	// EvalCommentCount (BACI-141) is the number of eval-flagged
	// comments on this issue. Surfaces on the kanban card regardless
	// of `Taken` state, so eval notes posted on a previously-active
	// card stay discoverable after the agent releases its claim.
	EvalCommentCount int `json:"evalCommentCount,omitempty"`
	// TerminalAt (BACI-187) is the BACI-138 "moved into a terminal
	// state" timestamp — non-nil on Done / Cancelled cards, nil on
	// open-state cards. The shipping-log topbar pill derives its
	// "last 7 days of done" count client-side from the already-polled
	// `cards` array; passing the timestamp through is the cheapest
	// way to avoid a second poll loop. Omitempty keeps open cards
	// lean on the wire — they outnumber done cards on a healthy board.
	TerminalAt *time.Time `json:"terminalAt,omitempty"`
	// FollowOn (BACI-192) is the denormalised view of the dormant
	// follow-on dispatch attached to this issue's currently in-flight
	// (parent) dispatch — the single-slot row written by BACI-180's
	// AddFollowOnDispatch. Drives the kanban card's footer follow-on
	// button visual state. Nil (and omitted from JSON) when no
	// dormant follow-on exists; the footer button stays in its
	// outline state in that case.
	FollowOn *BoardCardFollowOn `json:"followOn,omitempty"`
	// LatestPlan (BACI-216) is the newest `plan`-typed document
	// linked directly to this issue, or nil when no plan is linked.
	// Drives the per-card plan icon that opens the doc viewer —
	// hidden when nil. The wider issue-shaped read paths (CLI brief
	// / show, REST brief / show, Wails issue detail) carry the same
	// projection (model.LatestPlan) with snake_case JSON tags; the
	// kanban surface re-emits it under camelCase here so the BoardCard
	// stays consistent with every other camelCase field on the wire.
	LatestPlan *BoardCardLatestPlan `json:"latestPlan,omitempty"`
	// LatestPR (BACI-239) is the most-recently-attached PR on this
	// issue, or nil when no PR is attached. Drives the per-card PR
	// chip that opens the PR URL in a new tab — sibling of LatestPlan
	// in shape and render slot. Newest-attached wins (the link a user
	// clicks after a review iteration is the freshly-opened PR); the
	// `count` field on the value lets the card's tooltip surface
	// "N attached" when more than one is linked.
	LatestPR *BoardCardLatestPR `json:"latestPR,omitempty"`
}

// BoardCardLatestPlan (BACI-216) is the camelCase mirror of
// model.LatestPlan used on the BoardCard wire payload. Snake-case
// works fine on the CLI / REST issue payloads (matches every other
// model.* field on those surfaces); the BoardCard's contract is
// camelCase across the board, so the kanban shape gets its own
// projection of the same four fields.
type BoardCardLatestPlan struct {
	DocumentID int64     `json:"documentId"`
	UUID       string    `json:"uuid"`
	Filename   string    `json:"filename"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

func boardCardLatestPlan(p *model.LatestPlan) *BoardCardLatestPlan {
	if p == nil {
		return nil
	}
	return &BoardCardLatestPlan{
		DocumentID: p.DocumentID,
		UUID:       p.UUID,
		Filename:   p.Filename,
		UpdatedAt:  p.UpdatedAt,
	}
}

// BoardCardLatestPR (BACI-239) is the camelCase mirror of
// model.LatestPR used on the BoardCard wire payload. Carries the URL
// of the most-recently-attached PR so the per-card chip can link
// straight to it, plus the total PR count so the tooltip can advertise
// "N attached" without a second round-trip.
type BoardCardLatestPR struct {
	URL       string    `json:"url"`
	Count     int       `json:"count"`
	CreatedAt time.Time `json:"createdAt"`
}

func boardCardLatestPR(p *model.LatestPR) *BoardCardLatestPR {
	if p == nil {
		return nil
	}
	return &BoardCardLatestPR{
		URL:       p.URL,
		Count:     p.Count,
		CreatedAt: p.CreatedAt,
	}
}

// BoardCardBlocker (BACI-114) is one open `blocks` edge pointing at
// this card. Key is the canonical PREFIX-N of the blocker; State is
// the blocker's state at assembly time. Title is intentionally NOT
// on the wire — the React popover joins it from the same `cards`
// array (Option A: thin renderer over data the brief already has).
type BoardCardBlocker struct {
	Key   string      `json:"key"`
	State model.State `json:"state"`
}

// BoardCardFollowOn (BACI-192) is the denormalised view of the dormant
// follow-on dispatch attached to this issue. Surfaced on every taken /
// waiting / blocked-and-idle card so the kanban footer button can
// render the queued mode without a per-card REST call.
//
// Mode is the prompt-template slug the backend stored on the dispatch
// row; ActionLabel is the imperative verb (resolved via the same
// prompt-template lookup as ActiveVerb / WaitingState) so the button
// label can read "▶| Plan" rather than the bare slug. Nil (and
// omitted from JSON) when the issue has no dormant follow-on — the
// renderer only paints the .is-attached state when truthy.
//
// WaitingReason (BACI-217) is a short server-derived label
// describing what the dormant row is waiting on, when the variant is
// the blockers-clear gate ("blocked by N"). Empty (and omitted from
// JSON) for the parent-acks variant (today's BACI-179 default — the
// chip reads the mode label without a secondary qualifier).
type BoardCardFollowOn struct {
	Mode          model.DispatchMode `json:"mode"`
	ActionLabel   string             `json:"actionLabel"`
	WaitingReason string             `json:"waitingReason,omitempty"`
}

// BoardCardQuestion is one open ask_user_question row surfaced on
// the issue's kanban card. Header is the user-facing tag (≤12 chars,
// the same field AskUserQuestion uses); FirstQuestion is the full
// text of the first question in the payload (intended as the brief
// detail next to the pill); Count is len(payload.questions) so a
// multi-question modal can advertise "answer 3 questions".
type BoardCardQuestion struct {
	ID            int64     `json:"id"`
	Header        string    `json:"header"`
	FirstQuestion string    `json:"firstQuestion"`
	Count         int       `json:"count"`
	AskedAt       time.Time `json:"askedAt"`
}

// BoardCardTodo is one TodoWrite row surfaced on a kanban card —
// the inline-expansion list under the "Tasks n/m" pill. A thinner
// sibling of agentcards.SessionTodoDTO: no IssueKey field, because
// rows are pre-filtered server-side to this card's own issue.
type BoardCardTodo struct {
	Content string `json:"content"`
	Status  string `json:"status"`
}

// Assemble returns one BoardCard per issue in scope. When repo is
// nil, every tracked repo's issues are merged (the cross-repo "all"
// board the desktop sidebar uses). The agent-derived enrichment
// (Taken, ActiveVerb, TodosDone/Total) is computed by joining the
// open claims onto their sessions and the most recent dispatch for
// each (session, issue) pair, then mapped by issue key.
//
// Archived issues (BACI-68) are hidden by default. Pass
// includeArchived=true to inflate — the HTTP handler reads
// ?include_archived=1 OR the display.show_archived global setting.
//
// hiddenFeatureSlugs (BACI-177) is the set of feature slugs to exclude
// from the board — the per-feature "Show on board" toggle flipped off
// from the Features screen. Cards belonging to those features never
// ship over the wire. Empty slice = no filter. The caller (REST or
// Wails) loads the set from the per-repo board-hide KV first.
func Assemble(ctx context.Context, c client.Client, repo *model.Repo, includeArchived bool, hiddenFeatureSlugs []string) ([]BoardCard, error) {
	// BACI-171: request descriptions so the per-card DescriptionExcerpt
	// can be computed below. ListIssues otherwise strips the description
	// column to keep list responses lean; the excerpt is short (~140
	// runes) so the bulk-read cost stays modest.
	filter := client.IssueFilter{
		IncludeArchived:    includeArchived,
		IncludeDescription: true,
		HiddenFeatureSlugs: hiddenFeatureSlugs,
	}
	var repos []*model.Repo
	if repo == nil {
		filter.AllRepos = true
		rs, err := c.ListRepos(ctx)
		if err != nil {
			return nil, err
		}
		repos = rs
	} else {
		filter.Repo = repo
		repos = []*model.Repo{repo}
	}

	issues, err := c.ListIssues(ctx, filter)
	if err != nil {
		return nil, err
	}
	claims, err := c.ListOpenClaims(ctx, filter.Repo)
	if err != nil {
		return nil, err
	}

	// BACI-130: read every dispatch in scope once and reuse the slice
	// for both the per-(session, issue) ActiveVerb derivation
	// (`pickActiveMode`) and the new per-issue WaitingState derivation
	// (BACI-145, which absorbed BACI-130's WaitingDispatchDelivered).
	// RepoDispatches is already newest-first.
	var allDispatches []*model.AgentDispatch
	for _, r := range repos {
		ds, err := c.RepoDispatches(ctx, r)
		if err != nil {
			return nil, err
		}
		allDispatches = append(allDispatches, ds...)
	}
	activeByIssueID := activeDispatchByIssueID(allDispatches)
	// BACI-192 / BACI-217: per-issue dormant follow-on lookup. The
	// dispatch slice is already newest-first; first match wins per
	// issue. A row is a dormant follow-on when its status is queued
	// AND either the parent-acks gate (queued_after_dispatch_id IS
	// NOT NULL — BACI-179) or the blockers-clear gate
	// (queued_until_blockers_clear — BACI-217) is set. Same predicate
	// store.FollowOnForIssue uses, but derived client-side so we
	// don't N+1 against the store.
	followOnByIssueID := make(map[int64]*model.AgentDispatch, len(allDispatches))
	for _, d := range allDispatches {
		if d == nil || d.IssueID == nil {
			continue
		}
		if d.Status != model.DispatchQueued {
			continue
		}
		if d.QueuedAfterDispatchID == nil && !d.QueuedUntilBlockersClear {
			continue
		}
		if _, seen := followOnByIssueID[*d.IssueID]; seen {
			continue
		}
		followOnByIssueID[*d.IssueID] = d
	}

	// BACI-145 / BACI-227: bulked per-(repo, mode, base_branch)
	// in-flight count so the deriver can spot a concurrency-cap-
	// blocked queued dispatch without an N+1 scan per card. One read
	// per repo. The matcher reads the same table via
	// CountInFlightByModeBase so the per-card label and the matcher's
	// per-branch bind decision stay in lockstep — a card on feat/A
	// only chips as "blocked" when feat/A's slot is full, not when
	// some other branch's ship is in flight.
	inflightByRepo := make(map[int64]map[store.InflightKey]int, len(repos))
	for _, r := range repos {
		m, err := c.InflightByModeBaseForRepo(ctx, r)
		if err != nil {
			return nil, err
		}
		inflightByRepo[r.ID] = m
	}

	// BACI-145: prompt-template lookup for the action-label half of the
	// WaitingState. Same templates the verb-derivation path uses below;
	// shared lookup avoids a second ListPromptTemplates round-trip.
	templates, err := c.ListPromptTemplates(ctx)
	if err != nil {
		return nil, err
	}

	enrichByKey, err := enrichmentByIssueKey(ctx, c, claims, allDispatches, templates)
	if err != nil {
		return nil, err
	}

	// BACI-114: one bulk read for every inbound `blocks` edge whose
	// blocked end is in scope, filtered to open-state blockers only.
	// The result is keyed by blocked-issue id — used below to fan
	// BlockedBy onto each card.
	issueIDs := make([]int64, 0, len(issues))
	for _, iss := range issues {
		issueIDs = append(issueIDs, iss.ID)
	}
	blockersByID, err := c.BlockersFor(ctx, issueIDs)
	if err != nil {
		return nil, err
	}
	blockedByID := make(map[int64][]BoardCardBlocker, len(blockersByID))
	for blockedID, bs := range blockersByID {
		for _, b := range bs {
			if !isCardBlockerOpen(b.BlockerState) {
				continue
			}
			blockedByID[blockedID] = append(blockedByID[blockedID], BoardCardBlocker{
				Key:   b.BlockerKey,
				State: b.BlockerState,
			})
		}
	}

	// BACI-141: two bulk reads — one per indicator — that fan onto every
	// card regardless of `taken` state. Both queries are O(N issues in
	// scope) and ride existing indices (idx_comments_issue_eval for
	// evals; the document_links FK indices for transcripts). Surfacing
	// these counts on untaken cards too is the whole point of the
	// ticket: the BACI-131 quick-eval composer only renders while a
	// claim is held, so eval notes on a since-released card would
	// otherwise be invisible from the board.
	evalCountsByID, err := c.CountEvalCommentsByIssue(ctx, issueIDs)
	if err != nil {
		return nil, err
	}
	transcriptCountsByID, err := c.CountTranscriptDocsByIssue(ctx, issueIDs)
	if err != nil {
		return nil, err
	}
	// BACI-216: per-card "newest linked plan" lookup. Same
	// single-bulk-read-per-N-issues pattern as the counts above —
	// fans onto every card regardless of `taken` state so the plan
	// icon stays visible after the worker has released its claim.
	latestPlanByID, err := c.LatestPlanByIssue(ctx, issueIDs)
	if err != nil {
		return nil, err
	}
	// BACI-239: per-card "newest attached PR" lookup. Same
	// bulk-read-per-N-issues pattern as the plan lookup above; fans
	// onto every card regardless of `taken` state so the PR chip
	// stays visible after the worker has released its claim.
	latestPRByID, err := c.LatestPRByIssue(ctx, issueIDs)
	if err != nil {
		return nil, err
	}

	cards := make([]BoardCard, 0, len(issues))
	for _, iss := range issues {
		tags := iss.Tags
		if tags == nil {
			tags = []string{}
		}
		e := enrichByKey[iss.Key]
		ws := DeriveWaitingState(iss, activeByIssueID[iss.ID], inflightByRepo[iss.RepoID], templates)
		// BACI-192: shape the dormant follow-on row (if any) into the
		// wire DTO. The action label resolves via the same lookup the
		// WaitingState path uses, so the button reads consistently
		// with the spinner label. BACI-217: on a blockers-clear
		// variant the chip carries a "blocked by N" waiting reason
		// derived from the already-loaded blockedByID map; the
		// parent-acks variant leaves WaitingReason empty (the chip
		// reads the mode label as today).
		var followOn *BoardCardFollowOn
		if fo := followOnByIssueID[iss.ID]; fo != nil {
			followOn = &BoardCardFollowOn{
				Mode:        fo.Mode,
				ActionLabel: actionLabelForMode(fo.Mode, templates),
			}
			if fo.QueuedUntilBlockersClear {
				n := len(blockedByID[iss.ID])
				if n > 0 {
					followOn.WaitingReason = fmt.Sprintf("blocked by %d", n)
				}
			}
		}
		cards = append(cards, BoardCard{
			Key:                iss.Key,
			Column:             string(iss.State),
			ColumnLabel:        StateLabel(iss.State),
			Title:              iss.Title,
			DescriptionExcerpt: descriptionExcerpt(iss.Description),
			Tags:               tags,
			Assignees:          assigneeList(iss.Assignee),
			Claude:             iss.Assignee == "claude",
			Taken:              e.taken,
			FeatureEmoji:       iss.FeatureEmoji,
			FeatureBranchName:  iss.FeatureBranchName,
			WaitingState:       ws,
			ActiveVerb:         e.verb,
			TodosDone:          e.todosDone,
			TodosTotal:         e.todosTotal,
			Todos:              e.todos,
			OpenQuestions:      e.questions,
			Archived:           iss.ArchivedAt != nil,
			BlockedBy:          blockedByID[iss.ID],
			TranscriptDocCount: transcriptCountsByID[iss.ID],
			EvalCommentCount:   evalCountsByID[iss.ID],
			TerminalAt:         iss.TerminalAt,
			FollowOn:           followOn,
			LatestPlan:         boardCardLatestPlan(latestPlanByID[iss.ID]),
			LatestPR:           boardCardLatestPR(latestPRByID[iss.ID]),
		})
	}

	// BACI-101: Done and Cancelled columns render newest-completed first.
	// Stable sort with a "same completed column only" comparator so
	// non-completed columns keep their incoming creation order, and ties
	// within Done/Cancelled fall back to creation order naturally. The
	// flat slice mixes all columns — the web/desktop re-bucket by
	// `column` with an order-preserving filter, so a stable sort here is
	// correct for them too.
	issueByKey := make(map[string]*model.Issue, len(issues))
	for _, iss := range issues {
		issueByKey[iss.Key] = iss
	}
	sort.SliceStable(cards, func(i, j int) bool {
		ci, cj := cards[i], cards[j]
		if ci.Column != cj.Column {
			return false // different columns: leave global order alone
		}
		if !IsCompletedColumn(model.State(ci.Column)) {
			return false // non-completed column: keep creation order
		}
		ii, ij := issueByKey[ci.Key], issueByKey[cj.Key]
		if ii == nil || ij == nil {
			return false
		}
		return CompletionSortKey(ii).After(CompletionSortKey(ij))
	})
	return cards, nil
}

func assigneeList(a string) []string {
	if a == "" {
		return []string{}
	}
	return []string{a}
}

// descriptionExcerpt (BACI-171) returns a short summary of `body` for
// the ActivityTray entry's two-line description. Whitespace runs
// (including newlines, common in markdown bodies) collapse to single
// spaces so the tray entry reads as flowing text rather than the raw
// markdown's hard wraps. When the collapsed body exceeds the rune cap
// the cut is moved back to the nearest preceding space so we don't
// split a word, and a "…" is appended. Returns "" for an empty or
// whitespace-only body so the BoardCard's omitempty drops the field
// from the wire payload.
func descriptionExcerpt(body string) string {
	collapsed := strings.Join(strings.Fields(body), " ")
	if collapsed == "" {
		return ""
	}
	if utf8.RuneCountInString(collapsed) <= descriptionExcerptRunes {
		return collapsed
	}
	// Walk the rune sequence up to the cap and remember the last index
	// at a word boundary (a space). Cutting on that boundary keeps the
	// excerpt readable; if we never see a space (a single very long
	// token), fall back to the hard rune cap so we still truncate.
	runes := 0
	cut := 0
	lastSpace := 0
	for i, r := range collapsed {
		if runes >= descriptionExcerptRunes {
			cut = i
			break
		}
		if r == ' ' {
			lastSpace = i
		}
		runes++
	}
	if cut == 0 {
		// Defensive — caller logic above means cut should always be set
		// once we exceed the cap, but if the byte/rune accounting ever
		// drifts return the full string rather than panic.
		return collapsed
	}
	if lastSpace > 0 {
		cut = lastSpace
	}
	return strings.TrimRight(collapsed[:cut], " ") + "…"
}

type cardEnrichment struct {
	taken      bool
	verb       string
	todosDone  int
	todosTotal int
	// todos are the winning-claim session's TodoWrite rows whose
	// issue_key matches this card's issue, in storage order
	// (Position ascending). Same per-(session, issue) scoping as the
	// counts above — a session that won this card after another
	// won't leak the prior job's rows here.
	todos []BoardCardTodo
	// questions are the winning-claim session's open ask_user_question
	// rows whose issue_key matches this card's issue. Empty when the
	// agent isn't blocked on anything for this issue.
	questions []BoardCardQuestion
}

// enrichmentByIssueKey resolves the per-issue agent-derived fields:
// `taken`, `ActiveVerb`, and `TodosDone/Total`. When no claims are
// open the maps stay empty and only `taken` is consulted; the
// downstream loop falls back to BoardCard zero values for the rest.
//
// Algorithm: for each issue with at least one open claim, pick the
// newest claim (by ClaimedAt); look up that claim's session for its
// agent identity; find the newest non-cancelled dispatch for that
// (session, issue) pair (matching either by session_id or agent_id)
// from the supplied `allDispatches` slice (already read in Assemble so
// the BACI-130 delivered-flag derivation can share the same query);
// resolve the dispatch's mode slug to the prompt template's display
// label, lower-cased; read the session's TodoWrite mirror counts.
//
// templates is the shared prompt-template list the caller already
// fetched for WaitingState's ActionLabel — reusing it here avoids a
// second ListPromptTemplates round-trip.
func enrichmentByIssueKey(
	ctx context.Context,
	c client.Client,
	claims []*model.AgentClaim,
	allDispatches []*model.AgentDispatch,
	templates []*store.PromptTemplate,
) (map[string]cardEnrichment, error) {
	enrich := make(map[string]cardEnrichment, len(claims))
	if len(claims) == 0 {
		return enrich, nil
	}

	// Pick the newest open claim per issue — that's the one whose
	// session drives the verb + tasks display when multiple agents
	// have paired on one issue. Mirrors applyClaimAssignee's "last
	// claim wins" semantics.
	newestByIssue := make(map[string]*model.AgentClaim, len(claims))
	for _, cl := range claims {
		if cl == nil {
			continue
		}
		existing, ok := newestByIssue[cl.IssueKey]
		if !ok || cl.ClaimedAt.After(existing.ClaimedAt) {
			newestByIssue[cl.IssueKey] = cl
		}
	}

	// Mark every issue with an open claim as taken — independent of
	// whether we can derive a verb or todos for it.
	for key := range newestByIssue {
		enrich[key] = cardEnrichment{taken: true}
	}

	// Collect the session IDs we actually need to hydrate — only the
	// "newest claim per issue" set, so a card with three paired
	// agents only fetches the winning session's todos.
	wantSessionIDs := make(map[string]bool, len(newestByIssue))
	for _, cl := range newestByIssue {
		if cl.SessionID != "" {
			wantSessionIDs[cl.SessionID] = true
		}
	}

	sessions, err := c.ListAgentSessions(ctx, client.AgentSessionFilter{RegisteredOnly: true})
	if err != nil {
		return nil, err
	}
	sessionByID := make(map[string]*model.AgentSession, len(sessions))
	for _, s := range sessions {
		if wantSessionIDs[s.SessionID] {
			sessionByID[s.SessionID] = s
		}
	}

	// Bulk-read todos for the winning sessions only, scoped to each
	// (session, issue, dispatch) triple so a session that has worked
	// multiple issues only flows the current job's rows onto this
	// card (BACI-62) and two dispatches on the same issue get
	// separate task lists (BACI-132). The map is keyed by session PK
	// to match the storage layer; the per-issue rendering below picks
	// pair-by-pair via the sessionByID lookup.
	sessionIDList := make([]string, 0, len(wantSessionIDs))
	for id := range wantSessionIDs {
		sessionIDList = append(sessionIDList, id)
	}
	pairs := make([]store.SessionIssuePair, 0, len(newestByIssue))
	for issueKey, cl := range newestByIssue {
		if cl == nil || cl.SessionID == "" {
			continue
		}
		sess := sessionByID[cl.SessionID]
		dispatchID := pickActiveDispatchID(allDispatches, sess, issueKey)
		if dispatchID == nil {
			// No in-flight dispatch for this (session, issue) — every
			// task this card would show was either pre-BACI-132 or
			// orphaned at the hook. Skip the pair so the card renders
			// no tasks, matching how the activity pill goes blank
			// when pickActiveMode returns "".
			continue
		}
		pairs = append(pairs, store.SessionIssuePair{
			SessionID:  cl.SessionID,
			IssueKey:   issueKey,
			DispatchID: dispatchID,
		})
	}
	todosByPK, err := c.ListTodosBySessionsAndIssue(ctx, pairs)
	if err != nil {
		return nil, err
	}
	// Bulk-read open ask_user_question rows for the same winning
	// sessions so each card can surface a "? N — <header>" pill when
	// the agent posed a question against this card's issue.
	questionsByPK, err := c.ListOpenQuestionsBySessions(ctx, sessionIDList)
	if err != nil {
		return nil, err
	}

	// Prompt-template lookup — slug → display label (lower-cased).
	// A template renamed after dispatch keeps the old slug on the
	// dispatch row, so we tolerate "not found" and silently drop
	// the verb in that case (per CLAUDE.md's "treat an unrecognised
	// slug as removed, not error out"). `templates` is the shared
	// slice the assembler already fetched for WaitingState — no
	// second round-trip here.
	verbBySlug := make(map[string]string, len(templates))
	for _, t := range templates {
		if t == nil || t.Name == "" {
			continue
		}
		verbBySlug[t.Slug] = strings.ToLower(t.Name)
	}

	for issueKey, claim := range newestByIssue {
		sess := sessionByID[claim.SessionID]
		e := enrich[issueKey]
		if sess != nil {
			// todosByPK carries rows for every (session, issue) pair the
			// bulk reader was asked about — filter by this card's
			// issueKey so a session that wins multiple cards doesn't
			// leak the other card's progress into this one.
			for _, t := range todosByPK[sess.ID] {
				if t.IssueKey != issueKey {
					continue
				}
				e.todosTotal++
				if t.Status == model.TodoCompleted {
					e.todosDone++
				}
				e.todos = append(e.todos, BoardCardTodo{
					Content: t.Content,
					Status:  string(t.Status),
				})
			}
			if mode := pickActiveMode(allDispatches, sess, issueKey); mode != "" {
				if v, ok := verbBySlug[string(mode)]; ok {
					e.verb = v
				}
			}
			// Filter the session's open questions down to those whose
			// issue_key matches this card's issue. A session juggling
			// two issues should only light up each card with its own
			// open questions.
			for _, q := range questionsByPK[sess.ID] {
				if q.IssueKey != issueKey {
					continue
				}
				header := ""
				first := ""
				if len(q.Payload.Questions) > 0 {
					header = q.Payload.Questions[0].Header
					first = q.Payload.Questions[0].Question
				}
				e.questions = append(e.questions, BoardCardQuestion{
					ID:            q.ID,
					Header:        header,
					FirstQuestion: first,
					Count:         len(q.Payload.Questions),
					AskedAt:       q.AskedAt,
				})
			}
		}
		enrich[issueKey] = e
	}
	return enrich, nil
}

// activeDispatchByIssueID maps issue id → the issue's active (queued /
// pending / delivered) dispatch row. "Active" is the newest such row
// per issue — same shape as Store.WaitingDispatchForIssue, computed
// in-memory from the dispatches slice the caller already read.
// `allDispatches` is newest-first (RepoDispatches' contract), so the
// first matching row per issue wins. Cancelled / acked rows don't
// establish the "active" dispatch — the walker skips them and keeps
// scanning older rows for the same issue.
//
// Drives BACI-145 WaitingState derivation: the returned row carries
// the dispatch's mode + status, which DeriveWaitingState turns into
// the spinner's inline label.
func activeDispatchByIssueID(allDispatches []*model.AgentDispatch) map[int64]*model.AgentDispatch {
	out := make(map[int64]*model.AgentDispatch)
	for _, d := range allDispatches {
		if d == nil || d.IssueID == nil {
			continue
		}
		id := *d.IssueID
		if _, ok := out[id]; ok {
			continue
		}
		// BACI-209 / BACI-217: a dormant follow-on (queued + at least
		// one dormant gate set — parent-acks via
		// queued_after_dispatch_id, or blockers-clear via
		// queued_until_blockers_clear) is NOT the active dispatch.
		// It's the queued-behind-the-gate row; the spinner-label
		// deriver should look at the *parent* (when the issue is
		// already in flight) or render nothing (for a blocked-and-idle
		// card). Without this skip a blockers-clear row leaks through
		// as a `queued_no_agent` spinner on a card that is otherwise
		// dormant — the chip is the right surface, not the spinner.
		if d.Status == model.DispatchQueued && (d.QueuedAfterDispatchID != nil || d.QueuedUntilBlockersClear) {
			continue
		}
		switch d.Status {
		case model.DispatchQueued, model.DispatchPending, model.DispatchDelivered:
			out[id] = d
		}
		// Cancelled / acked rows don't establish the "active" dispatch
		// — keep walking newer-to-older for the same issue.
	}
	return out
}

// followOnByIssueID (BACI-182, BACI-217) maps issue id → the issue's
// dormant follow-on dispatch row (status=queued AND at least one of
// the two dormant flags set — parent-acks via
// queued_after_dispatch_id, or blockers-clear via
// queued_until_blockers_clear), or absent when no follow-on is queued.
// Walks the same `allDispatches` slice (newest-first per
// RepoDispatches' contract) so the first matching row per issue wins —
// mirrors activeDispatchByIssueID in shape and per-issue uniqueness.
//
// Drives BACI-182's kanban chevron-or-chip slot: a non-nil entry tells
// the renderer to paint the chip (replacing the chevron) and the chip
// carries the dispatch's mode + action label.
func followOnByIssueID(allDispatches []*model.AgentDispatch) map[int64]*model.AgentDispatch {
	out := make(map[int64]*model.AgentDispatch)
	for _, d := range allDispatches {
		if d == nil || d.IssueID == nil {
			continue
		}
		if d.Status != model.DispatchQueued {
			continue
		}
		if d.QueuedAfterDispatchID == nil && !d.QueuedUntilBlockersClear {
			continue
		}
		id := *d.IssueID
		if _, ok := out[id]; ok {
			continue
		}
		out[id] = d
	}
	return out
}

// DeriveWaitingState (BACI-145) is the pure per-card deriver that turns
// (issue, active dispatch, per-(mode, branch) in-flight counts,
// templates) into the WaitingState the kanban / TUI surface inline
// beside the spinner.
//
// Returns nil when the card isn't waiting (no active dispatch row).
// Otherwise:
//
//   - `delivered`: kind=WaitingDelivered, mode + action label from the
//     active dispatch's template.
//   - `queued` / `pending` with concurrency_limit > 0 AND
//     inflightByModeBase[(mode, branch)] >= limit: kind=WaitingQueuedBlocked.
//   - everything else: kind=WaitingQueuedNoAgent (the matcher just
//     hasn't bound it yet).
//
// BACI-227: the in-flight count is keyed per (mode, base_branch) so
// the deriver tracks the matcher's per-branch cap exactly — a card on
// feat/A is "blocked" only when feat/A's slot is full, not when some
// other branch's ship is in flight. The lookup folds the active
// dispatch's BaseBranch through model.DefaultBaseBranch ("main") when
// empty, mirroring branchOf in the dispatcher. The store-side
// InflightByModeBaseForRepo COALESCEs NULL → "" — so a row inserted
// before BACI-226 (no stamped value) doesn't match the resolved-main
// key. Read-after-BACI-226 rows always carry a stamped value, so this
// is a strictly transitional gap.
//
// When concurrency_limit is 0 (the default) the cap is "unlimited"
// and the deriver falls back to queued_no_agent — otherwise every
// queued dispatch in a default template would surface as blocked.
//
// Pure / no I/O — call freely in tests.
func DeriveWaitingState(
	iss *model.Issue,
	active *model.AgentDispatch,
	inflightByModeBase map[store.InflightKey]int,
	templates []*store.PromptTemplate,
) *WaitingState {
	if iss == nil {
		return nil
	}
	// BACI-255: the active dispatch row is the gate. Pre-BACI-255 this
	// branch consulted a denormalised issues.waiting_for_claim cache
	// that mirrored exactly the predicate the caller resolves above
	// (WaitingDispatchForIssue → status IN queued/pending/delivered).
	// The cache could drift — the BACI-252 stuck-spinner bug surfaced
	// when every dispatch row was settled but the flag was stuck on.
	// Reading the dispatch row directly removes the divergence by
	// construction.
	if active == nil {
		return nil
	}
	mode := active.Mode
	actionLabel := actionLabelForMode(mode, templates)
	switch active.Status {
	case model.DispatchDelivered:
		return &WaitingState{Kind: WaitingDelivered, Mode: mode, ActionLabel: actionLabel}
	case model.DispatchQueued, model.DispatchPending:
		// queued vs queued_blocked. The matcher gates a bind on the
		// per-(repo, mode, base_branch) inflight count; mirror that
		// predicate so the label doesn't lie. limit == 0 means "no cap"
		// — the deriver must not surface those as blocked.
		if limit := templateConcurrencyLimit(mode, templates); limit > 0 {
			branch := active.BaseBranch
			if branch == "" {
				branch = model.DefaultBaseBranch
			}
			key := store.InflightKey{Mode: mode, BaseBranch: branch}
			if inflightByModeBase[key] >= limit {
				return &WaitingState{Kind: WaitingQueuedBlocked, Mode: mode, ActionLabel: actionLabel}
			}
		}
		return &WaitingState{Kind: WaitingQueuedNoAgent, Mode: mode, ActionLabel: actionLabel}
	}
	// Unknown status / acked / cancelled rows aren't active. The
	// active-dispatch lookup filters those out, so this branch is
	// defensive — fall back to no-agent so the card isn't left
	// rendering an unlabelled spinner.
	return &WaitingState{Kind: WaitingQueuedNoAgent, Mode: mode, ActionLabel: actionLabel}
}

// actionLabelForMode returns the imperative action label of the
// template behind `mode` — the verb rendered on the dispatch button,
// "Ship it" / "Plan" / "Fix review". Empty when the mode is empty (an
// untyped dispatch — the channel's setup-dispatch creator is the
// canonical example) or when the slug doesn't resolve to a known
// template (a renamed-after-dispatch row). The fallback to the gerund-
// derivation rule mirrors what the React dispatch menu does so a
// user-created template's label stays in lockstep.
func actionLabelForMode(mode model.DispatchMode, templates []*store.PromptTemplate) string {
	if mode == "" {
		return ""
	}
	for _, t := range templates {
		if t == nil || t.Slug != string(mode) {
			continue
		}
		if t.ActionLabel != "" {
			return t.ActionLabel
		}
		// User-created template without an explicit override — derive
		// from the gerund display name via the same rule the dispatch
		// dropdown uses. Mirrors model.DeriveActionLabel.
		return model.DeriveActionLabel(t.Name)
	}
	// Slug not found in the templates list — fall back to the built-in
	// action label registry when we know it, else empty (the label
	// renderer drops the mode name from the sentence in that case).
	return model.BuiltinTemplateActionLabel(string(mode))
}

// templateConcurrencyLimit returns the per-(repo, mode) cap the matcher
// enforces for `mode`. 0 means "unlimited" — the deriver must not
// surface queued_blocked when the cap is 0 (otherwise the default
// 0-cap templates would tag every queued dispatch as blocked, which
// would be wrong). Unknown slugs default to 0 (unlimited).
func templateConcurrencyLimit(mode model.DispatchMode, templates []*store.PromptTemplate) int {
	if mode == "" {
		return 0
	}
	for _, t := range templates {
		if t == nil || t.Slug != string(mode) {
			continue
		}
		return t.ConcurrencyLimit
	}
	return 0
}

// WaitingStateLabel returns the user-facing label rendered next to the
// spinner on the kanban card / TUI / issue lock banner (BACI-145).
//
// The wording is the single source of truth for the Go-side surfaces
// (TUI board + brief consumers). The TS-side counterpart in
// `desktop/frontend/src/lib/waitingLabels.ts` mirrors this exactly —
// keep the two in sync when adding kinds or rewording an existing
// label.
func WaitingStateLabel(ws *WaitingState) string {
	if ws == nil {
		return ""
	}
	switch ws.Kind {
	case WaitingDelivered:
		if ws.ActionLabel != "" {
			return "Worker has the " + ws.ActionLabel + " job"
		}
		return "Worker is on this job"
	case WaitingQueuedBlocked:
		if ws.ActionLabel != "" {
			return "Waiting on " + ws.ActionLabel + " job to finish"
		}
		return "Waiting on prior job to finish"
	case WaitingQueuedNoAgent:
		return "Waiting for an available agent"
	}
	return ""
}

// pickActiveDispatch returns the most-recent non-cancelled dispatch
// targeting (session, issue) — the shared selection used by
// pickActiveMode (for the card's activity pill) and
// pickActiveDispatchID (for BACI-132's per-dispatch todo scope).
// Returns nil when no matching dispatch exists.
func pickActiveDispatch(dispatches []*model.AgentDispatch, sess *model.AgentSession, issueKey string) *model.AgentDispatch {
	if sess == nil {
		return nil
	}
	matches := make([]*model.AgentDispatch, 0)
	for _, d := range dispatches {
		if d == nil || d.IssueKey != issueKey {
			continue
		}
		if d.Status == model.DispatchCancelled {
			continue
		}
		if !targetsSession(d, sess) {
			continue
		}
		matches = append(matches, d)
	}
	if len(matches) == 0 {
		return nil
	}
	sort.SliceStable(matches, func(i, j int) bool {
		return matches[i].CreatedAt.After(matches[j].CreatedAt)
	})
	return matches[0]
}

// pickActiveMode returns the mode slug of the most recent dispatch
// for (session, issue) that's still in flight or settled (not
// cancelled). Used to label the card with the prompt-template
// behind the currently-claimed work. Returns "" when no matching
// dispatch exists (e.g. the agent claimed manually without a
// dispatch, or the most recent matching dispatch was cancelled).
func pickActiveMode(dispatches []*model.AgentDispatch, sess *model.AgentSession, issueKey string) model.DispatchMode {
	d := pickActiveDispatch(dispatches, sess, issueKey)
	if d == nil {
		return ""
	}
	return d.Mode
}

// pickActiveDispatchID is the ID-returning sibling of pickActiveMode
// — the BACI-132 lookup that scopes the kanban Tasks pill's todos to
// the active dispatch instead of merging every dispatch's rows for
// the same (session, issue). Returns nil when no matching dispatch
// exists, so callers can pass that through to the orphan path or
// suppress the card's task list entirely.
func pickActiveDispatchID(dispatches []*model.AgentDispatch, sess *model.AgentSession, issueKey string) *int64 {
	d := pickActiveDispatch(dispatches, sess, issueKey)
	if d == nil {
		return nil
	}
	id := d.ID
	return &id
}

// isCardBlockerOpen reports whether a blocker's state is one of the
// open states that still gate a card. Mirrors the `isOpenState`
// duplication already present in internal/api/handlers_plan.go and
// internal/client/local_feature.go — kept inline here rather than
// imported so internal/boardcards stays free of API / feature-pkg
// import-cycle risk. The three copies drift on cosmetic things
// (comment style) but the predicate itself stays in lockstep.
func isCardBlockerOpen(s model.State) bool {
	switch s {
	case model.StateDone, model.StateCancelled:
		return false
	}
	return true
}

// targetsSession reports whether a dispatch is aimed at this
// session — by the bare session id or the agent identity behind it.
// Mirrors agentcards.dispatchTargetsSession; kept private to this
// package to avoid an import cycle.
func targetsSession(d *model.AgentDispatch, s *model.AgentSession) bool {
	if d.TargetSessionID != "" && d.TargetSessionID == s.SessionID {
		return true
	}
	if d.TargetAgentID != nil && s.AgentID != nil && *d.TargetAgentID == *s.AgentID {
		return true
	}
	return false
}
