// Package agentcards assembles the per-session "AgentCard" used by every
// UI Agents panel (TUI, desktop, web). The single source of truth lived
// in desktop/boardservice.go until BACI-50 moved it here so the bacio
// api can serve the same payload over REST without N+1 round trips from
// the browser.
//
// Assemble takes a client.Client and a repo filter (a single repo or
// nil for cross-repo). All store reads are bulked — one
// ListAgentSessions, one ListTodosBySessions, one ListIssues per repo,
// one RepoDispatches per repo, plus one ShowAgentSession per session
// (the per-session claim hydration). The 10s poll on either UI is one
// round trip per repo, and stays that way.
//
// JSON tags are camelCase to match the desktop's existing Wails-bound
// AgentCard shape so the desktop binding and the REST response are the
// same wire format — the web frontend can read either without reshape.
package agentcards

import (
	"context"
	"time"

	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
	"github.com/mrgeoffrich/bacio/internal/version"
)

// ClaimDTO is one open agent claim, shaped for the Agents screen.
// State is the claimed issue's current state — needed to derive the
// session's Waiting flag and to annotate each claim in the drill-down
// (e.g. "BACI-12 (needs action)").
type ClaimDTO struct {
	IssueKey  string    `json:"issueKey"`
	Prompt    string    `json:"prompt"`
	ClaimedAt time.Time `json:"claimedAt"`
	State     string    `json:"state"`
}

// SessionTodoDTO is one row of the agent's mirrored TodoWrite list,
// shaped for the Agents screen. Status is one of
// pending|in_progress|completed, surfaced verbatim so the frontend can
// pick its glyph. IssueKey carries the BACI-62 per-job scope so a
// future "history" pane can group prior-job todos without a second
// fetch; omitted in JSON when empty so the on-wire shape stays
// back-compatible for callers that don't care. DispatchID (BACI-132)
// carries the per-dispatch scope so the UI could one day group two
// dispatches on the same (session, issue) as separate task lists;
// omitted on pre-BACI-132 rows and orphan rows so the on-wire shape
// stays back-compatible.
type SessionTodoDTO struct {
	Content    string `json:"content"`
	Status     string `json:"status"`
	IssueKey   string `json:"issueKey,omitempty"`
	DispatchID *int64 `json:"dispatch_id,omitempty"`
}

// QuestionDTO is one open BACI-53 ask_user_question row — included
// inside an AgentCard so the desktop and web bundle render the
// "user input needed" badge and modal in one round trip.
//
// We don't ship the full QuestionPayload over the AgentCard payload —
// the UI fetches it via /agents/questions/{id} only when the user
// opens the modal. The bare ID + asked-at + a count of pending
// questions is what the badge needs.
type QuestionDTO struct {
	ID       int64     `json:"id"`
	IssueKey string    `json:"issueKey,omitempty"`
	Header   string    `json:"header"`
	AskedAt  time.Time `json:"askedAt"`
}

// DispatchDTO is one queued dispatch — included inside an AgentCard
// (the agent's drill-down).
type DispatchDTO struct {
	ID          int64     `json:"id"`
	IssueKey    string    `json:"issueKey"`
	TargetAgent string    `json:"targetAgent"`
	Mode        string    `json:"mode"`
	Status      string    `json:"status"`
	Payload     string    `json:"payload"`
	CreatedBy   string    `json:"createdBy"`
	CreatedAt   time.Time `json:"createdAt"`
	// NeedsRescue (BACI-190) is true when the dispatch's target
	// session has ended without the dispatch ever being acked — its
	// worker either died mid-job or shut down without replying. The
	// AgentsView surfaces a "Rescue" button on rows with this flag.
	// Only typed per-mode dispatches qualify; rescue / setup / ping
	// dispatches are excluded.
	NeedsRescue bool `json:"needsRescue"`
}

