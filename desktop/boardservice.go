package main

import (
	"context"
	"fmt"
	"os/user"
	"strings"
	"time"

	"github.com/mrgeoffrich/bacio/internal/agentcards"
	"github.com/mrgeoffrich/bacio/internal/boardcards"
	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/git"
	"github.com/mrgeoffrich/bacio/internal/model"
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
//
// The SyncEnabled / SyncInProgress / SyncLastAt / SyncLastError fields
// (BACI-89) drive the topbar's live sync-status badge: SyncEnabled is
// "this repo has git sync configured"; the other three reflect the
// background sync runner's last / current state.
type Board struct {
	Prefix         string     `json:"prefix"`
	Name           string     `json:"name"`
	IssueCount     int        `json:"issueCount"`
	SyncEnabled    bool       `json:"syncEnabled"`
	SyncInProgress bool       `json:"syncInProgress"`
	SyncLastAt     *time.Time `json:"syncLastAt,omitempty"`
	SyncLastError  string     `json:"syncLastError,omitempty"`
}

// BoardColumn is one kanban column — one bacio issue state.
type BoardColumn struct {
	State string `json:"state"`
	Label string `json:"label"`
}

// BoardCard is the kanban-card wire shape, hoisted into
// internal/boardcards so the bacio api can serve the same payload
// over REST (mirrors the BACI-50 agentcards extraction). The alias
// keeps the Wails-bound surface unchanged — the generated TS
// bindings still point at the same struct name from the desktop's
// perspective, so api.ts and components don't need to update their
// imports.
type BoardCard = boardcards.BoardCard

// CommentDTO is one issue comment. UUID is the immutable identity the
// React layer addresses a delete with.
//
// BACI-131 added the four eval-context fields the kanban quick-eval
// composer pins onto a comment at write time. They're zero values on
// a normal comment; AgentName is the persistent agent-identity slug
// resolved server-side via the agent_sessions → agents JOIN at read
// time (never persisted on the row).
type CommentDTO struct {
	UUID           string    `json:"uuid"`
	Author         string    `json:"author"`
	Body           string    `json:"body"`
	CreatedAt      time.Time `json:"createdAt"`
	Eval           bool      `json:"eval"`
	AgentSessionID string    `json:"agentSessionId,omitempty"`
	DispatchID     *int64    `json:"dispatchId,omitempty"`
	Mode           string    `json:"mode,omitempty"`
	AgentName      string    `json:"agentName,omitempty"`
	// TranscriptEventRef (BACI-141) pins this comment to a specific
	// event inside a `.jsonl` transcript when set, so the transcript
	// viewer can render it as an inline annotation next to the matching
	// event card. Empty = unanchored, rendered pinned to the dispatch
	// prompt card at the top of the transcript view.
	TranscriptEventRef string `json:"transcriptEventRef,omitempty"`
}

// PRDTO is one attached pull request.
type PRDTO struct {
	URL string `json:"url"`
}

// DocLinkDTO is one document linked to an issue, shaped for the drawer's
// attachments section.
type DocLinkDTO struct {
	Filename    string `json:"filename"`
	Type        string `json:"type"`
	Description string `json:"description"` // the link's --why reason
}

// LinkedDocDTO is the per-doc shape used by the IssueWorkspace — the
// drawer-shaped DocLinkDTO plus the body content + source path +
// linked-via origin (so a doc reachable from both the issue and its
// feature renders one panel with "(issue + feature)"). Kept separate
// from DocLinkDTO so the existing drawer / Kanban card payloads don't
// get fattened.
type LinkedDocDTO struct {
	Filename    string   `json:"filename"`
	Type        string   `json:"type"`
	Description string   `json:"description"`
	SourcePath  string   `json:"sourcePath,omitempty"`
	LinkedVia   []string `json:"linkedVia"`
	// SizeBytes is the doc's on-disk size in bytes. Always populated, even
	// when Content is omitted from the brief (BACI-115 keeps transcript
	// bodies out of the inlined payload but carries the size so the
	// LinkedDocPanel can render "body not inlined — N KB" instead of the
	// misleading "Document body is empty.").
	SizeBytes int64  `json:"sizeBytes"`
	Content   string `json:"content"`
}

// FeatureRefDTO is the lightweight feature reference attached to an
// issue brief — slug + title. Just enough to render the feature pill
// in the workspace rail without leaking the full FeatureDetail.
type FeatureRefDTO struct {
	Slug  string `json:"slug"`
	Title string `json:"title"`
}

// RelationDTO is one outgoing/incoming relation, resolved to the other
// end's issue key.
type RelationDTO struct {
	Type     string `json:"type"`     // blocks | relates_to | duplicate_of
	OtherKey string `json:"otherKey"` // the *other* end of the edge
}

// RelationsDTO splits an issue's relations into the outgoing edges
// (this issue → other) and incoming edges (other → this issue), each
// with the other end resolved to a key.
type RelationsDTO struct {
	Outgoing []RelationDTO `json:"outgoing"`
	Incoming []RelationDTO `json:"incoming"`
}

// IssueMetaDTO is the workspace's issue-header payload: the bits the
// rail and primary column read repeatedly without going through the
// rest of the brief. Mirrors the BoardCard shape (column + label +
// title + tags + assignees), plus the description and the derived
// taken / waitingForClaim flags so the IssueWorkspace doesn't have to
// hop between brief.issue.{...} and brief.{taken,waitingForClaim}.
type IssueMetaDTO struct {
	Key             string   `json:"key"`
	Column          string   `json:"column"`
	ColumnLabel     string   `json:"columnLabel"`
	Title           string   `json:"title"`
	Description     string   `json:"description"`
	Tags            []string `json:"tags"`
	Assignees       []string `json:"assignees"`
	Claude          bool     `json:"claude"`
	Taken           bool     `json:"taken"`
	WaitingForClaim bool     `json:"waitingForClaim"`
}

// IssueBriefDTO is the workspace-shaped payload — BoardService.GetIssueBrief's
// return value, mirroring internal/client/views.go::IssueBrief with desktop
// camelCase JSON tags. New fields over IssueDetail: feature, relations,
// waitingForClaim, warnings, and doc content (via LinkedDocDTO).
//
// BACI-145: WaitingState is the structured "why is this card waiting?"
// projection used by the IssueLockBanner — same shape and same wording
// as the kanban-card label. Nil when the issue isn't waiting; the
// banner falls back to its "taken" branch when an agent has the claim.
type IssueBriefDTO struct {
	Issue           IssueMetaDTO              `json:"issue"`
	Feature         *FeatureRefDTO            `json:"feature,omitempty"`
	Relations       RelationsDTO              `json:"relations"`
	PullRequests    []PRDTO                   `json:"pullRequests"`
	Documents       []LinkedDocDTO            `json:"documents"`
	Comments        []CommentDTO              `json:"comments"`
	Claimants       []ClaimantDTO             `json:"claimants"`
	Taken           bool                      `json:"taken"`
	WaitingForClaim bool                      `json:"waitingForClaim"`
	WaitingState    *boardcards.WaitingState  `json:"waitingState,omitempty"`
	Warnings        []string                  `json:"warnings"`
}

// IssueDetail is the issue-drawer payload for a single issue.
type IssueDetail struct {
	Key          string        `json:"key"`
	Column       string        `json:"column"`
	ColumnLabel  string        `json:"columnLabel"`
	Title        string        `json:"title"`
	Description  string        `json:"description"`
	Tags         []string      `json:"tags"`
	Assignees    []string      `json:"assignees"`
	Claude       bool          `json:"claude"`
	Comments     []CommentDTO  `json:"comments"`
	PullRequests []PRDTO       `json:"pullRequests"`
	Documents    []DocLinkDTO  `json:"documents"`
	Claimants    []ClaimantDTO `json:"claimants"`
	// Taken is the derived "an agent is actively holding this" signal —
	// true iff Claimants has an open (unreleased) claim.
	Taken bool `json:"taken"`
}

// ClaimantDTO, ClaimDTO, SessionTodoDTO, DispatchDTO, and AgentCard
// live in internal/agentcards so the bacio api can serve the same wire
// format (BACI-50) and the per-issue claimant mapper has one home
// (BACI-55). Aliases keep the Wails-bound surface unchanged — the
// generated TS bindings point at the same struct names from the
// desktop's perspective, so the existing api.ts and components don't
// need to update their imports.
type (
	ClaimantDTO    = agentcards.ClaimantDTO
	ClaimDTO       = agentcards.ClaimDTO
	SessionTodoDTO = agentcards.SessionTodoDTO
	DispatchDTO    = agentcards.DispatchDTO
	AgentCard      = agentcards.AgentCard
)

// BoardService is the Wails-bound API the kanban frontend talks to. It
// wraps a local bacio client.Client and reshapes its results into the
// DTOs the imported UI kit expects. Mostly read-only; the mutations are
// DispatchIssue (queuing a dispatch for an agent), UpdateIssueDescription,
// and AddComment (both driven from the issue-drawer Edit modal).
type BoardService struct {
	client client.Client
}

func NewBoardService(c client.Client) *BoardService {
	return &BoardService{client: c}
}

// resolveRepoForKey turns a repo prefix into a *model.Repo. When the prefix
// is empty or the "all" pseudo-board, the prefix is derived from the
// canonical issue key (PREFIX-N) instead.
func (b *BoardService) resolveRepoForKey(ctx context.Context, repoPrefix, key string) (*model.Repo, error) {
	prefix := repoPrefix
	if prefix == "" || prefix == "all" {
		if i := strings.LastIndex(key, "-"); i > 0 {
			prefix = key[:i]
		}
	}
	return b.client.GetRepoByPrefix(ctx, prefix)
}

func assigneeList(a string) []string {
	if a == "" {
		return []string{}
	}
	return []string{a}
}

// cardFromIssue produces a BoardCard for one issue without the
// agent-derived enrichment (ActiveVerb + TodosDone/Total or BACI-145
// WaitingState). Used by the single-issue refresh paths (SetIssueState
// after a drag) where the active dispatch / todos can't change as part
// of the operation — the next 10s poll re-runs ListCards and
// re-populates them. Dragging a waiting card is blocked by the UI
// anyway, so the missing WaitingState here doesn't cause a flicker on
// any real interaction.
func cardFromIssue(iss *model.Issue, taken bool) BoardCard {
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
		Taken:       taken,
	}
}

