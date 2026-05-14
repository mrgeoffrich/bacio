package main

import (
	"context"
	"time"

	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/model"
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
	Prefix     string `json:"prefix"`
	Name       string `json:"name"`
	IssueCount int    `json:"issueCount"`
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
	Key          string       `json:"key"`
	Column       string       `json:"column"`
	ColumnLabel  string       `json:"columnLabel"`
	Title        string       `json:"title"`
	Description  string       `json:"description"`
	Tags         []string     `json:"tags"`
	Assignees    []string     `json:"assignees"`
	Claude       bool         `json:"claude"`
	Comments     []CommentDTO `json:"comments"`
	PullRequests []PRDTO      `json:"pullRequests"`
}

// BoardService is the Wails-bound read API the kanban frontend talks to. It
// wraps a local bacio client.Client and reshapes its results into the DTOs
// the imported UI kit expects. Read-only for now — mutations are a follow-up.
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

// ListBoards returns every bacio repo as a sidebar board, with its issue count.
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
			Prefix:     r.Prefix,
			Name:       r.Name,
			IssueCount: len(issues),
		})
	}
	return boards, nil
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
	}, nil
}