// AgentCard is one agent session shaped for the Agents screen, carrying
// its open claims and the dispatches aimed at it so the frontend can
// render the drill-down without a second round trip.
//
// Field tags match the desktop's pre-BACI-50 wire format so the web
// bundle's TS AgentCard type (in api.http.ts) is structurally
// identical to the desktop's generated binding.
type AgentCard struct {
	SessionID  string `json:"sessionId"`
	AgentName  string `json:"agentName"`
	Actor      string `json:"actor"`
	Model      string `json:"model"`
	Branch     string `json:"branch"`
	RepoPrefix string `json:"repoPrefix"`
	Status     string `json:"status"`
	// Busy is true while the session holds an open claim — orthogonal to
	// Status (a session can be active+busy or idle+busy). BusyIssue is
	// the issue key it's working, for a "busy (BACI-12)" label. A busy
	// session is not a valid dispatch target.
	Busy      bool   `json:"busy"`
	BusyIssue string `json:"busyIssue"`
	// Waiting is true while the session holds an open claim on an issue
	// in needs_action — the derived "parked, waiting on the user"
	// signal. The Stop hook auto-flips a claimed in_progress issue to
	// needs_action on idle, so this lights up automatically. A waiting
	// session is also busy; the UI renders the waiting badge in place
	// of busy because it's the actionable state.
	Waiting      bool   `json:"waiting"`
	WaitingIssue string `json:"waitingIssue"`
	// HasChannel is true when the bacio channel MCP server has been seen
	// running alongside this session. Only sessions with a live channel
	// can receive push dispatches — sessions without one are interactive
	// (the user hasn't granted channel permission) and should be skipped.
	HasChannel        bool          `json:"hasChannel"`
	BacioVersion      string        `json:"bacioVersion"`
	BacioVersionStale bool          `json:"bacioVersionStale"`
	LastSeenAt        time.Time     `json:"lastSeenAt"`
	Claims            []ClaimDTO    `json:"claims"`
	Dispatches        []DispatchDTO `json:"dispatches"`
	// Todos mirrors the agent's TodoWrite list (BACI-45). TodosDone /
	// TodosTotal are pre-computed server-side so the UI doesn't reduce
	// the array twice per render.
	Todos      []SessionTodoDTO `json:"todos"`
	TodosDone  int              `json:"todosDone"`
	TodosTotal int              `json:"todosTotal"`
	// OpenQuestions are the BACI-53 ask_user_question rows the agent
	// posed and the user hasn't answered or dismissed yet. Drives the
	// "user input needed" badge and modal trigger in the Agents view.
	OpenQuestions []QuestionDTO `json:"openQuestions"`
}

