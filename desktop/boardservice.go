package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/git"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/sync"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// stateLabels maps each bacio issue state to a human-friendly column label.
var stateLabels = map[model.State]string{
	model.StateTodo:        "Todo",
	model.StateInProgress:  "In Progress",
	model.StateNeedsAction: "Needs Action",
	model.StateInReview:    "In Review",
	model.StateDone:        "Done",
	model.StateCancelled:   "Cancelled",
}

func stateLabel(s model.State) string {
	if l, ok := stateLabels[s]; ok {
		return l
	}
	return string(s)
}

// Board is one bacio repo, offered in the top-nav repository selector.
type Board struct {
	Prefix      string `json:"prefix"`
	Name        string `json:"name"`
	IssueCount  int    `json:"issueCount"`
	SyncEnabled bool   `json:"syncEnabled"`
}

// BoardColumn is one kanban column — one bacio issue state.
type BoardColumn struct {
	State string `json:"state"`
	Label string `json:"label"`
}

// BoardCard is a kanban card — one bacio issue, shaped for the imported UI kit.
type BoardCard struct {
	Key         string   `json:"key"`
	Column      string   `json:"column"`
	ColumnLabel string   `json:"columnLabel"`
	Title       string   `json:"title"`
	Tags        []string `json:"tags"`
	Assignees   []string `json:"assignees"`
	Claude      bool     `json:"claude"`
}

// CommentDTO is one issue comment.
type CommentDTO struct {
	Author    string    `json:"author"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"createdAt"`
}

// PRDTO is one attached pull request.
type PRDTO struct {
	URL string `json:"url"`
}

// IssueDetail is the issue-drawer payload for a single issue.
type IssueDetail struct {
	Key          string         `json:"key"`
	Column       string         `json:"column"`
	ColumnLabel  string         `json:"columnLabel"`
	Title        string         `json:"title"`
	Description  string         `json:"description"`
	Tags         []string       `json:"tags"`
	Assignees    []string       `json:"assignees"`
	Claude       bool           `json:"claude"`
	Comments     []CommentDTO   `json:"comments"`
	PullRequests []PRDTO        `json:"pullRequests"`
	Claimants    []ClaimantDTO  `json:"claimants"`
	// Taken is the derived "an agent is actively holding this" signal —
	// true iff Claimants has an open (unreleased) claim.
	Taken bool `json:"taken"`
}

// ClaimantDTO is one entry in an issue's claim history — a session that
// has claimed the issue, with the prompt it ran and whether the claim
// is still open.
type ClaimantDTO struct {
	SessionID  string     `json:"sessionId"`
	AgentName  string     `json:"agentName"`
	Prompt     string     `json:"prompt"`
	ClaimedAt  time.Time  `json:"claimedAt"`
	ReleasedAt *time.Time `json:"releasedAt"`
	Open       bool       `json:"open"`
}

// ClaimDTO is one open agent claim, shaped for the Agents screen.
type ClaimDTO struct {
	IssueKey  string    `json:"issueKey"`
	Prompt    string    `json:"prompt"`
	ClaimedAt time.Time `json:"claimedAt"`
}

// DispatchDTO is one queued dispatch — returned both inside an AgentCard
// (the agent's drill-down) and from DispatchIssue (the new write).
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

// AgentCard is one agent session shaped for the desktop Agents screen,
// carrying its open claims and the dispatches aimed at it so the
// frontend can render the drill-down without a second round trip.
type AgentCard struct {
	SessionID  string `json:"sessionId"`
	AgentName  string `json:"agentName"` // identity slug; "" if none
	Actor      string `json:"actor"`
	Model      string `json:"model"`
	Branch     string `json:"branch"`
	RepoPrefix string `json:"repoPrefix"`
	Status     string `json:"status"` // active | idle | ended
	// Busy is true while the session holds an open claim — orthogonal to
	// Status (a session can be active+busy or idle+busy). BusyIssue is
	// the issue key it's working, for a "busy (BACI-12)" label. A busy
	// session is not a valid dispatch target.
	Busy       bool          `json:"busy"`
	BusyIssue  string        `json:"busyIssue"`
	LastSeenAt time.Time     `json:"lastSeenAt"`
	Claims     []ClaimDTO    `json:"claims"`
	Dispatches []DispatchDTO `json:"dispatches"`
}