// ListBoards returns every bacio repo as a sidebar board, with its issue count
// and its BACI-89 background-sync status.
func (b *BoardService) ListBoards() ([]Board, error) {
	ctx := context.Background()
	repos, err := b.client.ListRepos(ctx)
	if err != nil {
		return nil, err
	}
	// Per-repo sync status, keyed by prefix. A failure here is
	// non-fatal — sync status is badge polish, not load-bearing.
	syncByPrefix := map[string]client.SyncStatus{}
	if statuses, serr := b.client.SyncStatuses(ctx); serr == nil {
		for _, st := range statuses {
			syncByPrefix[st.Prefix] = st
		}
	}
	boards := make([]Board, 0, len(repos))
	for _, r := range repos {
		issues, err := b.client.ListIssues(ctx, client.IssueFilter{Repo: r})
		if err != nil {
			return nil, err
		}
		boards = append(boards, boardWithSync(r, len(issues), syncByPrefix[r.Prefix]))
	}
	return boards, nil
}

// boardWithSync builds a Board from a repo, issue count, and the
// repo's sync status. Centralises the SyncEnabled / Sync* mapping so
// ListBoards and AddRepository stay in lockstep.
func boardWithSync(r *model.Repo, issueCount int, st client.SyncStatus) Board {
	bd := Board{
		Prefix:      r.Prefix,
		Name:        r.Name,
		IssueCount:  issueCount,
		SyncEnabled: st.Configured,
	}
	bd.SyncInProgress = st.InProgress
	bd.SyncLastAt = st.LastSyncAt
	if st.LastError != nil {
		bd.SyncLastError = *st.LastError
	}
	return bd
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
	// Freshly-added repo: sync is almost never configured yet, but
	// look it up anyway so an add-after-`bacio sync init` reflects
	// reality. A failure is non-fatal — fall back to the zero status.
	var st client.SyncStatus
	if statuses, serr := b.client.SyncStatuses(ctx); serr == nil {
		for _, s := range statuses {
			if s.Prefix == repo.Prefix {
				st = s
				break
			}
		}
	}
	return boardWithSync(repo, len(issues), st), nil
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
// repo when repoPrefix is empty or "all". Delegates to boardcards.Assemble
// so the desktop and REST surface share one assembler.
//
// Archived cards (BACI-68) follow the display.show_archived global
// setting — when on, archived rows surface as visibly-muted cards;
// when off (the default) they're hidden from the board entirely. The
// desktop has no per-call --include-archived knob — toggle the setting
// from the Settings panel.
func (b *BoardService) ListCards(repoPrefix string) ([]BoardCard, error) {
	ctx := context.Background()
	var repo *model.Repo
	if repoPrefix != "" && repoPrefix != "all" {
		r, err := b.client.GetRepoByPrefix(ctx, repoPrefix)
		if err != nil {
			return nil, err
		}
		repo = r
	}
	showArchived, _ := b.client.GetDisplayShowArchived(ctx)
	// BACI-177: load the per-repo board-hide set so cards belonging to
	// hidden features never ship over the wire. Skipped for the cross-
	// repo "all" pseudo-board — the hide set is per-repo, and the
	// cross-repo path goes through ListRepos in Assemble. (A failure
	// here is non-fatal — the board still renders, just without the
	// filter for this trip.)
	var hiddenSlugs []string
	if repo != nil {
		if slugs, herr := b.client.ListHiddenFeatureSlugs(ctx, repo); herr == nil {
			hiddenSlugs = slugs
		}
	}
	return boardcards.Assemble(ctx, b.client, repo, showArchived, hiddenSlugs)
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
		comments = append(comments, CommentDTO{
			UUID: c.UUID, Author: c.Author, Body: c.Body, CreatedAt: c.CreatedAt,
			Eval:               c.Eval,
			AgentSessionID:     c.AgentSessionID,
			DispatchID:         c.DispatchID,
			Mode:               c.Mode,
			AgentName:          c.AgentName,
			TranscriptEventRef: c.TranscriptEventRef,
		})
	}
	prs := make([]PRDTO, 0, len(view.PullRequests))
	for _, p := range view.PullRequests {
		prs = append(prs, PRDTO{URL: p.URL})
	}
	claimants := agentcards.MapClaimants(view.Claimants)
	docs := make([]DocLinkDTO, 0, len(view.Documents))
	for _, d := range view.Documents {
		docs = append(docs, DocLinkDTO{
			Filename:    d.DocumentFilename,
			Type:        string(d.DocumentType),
			Description: d.Description,
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
		Documents:    docs,
		Claimants:    claimants,
		Taken:        view.Taken,
	}, nil
}

// GetIssueBrief returns the workspace-shaped bulk payload for one
// issue — issue meta + feature reference + relations (both directions)
// + linked documents with body content (deduped between issue-link
// and feature-link sources) + pull requests + comments + claimants +
// derived taken / waitingForClaim flags. Backs the IssueWorkspace
// top-level view; replaces the drawer's GetIssue payload for that
// surface but doesn't retire GetIssue (the older payload still backs
// callers like the IssueDrawer until BACI-54's deletion step). The
// repo prefix may be empty or "all" — canonical issue keys (PREFIX-N)
// resolve without one.
func (b *BoardService) GetIssueBrief(repoPrefix, key string) (IssueBriefDTO, error) {
	ctx := context.Background()
	var repo *model.Repo
	if repoPrefix != "" && repoPrefix != "all" {
		r, err := b.client.GetRepoByPrefix(ctx, repoPrefix)
		if err != nil {
			return IssueBriefDTO{}, err
		}
		repo = r
	}
	brief, err := b.client.BriefIssue(ctx, repo, key, client.BriefOptions{})
	if err != nil {
		return IssueBriefDTO{}, err
	}
	iss := brief.Issue

	tags := iss.Tags
	if tags == nil {
		tags = []string{}
	}
	meta := IssueMetaDTO{
		Key:             iss.Key,
		Column:          string(iss.State),
		ColumnLabel:     stateLabel(iss.State),
		Title:           iss.Title,
		Description:     iss.Description,
		Tags:            tags,
		Assignees:       assigneeList(iss.Assignee),
		Claude:          iss.Assignee == "claude",
		Taken:           brief.Taken,
		WaitingForClaim: iss.WaitingForClaim,
	}

	var feat *FeatureRefDTO
	if brief.Feature != nil {
		feat = &FeatureRefDTO{Slug: brief.Feature.Slug, Title: brief.Feature.Title}
	}

	rels := RelationsDTO{Outgoing: []RelationDTO{}, Incoming: []RelationDTO{}}
	if brief.Relations != nil {
		for _, r := range brief.Relations.Outgoing {
			rels.Outgoing = append(rels.Outgoing, RelationDTO{Type: string(r.Type), OtherKey: r.ToIssue})
		}
		for _, r := range brief.Relations.Incoming {
			rels.Incoming = append(rels.Incoming, RelationDTO{Type: string(r.Type), OtherKey: r.FromIssue})
		}
	}

	prs := make([]PRDTO, 0, len(brief.PullRequests))
	for _, p := range brief.PullRequests {
		prs = append(prs, PRDTO{URL: p.URL})
	}

	docs := make([]LinkedDocDTO, 0, len(brief.Documents))
	for _, d := range brief.Documents {
		via := d.LinkedVia
		if via == nil {
			via = []string{}
		}
		docs = append(docs, LinkedDocDTO{
			Filename:    d.Filename,
			Type:        string(d.Type),
			Description: d.Description,
			SourcePath:  d.SourcePath,
			LinkedVia:   via,
			SizeBytes:   d.SizeBytes,
			Content:     d.Content,
		})
	}

	comments := make([]CommentDTO, 0, len(brief.Comments))
	for _, c := range brief.Comments {
		comments = append(comments, CommentDTO{
			UUID: c.UUID, Author: c.Author, Body: c.Body, CreatedAt: c.CreatedAt,
			Eval:               c.Eval,
			AgentSessionID:     c.AgentSessionID,
			DispatchID:         c.DispatchID,
			Mode:               c.Mode,
			AgentName:          c.AgentName,
			TranscriptEventRef: c.TranscriptEventRef,
		})
	}

	warnings := brief.Warnings
	if warnings == nil {
		warnings = []string{}
	}

	// BACI-145: derive WaitingState for the IssueLockBanner. Only worth
	// the round-trips when the issue is actually flagged as waiting;
	// otherwise the deriver returns nil regardless. The "all" pseudo-
	// board doesn't have a *model.Repo handy, so resolve it from the
	// issue's canonical key (PREFIX-N) for that case.
	var waitingState *boardcards.WaitingState
	if iss.WaitingForClaim {
		dispatchRepo := repo
		if dispatchRepo == nil {
			if r, err := b.resolveRepoForKey(ctx, repoPrefix, iss.Key); err == nil {
				dispatchRepo = r
			}
		}
		if dispatchRepo != nil {
			activeDispatch, derr := b.client.WaitingDispatchForIssue(ctx, dispatchRepo, iss.Key)
			if derr == nil {
				inflight, ierr := b.client.InflightByModeForRepo(ctx, dispatchRepo)
				if ierr != nil {
					inflight = map[model.DispatchMode]int{}
				}
				templates, terr := b.client.ListPromptTemplates(ctx)
				if terr != nil {
					templates = nil
				}
				waitingState = boardcards.DeriveWaitingState(iss, activeDispatch, inflight, templates)
			}
		}
	}

	return IssueBriefDTO{
		Issue:           meta,
		Feature:         feat,
		Relations:       rels,
		PullRequests:    prs,
		Documents:       docs,
		Comments:        comments,
		Claimants:       agentcards.MapClaimants(brief.Claimants),
		Taken:           brief.Taken,
		WaitingForClaim: iss.WaitingForClaim,
		WaitingState:    waitingState,
		Warnings:        warnings,
	}, nil
}

// AttachPullRequest attaches a pull-request URL to an issue and returns
// the resolved PRDTO. Validation (http/https + host) lives in the
// store layer; an invalid URL surfaces as the original error message
// for the IssueWorkspace's PR-attach form to display.
func (b *BoardService) AttachPullRequest(repoPrefix, key, url string) (PRDTO, error) {
	ctx := context.Background()
	repo, err := b.resolveRepoForKey(ctx, repoPrefix, key)
	if err != nil {
		return PRDTO{}, err
	}
	pr, err := b.client.AttachPR(ctx, repo, key, url, false)
	if err != nil {
		return PRDTO{}, err
	}
	return PRDTO{URL: pr.URL}, nil
}

// UpdateIssueDescription replaces an issue's description and returns the
// refreshed issue-drawer payload. repoPrefix may be empty or "all" — the
// prefix is then derived from the canonical issue key.
func (b *BoardService) UpdateIssueDescription(repoPrefix, key, description string) (IssueDetail, error) {
	ctx := context.Background()
	repo, err := b.resolveRepoForKey(ctx, repoPrefix, key)
	if err != nil {
		return IssueDetail{}, err
	}
	if _, err := b.client.UpdateIssue(ctx, repo, key, client.IssueEdit{Description: &description}, false); err != nil {
		return IssueDetail{}, err
	}
	return b.GetIssue(repoPrefix, key)
}

// SetIssueState changes an issue's state and returns the refreshed card.
// It backs the board's drag-to-move: dropping a card in a new column
// persists the state change so it survives the next auto-refresh poll.
// repoPrefix may be empty or "all" — the prefix is then derived from the
// canonical issue key.
func (b *BoardService) SetIssueState(repoPrefix, key, state string) (BoardCard, error) {
	ctx := context.Background()
	parsedState, err := model.ParseState(state)
	if err != nil {
		return BoardCard{}, err
	}
	repo, err := b.resolveRepoForKey(ctx, repoPrefix, key)
	if err != nil {
		return BoardCard{}, err
	}
	iss, err := b.client.SetIssueState(ctx, repo, key, parsedState, false)
	if err != nil {
		return BoardCard{}, err
	}
	// A card only reaches here via drag, which the UI blocks for taken
	// cards — so it can't be taken. Skip the extra claims query.
	return cardFromIssue(iss, false), nil
}

// AddComment appends a comment to an issue and returns the refreshed
// issue-drawer payload. An empty author falls back to the OS username,
// the same default the CLI uses for human actors. repoPrefix may be empty
// or "all" — the prefix is then derived from the canonical issue key.
//
// `eval` (BACI-131) flags the row as a quality-review note posted from
// the kanban quick-eval composer — the server pins the in-flight
// (agent_session_id, dispatch_id, mode) snapshot onto the comment at
// write time. `transcriptEventRef` (BACI-141) is the optional per-event
// anchor set by the transcript viewer's per-event composer
// (`tool_use_id:<id>` or `line_index:<n>`); empty leaves the comment
// unanchored, rendered pinned to the dispatch prompt card at the top of
// the transcript view.
func (b *BoardService) AddComment(repoPrefix, key, author, body string, isEval bool, transcriptEventRef string) (IssueDetail, error) {
	ctx := context.Background()
	repo, err := b.resolveRepoForKey(ctx, repoPrefix, key)
	if err != nil {
		return IssueDetail{}, err
	}
	if strings.TrimSpace(author) == "" {
		if u, err := user.Current(); err == nil && u.Username != "" {
			author = u.Username
		} else {
			author = "desktop"
		}
	}
	if _, err := b.client.AddComment(ctx, repo, inputs.CommentAddInput{
		IssueKey:           key,
		Author:             author,
		Body:               body,
		Eval:               isEval,
		TranscriptEventRef: transcriptEventRef,
	}, false); err != nil {
		return IssueDetail{}, err
	}
	return b.GetIssue(repoPrefix, key)
}

// DeleteComment removes a comment from an issue and returns the
// refreshed issue-drawer payload. The comment is addressed by its
// immutable uuid. repoPrefix may be empty or "all" — the prefix is then
// derived from the canonical issue key.
func (b *BoardService) DeleteComment(repoPrefix, key, commentUUID string) (IssueDetail, error) {
	ctx := context.Background()
	repo, err := b.resolveRepoForKey(ctx, repoPrefix, key)
	if err != nil {
		return IssueDetail{}, err
	}
	if _, _, err := b.client.DeleteComment(ctx, repo, inputs.CommentRmInput{
		IssueKey:    key,
		CommentUUID: commentUUID,
	}, false); err != nil {
		return IssueDetail{}, err
	}
	return b.GetIssue(repoPrefix, key)
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

// ListAgents returns the agent sessions for one repo (or every repo when
// repoPrefix is empty or "all"), each carrying its status, open claims,
// and the dispatches aimed at it. SessionStart stubs that never
// completed register are hidden — they're noise in the supervision UI.
//
// The assembly logic lives in internal/agentcards (BACI-50) so the
// bacio api can serve the same payload to a browser. This wrapper just
// resolves the repo and delegates.
func (b *BoardService) ListAgents(repoPrefix string) ([]AgentCard, error) {
	ctx := context.Background()
	var repo *model.Repo
	if repoPrefix != "" && repoPrefix != "all" {
		r, err := b.client.GetRepoByPrefix(ctx, repoPrefix)
		if err != nil {
			return nil, err
		}
		repo = r
	}
	return agentcards.Assemble(ctx, b.client, repo)
}

// DispatchIssue queues a dispatch against an issue for a given job stage
// (mode). The state-gate check and the free-agent auto-pick both live
// on client.Client.AutoDispatchIssue (BACI-40), so the per-card button,
// the REST route, and the CLI's target-less `bacio agent dispatch`
// share the same picker + gate. repoPrefix may be empty or "all" — the
// prefix is then derived from the canonical issue key.
func (b *BoardService) DispatchIssue(repoPrefix, issueKey, mode string) (DispatchDTO, error) {
	ctx := context.Background()
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
	d, err := b.client.AutoDispatchIssue(ctx, repo, issueKey, mode, false)
	if err != nil {
		return DispatchDTO{}, err
	}
	return dispatchDTO(d), nil
}

// GetSessionQuestion fetches one BACI-53 ask_user_question row by id.
// Backs the desktop Agents-view modal — the AgentCard composite ships
// only the badge metadata, so the modal does one extra round trip
// when the user opens it to fetch the full payload + any existing
// answer.
func (b *BoardService) GetSessionQuestion(id int64) (*model.SessionQuestion, error) {
	return b.client.GetSessionQuestion(context.Background(), id)
}

// AnswerSessionQuestion submits the user's answer. answers is keyed
// by question text; values are either string (single-select) or
// []string (multi-select). The store re-validates against the
// stored payload at the boundary.
func (b *BoardService) AnswerSessionQuestion(id int64, answers map[string]any) (*model.SessionQuestion, error) {
	return b.client.AnswerSessionQuestion(context.Background(), id, model.QuestionAnswers(answers), false)
}

// CancelSessionQuestion dismisses an open question — the agent
// receives a tool error on the next channel poll tick.
func (b *BoardService) CancelSessionQuestion(id int64) (*model.SessionQuestion, error) {
	return b.client.CancelSessionQuestion(context.Background(), id, false)
}

// QueueFollowOnDispatch (BACI-180) attaches a dormant follow-on
// dispatch to the issue's in-flight (parent) dispatch. Mirrors
// DispatchIssue's shape — same DTO out, repo prefix derivable from
// the issue key — but routes through client.QueueFollowOnDispatch so
// the gate + parent-resolution path stays in one place. Errors when
// there is no open dispatch on the issue, when the state-gate
// rejects, or when a dormant follow-on already exists (single-slot
// per issue).
func (b *BoardService) QueueFollowOnDispatch(repoPrefix, issueKey, mode string) (DispatchDTO, error) {
	ctx := context.Background()
	repo, err := b.resolveRepoForKey(ctx, repoPrefix, issueKey)
	if err != nil {
		return DispatchDTO{}, err
	}
	d, err := b.client.QueueFollowOnDispatch(ctx, repo, issueKey, mode, false)
	if err != nil {
		return DispatchDTO{}, err
	}
	return dispatchDTO(d), nil
}

// CancelFollowOnDispatch (BACI-180) is the chip-remove handler: cancel
// the dormant follow-on attached to an issue. Idempotent — a no-op on
// an issue with no dormant follow-on (returns the zero DispatchDTO and
// nil error) so a stale UI click doesn't surface as an error.
func (b *BoardService) CancelFollowOnDispatch(repoPrefix, issueKey string) (DispatchDTO, error) {
	ctx := context.Background()
	repo, err := b.resolveRepoForKey(ctx, repoPrefix, issueKey)
	if err != nil {
		return DispatchDTO{}, err
	}
	d, err := b.client.CancelFollowOnDispatch(ctx, repo, issueKey, false)
	if err != nil {
		return DispatchDTO{}, err
	}
	if d == nil {
		return DispatchDTO{}, nil
	}
	return dispatchDTO(d), nil
}

// CancelWaitingDispatch (BACI-51) is the spinner-as-cancel-button
// binding. Resolves the active (queued / pending / delivered) dispatch
// for an issue and cancels it in a single Wails call so card DTOs
// don't have to carry the dispatch id. A no-active-dispatch issue is
// not an error — the spinner may have cleared between the click and
// the call landing — the cancel is a no-op and returns nil.
//
// BACI-130: cancel-after-delivery is now rejected at the store
// boundary. The desktop card hides the cancel affordance via the
// new BoardCard.WaitingDispatchDelivered field, but a stale render
// can still send the click in — swallow the recognisable error
// fragment the same way the 404 race-clear path returns nil.
func (b *BoardService) CancelWaitingDispatch(repoPrefix, issueKey string) error {
	ctx := context.Background()
	prefix := repoPrefix
	if prefix == "" || prefix == "all" {
		if i := strings.LastIndex(issueKey, "-"); i > 0 {
			prefix = issueKey[:i]
		}
	}
	repo, err := b.client.GetRepoByPrefix(ctx, prefix)
	if err != nil {
		return err
	}
	dsp, err := b.client.WaitingDispatchForIssue(ctx, repo, issueKey)
	if err != nil {
		return err
	}
	if dsp == nil {
		return nil
	}
	if _, err = b.client.CancelDispatch(ctx, inputs.AgentCancelInput{ID: dsp.ID}, false); err != nil {
		if strings.Contains(err.Error(), "has been delivered") {
			return nil
		}
		return err
	}
	return nil
}

// ArchiveIssue (BACI-68) is the per-card "archive" Wails binding —
// stamps archived_at, returns the updated row. The desktop card menu
// calls this when the user selects Archive. The kanban refreshes via
// the regular ListCards path; archived cards stay visible only when
// display.show_archived is on (and then render visibly-muted).
func (b *BoardService) ArchiveIssue(repoPrefix, issueKey string) (*model.Issue, error) {
	ctx := context.Background()
	prefix := repoPrefix
	if prefix == "" || prefix == "all" {
		if i := strings.LastIndex(issueKey, "-"); i > 0 {
			prefix = issueKey[:i]
		}
	}
	repo, err := b.client.GetRepoByPrefix(ctx, prefix)
	if err != nil {
		return nil, err
	}
	return b.client.ArchiveIssue(ctx, repo, issueKey, false)
}

// UnarchiveIssue (BACI-68) clears archived_at.
func (b *BoardService) UnarchiveIssue(repoPrefix, issueKey string) (*model.Issue, error) {
	ctx := context.Background()
	prefix := repoPrefix
	if prefix == "" || prefix == "all" {
		if i := strings.LastIndex(issueKey, "-"); i > 0 {
			prefix = issueKey[:i]
		}
	}
	repo, err := b.client.GetRepoByPrefix(ctx, prefix)
	if err != nil {
		return nil, err
	}
	return b.client.UnarchiveIssue(ctx, repo, issueKey, false)
}