// Assemble returns one AgentCard per registered session in scope.
// When repo is nil, every repo's sessions, dispatches and issue states
// are included (the cross-repo Agents view). When repo is non-nil,
// only that repo's sessions and dispatches are walked. SessionStart
// stubs that never completed register are hidden — they're noise in
// the supervision UI.
func Assemble(ctx context.Context, c client.Client, repo *model.Repo) ([]AgentCard, error) {
	filter := client.AgentSessionFilter{RegisteredOnly: true}
	var repos []*model.Repo
	if repo == nil {
		rs, err := c.ListRepos(ctx)
		if err != nil {
			return nil, err
		}
		repos = rs
	} else {
		filter.Repo = repo
		repos = []*model.Repo{repo}
	}

	sessions, err := c.ListAgentSessions(ctx, filter)
	if err != nil {
		return nil, err
	}

	// Hydrate each session's claims once up front — the per-session
	// view drives both the AgentCard.Claims drill-down and the
	// per-(session, issue) scope BACI-62 uses to bulk-read the right
	// todo subset for each card.
	sessionIDs := make([]string, 0, len(sessions))
	viewBySession := make(map[string]*client.AgentSessionView, len(sessions))
	for _, s := range sessions {
		sessionIDs = append(sessionIDs, s.SessionID)
		view, err := c.ShowAgentSession(ctx, s.SessionID)
		if err != nil {
			return nil, err
		}
		viewBySession[s.SessionID] = view
	}

	// Gather dispatches across the in-scope repos once. Hoisted ahead
	// of the todo pair construction so the BACI-132 per-dispatch
	// scope can be resolved up front; downstream code still buckets
	// the same slice onto each session.
	var allDispatches []*model.AgentDispatch
	for _, r := range repos {
		ds, err := c.RepoDispatches(ctx, r)
		if err != nil {
			return nil, err
		}
		allDispatches = append(allDispatches, ds...)
	}

	// Build (session, issue, dispatch) triples so the todo bulk-read
	// flows only the active dispatch's rows onto each card (BACI-132).
	// If no in-flight dispatch matches a (session, issue) we skip the
	// pair — the orphan / pre-migration rows the card would otherwise
	// pick up have been stamped with NULL dispatch_id and fall out of
	// the wider filter.
	pairs := make([]store.SessionIssuePair, 0, len(sessions))
	for _, s := range sessions {
		view := viewBySession[s.SessionID]
		issueKey := newestOpenClaimIssueKey(view)
		dispatchID := pickActiveDispatchID(allDispatches, s, issueKey)
		if dispatchID == nil {
			// No in-flight dispatch — render the card with an empty
			// task list. Matches the kanban-card behaviour and the
			// blank activity pill an unrecognised dispatch produces.
			continue
		}
		pairs = append(pairs, store.SessionIssuePair{
			SessionID:  s.SessionID,
			IssueKey:   issueKey,
			DispatchID: dispatchID,
		})
	}

	// Bulk-read each session's TodoWrite mirror in one query, scoped
	// to the (session, current-job-issue, active-dispatch) triple so
	// a session that's handled multiple dispatches only flows the
	// active dispatch's rows onto its card — keeps the 10s poll to
	// one round trip per repo.
	todosByPK, err := c.ListTodosBySessionsAndIssue(ctx, pairs)
	if err != nil {
		return nil, err
	}

	// Bulk-read every session's open BACI-53 questions in one query
	// — same shape as the todos read so the 10s poll stays a
	// single round trip per repo.
	questionsByPK, err := c.ListOpenQuestionsBySessions(ctx, sessionIDs)
	if err != nil {
		return nil, err
	}

	// Build a key→state lookup of every non-terminal issue across the
	// in-scope repos in one bulk read per repo, so each claim can
	// carry its issue's current state without a per-claim round trip.
	// The derived Waiting flag reads from this map; populating
	// ClaimDTO.State from the same source means a card can render
	// "BACI-12 (needs action)" in the drill-down for free.
	issueState := make(map[string]model.State)
	for _, r := range repos {
		issues, err := c.ListIssues(ctx, client.IssueFilter{
			Repo: r,
			States: []model.State{
				model.StateTodo, model.StateInProgress,
				model.StateNeedsAction, model.StateInReview,
			},
		})
		if err != nil {
			return nil, err
		}
		for _, iss := range issues {
			issueState[iss.Key] = iss.State
		}
	}
	needsAction := make(map[string]bool)
	for k, st := range issueState {
		if st == model.StateNeedsAction {
			needsAction[k] = true
		}
	}

	now := time.Now()
	cards := make([]AgentCard, 0, len(sessions))
	for _, s := range sessions {
		current := version.String()
		stale := s.ChannelVersion != "" &&
			current != "" && current != "dev" &&
			s.ChannelVersion != current
		sessTodos := todosByPK[s.ID]
		todosDTO := make([]SessionTodoDTO, 0, len(sessTodos))
		todosDone := 0
		for _, t := range sessTodos {
			todosDTO = append(todosDTO, SessionTodoDTO{
				Content:    t.Content,
				Status:     string(t.Status),
				IssueKey:   t.IssueKey,
				DispatchID: t.DispatchID,
			})
			if t.Status == model.TodoCompleted {
				todosDone++
			}
		}
		sessQuestions := questionsByPK[s.ID]
		questionsDTO := make([]QuestionDTO, 0, len(sessQuestions))
		for _, q := range sessQuestions {
			header := ""
			if len(q.Payload.Questions) > 0 {
				header = q.Payload.Questions[0].Header
			}
			questionsDTO = append(questionsDTO, QuestionDTO{
				ID:       q.ID,
				IssueKey: q.IssueKey,
				Header:   header,
				AskedAt:  q.AskedAt,
			})
		}
		card := AgentCard{
			SessionID:         s.SessionID,
			AgentName:         s.AgentName,
			Actor:             s.Actor,
			Model:             s.Model,
			Branch:            s.Branch,
			RepoPrefix:        s.RepoPrefix,
			Status:            model.SessionLiveness(s, now),
			HasChannel:        s.ChannelSeenAt != nil,
			BacioVersion:      s.ChannelVersion,
			BacioVersionStale: stale,
			LastSeenAt:        s.LastSeenAt,
			Claims:            []ClaimDTO{},
			Dispatches:        []DispatchDTO{},
			Todos:             todosDTO,
			TodosDone:         todosDone,
			TodosTotal:        len(todosDTO),
			OpenQuestions:     questionsDTO,
		}
		view := viewBySession[s.SessionID]
		var openClaims []*model.AgentClaim
		for _, cl := range view.Claims {
			if cl.ReleasedAt == nil {
				openClaims = append(openClaims, cl)
				card.Claims = append(card.Claims, ClaimDTO{
					IssueKey:  cl.IssueKey,
					Prompt:    cl.Prompt,
					ClaimedAt: cl.ClaimedAt,
					State:     string(issueState[cl.IssueKey]),
				})
			}
		}
		// A session holding an open claim is busy — and an ended
		// session is never busy (its claims are auto-released on end,
		// but guard anyway in case of a stale read).
		if s.EndedAt == nil {
			card.Busy, card.BusyIssue = model.SessionBusy(openClaims)
			card.Waiting, card.WaitingIssue = model.SessionWaiting(openClaims, needsAction)
		}
		for _, d := range allDispatches {
			if dispatchTargetsSession(d, s) {
				card.Dispatches = append(card.Dispatches, DispatchDTO{
					ID:          d.ID,
					IssueKey:    d.IssueKey,
					TargetAgent: d.TargetAgentName,
					Mode:        string(d.Mode),
					Status:      string(d.Status),
					Payload:     d.Payload,
					CreatedBy:   d.CreatedBy,
					CreatedAt:   d.CreatedAt,
					NeedsRescue: dispatchNeedsRescue(d, s),
				})
			}
		}
		cards = append(cards, card)
	}
	return cards, nil
}