// BoardService is the Wails-bound API the kanban frontend talks to. It
// wraps a local bacio client.Client and reshapes its results into the
// DTOs the imported UI kit expects. Mostly read-only; DispatchIssue is
// the one mutation (queuing a dispatch for an agent).
type BoardService struct {
	client client.Client
}

func NewBoardService(c client.Client) *BoardService {
	return &BoardService{client: c}
}

func assigneeList(a string) []string {
	if a == "" {
		return []string{}
	}
	return []string{a}
}

func cardFromIssue(iss *model.Issue) BoardCard {
	tags := iss.Tags
	if tags == nil {
		tags = []string{}
	}
	return BoardCard{
		Key:         iss.Key,
		Column:      string(iss.State),
		ColumnLabel: stateLabel(iss.State),
		Title:       iss.Title,
		Tags:        tags,
		Assignees:   assigneeList(iss.Assignee),
		Claude:      iss.Assignee == "claude",
	}
}

// repoSyncEnabled reports whether the repo's working tree has git sync
// configured — a readable .bacio/config.yaml with a sync.remote set. Any
// read/parse failure (missing dir, broken config) counts as not-enabled.
func repoSyncEnabled(path string) bool {
	if path == "" {
		return false
	}
	cfg, err := sync.ReadProjectConfig(path)
	return err == nil && cfg.Sync.Remote != ""
}

// ListBoards returns every bacio repo as a sidebar board, with its issue count
// and whether git sync is configured for it.
func (b *BoardService) ListBoards() ([]Board, error) {
	ctx := context.Background()
	repos, err := b.client.ListRepos(ctx)
	if err != nil {
		return nil, err
	}
	boards := make([]Board, 0, len(repos))
	for _, r := range repos {
		issues, err := b.client.ListIssues(ctx, client.IssueFilter{Repo: r})
		if err != nil {
			return nil, err
		}
		boards = append(boards, Board{
			Prefix:      r.Prefix,
			Name:        r.Name,
			IssueCount:  len(issues),
			SyncEnabled: repoSyncEnabled(r.Path),
		})
	}
	return boards, nil
}

// AddRepository opens a native folder picker and registers the chosen git
// working tree as a bacio repo, returning it as a Board. A Board with an empty
// Prefix means the user cancelled the dialog. The picked folder may sit
// anywhere inside the repo — git.Detect walks up to the working-tree root.
func (b *BoardService) AddRepository() (Board, error) {
	path, err := application.Get().Dialog.OpenFile().
		CanChooseDirectories(true).
		CanChooseFiles(false).
		SetTitle("Add Repository — pick a git working tree").
		PromptForSingleSelection()
	if err != nil {
		return Board{}, err
	}
	if path == "" {
		return Board{}, nil // dialog cancelled
	}
	info, err := git.Detect(path)
	if err != nil {
		return Board{}, fmt.Errorf("%q is not inside a git repository", path)
	}
	ctx := context.Background()
	repo, _, err := b.client.EnsureRepo(ctx, info)
	if err != nil {
		return Board{}, err
	}
	issues, err := b.client.ListIssues(ctx, client.IssueFilter{Repo: repo})
	if err != nil {
		return Board{}, err
	}
	return Board{
		Prefix:      repo.Prefix,
		Name:        repo.Name,
		IssueCount:  len(issues),
		SyncEnabled: repoSyncEnabled(repo.Path),
	}, nil
}

// ListColumns returns the kanban columns — bacio's issue states, in order.
func (b *BoardService) ListColumns() ([]BoardColumn, error) {
	states := model.AllStates()
	cols := make([]BoardColumn, 0, len(states))
	for _, s := range states {
		cols = append(cols, BoardColumn{State: string(s), Label: stateLabel(s)})
	}
	return cols, nil
}

