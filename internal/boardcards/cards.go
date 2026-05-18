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

// Assemble returns one BoardCard per issue in scope. When repo is
// nil, every tracked repo's issues are merged (the cross-repo "all"
// board the desktop sidebar uses). The agent-derived enrichment
// (Taken, ActiveVerb, TodosDone/Total) is computed by joining the
// open claims onto their sessions and the most recent dispatch for
// each (session, issue) pair, then mapped by issue key.
func Assemble(ctx context.Context, c client.Client, repo *model.Repo) ([]BoardCard, error) {
	filter := client.IssueFilter{}
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

	enrichByKey, err := enrichmentByIssueKey(ctx, c, repos, claims)
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
			Key:             iss.Key,
			Column:          string(iss.State),
			ColumnLabel:     StateLabel(iss.State),
			Title:           iss.Title,
			Tags:            tags,
			Assignees:       assigneeList(iss.Assignee),
			Claude:          iss.Assignee == "claude",
			Taken:           e.taken,
			WaitingForClaim: iss.WaitingForClaim,
			ActiveVerb:      e.verb,
			TodosDone:       e.todosDone,
			TodosTotal:      e.todosTotal,
			OpenQuestions:   e.questions,
		})
	}
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
// (session, issue) pair (matching either by session_id or agent_id);
// resolve the dispatch's mode slug to the prompt template's display
// label, lower-cased; read the session's TodoWrite mirror counts.
func enrichmentByIssueKey(
	ctx context.Context,
	c client.Client,
	repos []*model.Repo,
	claims []*model.AgentClaim,
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
	// (session, issue) pair so a session that has worked multiple
	// issues only flows the current job's rows onto this card
	// (BACI-62). The map is keyed by session PK to match the storage
	// layer; the per-issue rendering below picks pair-by-pair via the
	// sessionByID lookup.
	sessionIDList := make([]string, 0, len(wantSessionIDs))
	for id := range wantSessionIDs {
		sessionIDList = append(sessionIDList, id)
	}
	pairs := make([]store.SessionIssuePair, 0, len(newestByIssue))
	for issueKey, cl := range newestByIssue {
		if cl == nil || cl.SessionID == "" {
			continue
		}
		pairs = append(pairs, store.SessionIssuePair{
			SessionID: cl.SessionID,
			IssueKey:  issueKey,
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

	// Dispatches are scoped per repo — fan-out over the in-scope
	// repos and concat. RepoDispatches is already newest-first.
	var allDispatches []*model.AgentDispatch
	for _, r := range repos {
		ds, err := c.RepoDispatches(ctx, r)
		if err != nil {
			return nil, err
		}
		allDispatches = append(allDispatches, ds...)
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

// pickActiveMode returns the mode slug of the most recent dispatch
// for (session, issue) that's still in flight or settled (not
// cancelled). Used to label the card with the prompt-template
// behind the currently-claimed work. Returns "" when no matching
// dispatch exists (e.g. the agent claimed manually without a
// dispatch, or the most recent matching dispatch was cancelled).
func pickActiveMode(dispatches []*model.AgentDispatch, sess *model.AgentSession, issueKey string) model.DispatchMode {
	if sess == nil {
		return ""
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
		return ""
	}
	sort.SliceStable(matches, func(i, j int) bool {
		return matches[i].CreatedAt.After(matches[j].CreatedAt)
	})
	return matches[0].Mode
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