// newestOpenClaimIssueKey returns the issue key of the most-recently
// claimed open issue on the session view, or "" when no open claim is
// present. Drives the per-(session, issue) todo lookup so a paired
// session shows the winning claim's job on its card (mirrors
// boardcards' "last claim wins" semantics).
func newestOpenClaimIssueKey(view *client.AgentSessionView) string {
	if view == nil {
		return ""
	}
	var newest *model.AgentClaim
	for _, cl := range view.Claims {
		if cl == nil || cl.ReleasedAt != nil {
			continue
		}
		if newest == nil || cl.ClaimedAt.After(newest.ClaimedAt) {
			newest = cl
		}
	}
	if newest == nil {
		return ""
	}
	return newest.IssueKey
}

// dispatchTargetsSession reports whether a dispatch is aimed at this
// session — by the bare session id or the agent identity behind it.
func dispatchTargetsSession(d *model.AgentDispatch, s *model.AgentSession) bool {
	if d.TargetSessionID != "" && d.TargetSessionID == s.SessionID {
		return true
	}
	if d.TargetAgentID != nil && s.AgentID != nil && *d.TargetAgentID == *s.AgentID {
		return true
	}
	return false
}

// dispatchNeedsRescue reports whether the dispatch's target session
// has ended without the dispatch ever being acked — the BACI-190
// signal the AgentsView uses to render a Rescue button. Three gates
// fire here: (1) the session is ended; (2) the dispatch is still
// unacked (pending or delivered) — a `queued` row has no target yet,
// an `acked` row is done, a `cancelled` row was withdrawn; (3) the
// creator is a real per-mode caller — setup / ping / rescue
// dispatches are excluded so a stale ping on a dead session and a
// running rescue itself don't sprout their own rescue buttons.
func dispatchNeedsRescue(d *model.AgentDispatch, s *model.AgentSession) bool {
	if d == nil || s == nil {
		return false
	}
	if s.EndedAt == nil {
		return false
	}
	if d.Status != model.DispatchPending && d.Status != model.DispatchDelivered {
		return false
	}
	switch d.CreatedBy {
	case model.SetupDispatchCreator, model.IdlePingDispatchCreator, model.RescueDispatchCreator:
		return false
	}
	return true
}

// pickActiveDispatchID returns the id of the newest non-cancelled
// dispatch targeting (session, issue) — the BACI-132 per-dispatch
// scope used to scope this card's TodoWrite mirror to the active
// dispatch instead of merging every dispatch's rows for the same
// (session, issue). Mirrors boardcards.pickActiveDispatchID; kept
// duplicated here to avoid an import-cycle into internal/boardcards
// (matches the existing dispatchTargetsSession duplication).
func pickActiveDispatchID(dispatches []*model.AgentDispatch, s *model.AgentSession, issueKey string) *int64 {
	if s == nil || issueKey == "" {
		return nil
	}
	var best *model.AgentDispatch
	for _, d := range dispatches {
		if d == nil || d.IssueKey != issueKey {
			continue
		}
		if d.Status == model.DispatchCancelled {
			continue
		}
		if !dispatchTargetsSession(d, s) {
			continue
		}
		if best == nil || d.CreatedAt.After(best.CreatedAt) {
			best = d
		}
	}
	if best == nil {
		return nil
	}
	id := best.ID
	return &id
}