// ListCards returns issues as kanban cards — for one repo, or across every
// repo when repoPrefix is empty or "all".
func (b *BoardService) ListCards(repoPrefix string) ([]BoardCard, error) {
	ctx := context.Background()
	filter := client.IssueFilter{}
	if repoPrefix == "" || repoPrefix == "all" {
		filter.AllRepos = true
	} else {
		repo, err := b.client.GetRepoByPrefix(ctx, repoPrefix)
		if err != nil {
			return nil, err
		}
		filter.Repo = repo
	}
	issues, err := b.client.ListIssues(ctx, filter)
	if err != nil {
		return nil, err
	}
	cards := make([]BoardCard, 0, len(issues))
	for _, iss := range issues {
		cards = append(cards, cardFromIssue(iss))
	}
	return cards, nil
}

// GetIssue returns the full issue-drawer payload for one issue. repoPrefix
// may be empty or "all" — canonical issue keys (PREFIX-N) resolve without a
// repo context.
func (b *BoardService) GetIssue(repoPrefix, key string) (IssueDetail, error) {
	ctx := context.Background()
	var repo *model.Repo
	if repoPrefix != "" && repoPrefix != "all" {
		r, err := b.client.GetRepoByPrefix(ctx, repoPrefix)
		if err != nil {
			return IssueDetail{}, err
		}
		repo = r
	}
	view, err := b.client.ShowIssue(ctx, repo, key)
	if err != nil {
		return IssueDetail{}, err
	}
	iss := view.Issue
	tags := iss.Tags
	if tags == nil {
		tags = []string{}
	}
	comments := make([]CommentDTO, 0, len(view.Comments))
	for _, c := range view.Comments {
		comments = append(comments, CommentDTO{Author: c.Author, Body: c.Body, CreatedAt: c.CreatedAt})
	}
	prs := make([]PRDTO, 0, len(view.PullRequests))
	for _, p := range view.PullRequests {
		prs = append(prs, PRDTO{URL: p.URL})
	}
	claimants := make([]ClaimantDTO, 0, len(view.Claimants))
	for _, c := range view.Claimants {
		claimants = append(claimants, ClaimantDTO{
			SessionID:  c.SessionID,
			AgentName:  c.AgentName,
			Prompt:     c.Prompt,
			ClaimedAt:  c.ClaimedAt,
			ReleasedAt: c.ReleasedAt,
			Open:       c.ReleasedAt == nil,
		})
	}
	return IssueDetail{
		Key:          iss.Key,
		Column:       string(iss.State),
		ColumnLabel:  stateLabel(iss.State),
		Title:        iss.Title,
		Description:  iss.Description,
		Tags:         tags,
		Assignees:    assigneeList(iss.Assignee),
		Claude:       iss.Assignee == "claude",
		Comments:     comments,
		PullRequests: prs,
		Claimants:    claimants,
		Taken:        view.Taken,
	}, nil
}

