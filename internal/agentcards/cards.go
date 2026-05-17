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
// pick its glyph.
type SessionTodoDTO struct {
	Content string `json:"content"`
	Status  string `json:"status"`
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

	// Bulk-read each session's TodoWrite mirror in one query, then fan
	// back out by session PK — mirrors the OpenClaimsBySession
	// hydration pattern so the 10s poll stays a single round trip per
	// repo.
	sessionIDs := make([]string, 0, len(sessions))
	for _, s := range sessions {
		sessionIDs = append(sessionIDs, s.SessionID)
	}
	todosByPK, err := c.ListTodosBySessions(ctx, sessionIDs)
	if err != nil {
		return nil, err
	}

	// Gather dispatches across the in-scope repos once, then bucket
	// them onto each session below.
	var allDispatches []*model.AgentDispatch
	for _, r := range repos {
		ds, err := c.RepoDispatches(ctx, r)
		if err != nil {
			return nil, err
		}
		allDispatches = append(allDispatches, ds...)
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
				Content: t.Content,
				Status:  string(t.Status),
			})
			if t.Status == model.TodoCompleted {
				todosDone++
			}
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
		}
		view, err := c.ShowAgentSession(ctx, s.SessionID)
		if err != nil {
			return nil, err
		}
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
				})
			}
		}
		cards = append(cards, card)
	}
	return cards, nil
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
