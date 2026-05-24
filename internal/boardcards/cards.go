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
	"sort"
	"strings"
	"time"

	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

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

// BoardCard is one kanban card — one bacio issue, shaped for the
// imported UI kit. Fields beyond the issue itself: Taken (an agent
// holds an open claim on this issue), WaitingForClaim (a dispatch is
// queued but no claim yet), ActiveVerb (the lower-cased
// prompt-template label of the newest open claim's dispatch — e.g.
// "designing", "planning"), and TodosDone / TodosTotal (the TodoWrite
// progress of the claiming session).
type BoardCard struct {
	Key             string   `json:"key"`
	Column          string   `json:"column"`
	ColumnLabel     string   `json:"columnLabel"`
	Title           string   `json:"title"`
	Tags            []string `json:"tags"`
	Assignees       []string `json:"assignees"`
	Claude          bool     `json:"claude"`
	Taken           bool     `json:"taken"`
	WaitingForClaim bool     `json:"waitingForClaim"`
	// WaitingDispatchDelivered (BACI-130) is true when the issue's
	// active (queued / pending / delivered) dispatch is already in the
	// `delivered` state — the worker has the Task in hand. Drives the
	// spinner-cancel UI gate: cards with this flag set render the
	// spinner glyph without the click affordance, because cancelling a
	// delivered dispatch is now rejected at the store boundary.
	// Omitted from JSON when false (common case) so the wire payload
	// doesn't grow on untaken / queued / pending cards.
	WaitingDispatchDelivered bool `json:"waitingDispatchDelivered,omitempty"`
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
func Assemble(ctx context.Context, c client.Client, repo *model.Repo, includeArchived bool) ([]BoardCard, error) {
	filter := client.IssueFilter{IncludeArchived: includeArchived}
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
	// (`pickActiveMode`) and the new per-issue `WaitingDispatchDelivered`
	// flag. The flag drives the kanban spinner-cancel UI: a card whose
	// active dispatch has been delivered hides the cancel affordance,
	// because cancelling a delivered dispatch is rejected at the store
	// boundary now. RepoDispatches is already newest-first.
	var allDispatches []*model.AgentDispatch
	for _, r := range repos {
		ds, err := c.RepoDispatches(ctx, r)
		if err != nil {
			return nil, err
		}
		allDispatches = append(allDispatches, ds...)
	}
	deliveredByIssueID := waitingDeliveredByIssueID(allDispatches)

	enrichByKey, err := enrichmentByIssueKey(ctx, c, claims, allDispatches)
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

	cards := make([]BoardCard, 0, len(issues))
	for _, iss := range issues {
		tags := iss.Tags
		if tags == nil {
			tags = []string{}
		}
		e := enrichByKey[iss.Key]
		cards = append(cards, BoardCard{
			Key:                      iss.Key,
			Column:                   string(iss.State),
			ColumnLabel:              StateLabel(iss.State),
			Title:                    iss.Title,
			Tags:                     tags,
			Assignees:                assigneeList(iss.Assignee),
			Claude:                   iss.Assignee == "claude",
			Taken:                    e.taken,
			WaitingForClaim:          iss.WaitingForClaim,
			WaitingDispatchDelivered: iss.WaitingForClaim && deliveredByIssueID[iss.ID],
			ActiveVerb:               e.verb,
			TodosDone:                e.todosDone,
			TodosTotal:               e.todosTotal,
			Todos:                    e.todos,
			OpenQuestions:            e.questions,
			Archived:                 iss.ArchivedAt != nil,
			BlockedBy:                blockedByID[iss.ID],
			TranscriptDocCount:       transcriptCountsByID[iss.ID],
			EvalCommentCount:         evalCountsByID[iss.ID],
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
func enrichmentByIssueKey(
	ctx context.Context,
	c client.Client,
	claims []*model.AgentClaim,
	allDispatches []*model.AgentDispatch,
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
	// slug as removed, not error out").
	templates, err := c.ListPromptTemplates(ctx)
	if err != nil {
		return nil, err
	}
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

// waitingDeliveredByIssueID maps issue id → true when the issue's
// active (queued / pending / delivered) dispatch is in the `delivered`
// state. Drives BoardCard.WaitingDispatchDelivered, which gates the
// kanban spinner-cancel UI (BACI-130): a card whose active dispatch
// has been delivered hides the cancel affordance because the store
// rejects cancel-after-delivery now.
//
// "Active" is the newest non-cancelled / non-acked dispatch per issue
// — same shape as Store.WaitingDispatchForIssue, computed in-memory
// from the dispatches slice the caller already read. `allDispatches`
// is newest-first (RepoDispatches' contract), so the first matching
// row per issue wins.
func waitingDeliveredByIssueID(allDispatches []*model.AgentDispatch) map[int64]bool {
	out := make(map[int64]bool)
	seen := make(map[int64]bool)
	for _, d := range allDispatches {
		if d == nil || d.IssueID == nil {
			continue
		}
		id := *d.IssueID
		if seen[id] {
			continue
		}
		switch d.Status {
		case model.DispatchQueued, model.DispatchPending:
			seen[id] = true
			out[id] = false
		case model.DispatchDelivered:
			seen[id] = true
			out[id] = true
		}
		// Cancelled / acked rows don't establish the "active" dispatch
		// — keep walking newer-to-older for the same issue.
	}
	return out
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