func dispatchDTO(d *model.AgentDispatch) DispatchDTO {
	return DispatchDTO{
		ID:          d.ID,
		IssueKey:    d.IssueKey,
		TargetAgent: d.TargetAgentName,
		Mode:        string(d.Mode),
		Status:      string(d.Status),
		Payload:     d.Payload,
		CreatedBy:   d.CreatedBy,
		CreatedAt:   d.CreatedAt,
	}
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

// ListAgents returns the agent sessions for one repo (or every repo when
// repoPrefix is empty or "all"), each carrying its status, open claims,
// and the dispatches aimed at it.
func (b *BoardService) ListAgents(repoPrefix string) ([]AgentCard, error) {
	ctx := context.Background()
	filter := client.AgentSessionFilter{}
	var repos []*model.Repo
	if repoPrefix == "" || repoPrefix == "all" {
		rs, err := b.client.ListRepos(ctx)
		if err != nil {
			return nil, err
		}
		repos = rs
	} else {
		repo, err := b.client.GetRepoByPrefix(ctx, repoPrefix)
		if err != nil {
			return nil, err
		}
		filter.Repo = repo
		repos = []*model.Repo{repo}
	}

	sessions, err := b.client.ListAgentSessions(ctx, filter)
	if err != nil {
		return nil, err
	}

	// Gather dispatches across the in-scope repos once, then bucket them
	// onto each session.
	var allDispatches []*model.AgentDispatch
	for _, r := range repos {
		ds, err := b.client.RepoDispatches(ctx, r)
		if err != nil {
			return nil, err
		}
		allDispatches = append(allDispatches, ds...)
	}

	now := time.Now()
	cards := make([]AgentCard, 0, len(sessions))
	for _, s := range sessions {
		card := AgentCard{
			SessionID:  s.SessionID,
			AgentName:  s.AgentName,
			Actor:      s.Actor,
			Model:      s.Model,
			Branch:     s.Branch,
			RepoPrefix: s.RepoPrefix,
			Status:     model.SessionLiveness(s, now),
			LastSeenAt: s.LastSeenAt,
			Claims:     []ClaimDTO{},
			Dispatches: []DispatchDTO{},
		}
		view, err := b.client.ShowAgentSession(ctx, s.SessionID)
		if err != nil {
			return nil, err
		}
		var openClaims []*model.AgentClaim
		for _, c := range view.Claims {
			if c.ReleasedAt == nil {
				openClaims = append(openClaims, c)
				card.Claims = append(card.Claims, ClaimDTO{
					IssueKey: c.IssueKey, Prompt: c.Prompt, ClaimedAt: c.ClaimedAt,
				})
			}
		}
		// A session holding an open claim is busy — and an ended session
		// is never busy (its claims are auto-released on end, but guard
		// anyway in case of a stale read).
		if s.EndedAt == nil {
			card.Busy, card.BusyIssue = model.SessionBusy(openClaims)
		}
		for _, d := range allDispatches {
			if dispatchTargetsSession(d, s) {
				card.Dispatches = append(card.Dispatches, dispatchDTO(d))
			}
		}
		cards = append(cards, card)
	}
	return cards, nil
}

// DispatchIssue queues a dispatch for an agent: the target agent slug,
// the mode ("plan" / "implement" / ""), and an optional free-form note.
// repoPrefix may be empty or "all" — the prefix is then derived from the
// canonical issue key.
func (b *BoardService) DispatchIssue(repoPrefix, issueKey, agentName, mode, note string) (DispatchDTO, error) {
	ctx := context.Background()
	if _, err := model.ParseDispatchMode(mode); err != nil {
		return DispatchDTO{}, err
	}
	prefix := repoPrefix
	if prefix == "" || prefix == "all" {
		if i := strings.LastIndex(issueKey, "-"); i > 0 {
			prefix = issueKey[:i]
		}
	}
	repo, err := b.client.GetRepoByPrefix(ctx, prefix)
	if err != nil {
		return DispatchDTO{}, err
	}
	// Service-boundary guard: a busy agent isn't a valid dispatch target.
	// Reject here so a stale UI can't queue an undeliverable job — mirrors
	// the TUI confirmDispatch guard. The agent counts as busy only when
	// every one of its live sessions is busy; one free session is enough.
	cards, err := b.ListAgents(prefix)
	if err != nil {
		return DispatchDTO{}, err
	}
	var sawAgent, hasFreeSession bool
	var busyIssue string
	for _, c := range cards {
		if c.AgentName != agentName || c.Status == "ended" {
			continue
		}
		sawAgent = true
		if c.Busy {
			busyIssue = c.BusyIssue
		} else {
			hasFreeSession = true
		}
	}
	if sawAgent && !hasFreeSession {
		return DispatchDTO{}, fmt.Errorf("agent %s is busy (working %s) — wait until it's free", agentName, busyIssue)
	}
	d, err := b.client.CreateDispatch(ctx, repo, inputs.AgentDispatchInput{
		TargetAgent: agentName,
		IssueKey:    issueKey,
		Mode:        mode,
		Message:     note,
	}, false)
	if err != nil {
		return DispatchDTO{}, err
	}
	return dispatchDTO(d), nil
}
