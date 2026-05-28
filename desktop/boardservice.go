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
	"github.com/mrgeoffrich/bacio/internal/store"
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
	model.StateInPipeline:  "In Pipeline",
	model.StateToBeShipped: "To Be Shipped",
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
// `taken` flag so the IssueWorkspace doesn't have to hop between
// brief.issue.{...} and brief.taken. The waiting signal lives on the
// brief envelope's WaitingState field (BACI-145/BACI-255), not here.
type IssueMetaDTO struct {
	Key         string   `json:"key"`
	Column      string   `json:"column"`
	ColumnLabel string   `json:"columnLabel"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
	Assignees   []string `json:"assignees"`
	Claude      bool     `json:"claude"`
	Taken       bool     `json:"taken"`
	// LatestPlan (BACI-216) — the newest `plan`-typed doc linked
	// directly to this issue, or nil when none. Drives the prominent
	// "Open plan" link in IssueWorkspace's header. Mirrors the
	// BoardCard.LatestPlan / IssueDetail.LatestPlan fields so every
	// surface reads the same plan affordance.
	LatestPlan *LatestPlanDTO `json:"latestPlan,omitempty"`
}

// IssueBriefDTO is the workspace-shaped payload — BoardService.GetIssueBrief's
// return value, mirroring internal/client/views.go::IssueBrief with desktop
// camelCase JSON tags. New fields over IssueDetail: feature, relations,
// warnings, and doc content (via LinkedDocDTO).
//
// BACI-145: WaitingState is the structured "why is this card waiting?"
// projection used by the IssueLockBanner — same shape and same wording
// as the kanban-card label. Nil when the issue isn't waiting; the
// banner falls back to its "taken" branch when an agent has the claim.
// Post-BACI-255 this is also the canonical "is this card waiting?"
// signal — the denormalised waitingForClaim boolean that used to ride
// alongside was removed because it duplicated this richer field and
// could drift out of sync with the underlying dispatch rows.
type IssueBriefDTO struct {
	Issue        IssueMetaDTO             `json:"issue"`
	Feature      *FeatureRefDTO           `json:"feature,omitempty"`
	Relations    RelationsDTO             `json:"relations"`
	PullRequests []PRDTO                  `json:"pullRequests"`
	Documents    []LinkedDocDTO           `json:"documents"`
	Comments     []CommentDTO             `json:"comments"`
	Claimants    []ClaimantDTO            `json:"claimants"`
	Taken        bool                     `json:"taken"`
	WaitingState *boardcards.WaitingState `json:"waitingState,omitempty"`
	// LatestPlan (BACI-216) — duplicated alongside Issue.LatestPlan
	// so consumers that read the brief envelope (REST/HTTP clients)
	// can pick it up without descending into the meta block. The two
	// are always in lockstep.
	LatestPlan *LatestPlanDTO `json:"latestPlan,omitempty"`
	Warnings   []string       `json:"warnings"`
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
	// LatestPlan (BACI-216) — see IssueMetaDTO.LatestPlan.
	LatestPlan *LatestPlanDTO `json:"latestPlan,omitempty"`
}

// LatestPlanDTO (BACI-216) is the Wails-side mirror of
// model.LatestPlan — the newest `plan`-typed doc linked directly to
// an issue. Carries enough metadata (filename, uuid, updated_at) for
// the React surfaces to render the "Open plan" link without a
// follow-up fetch; the doc body itself is loaded on-demand via the
// existing /documents/<filename> route.
type LatestPlanDTO struct {
	DocumentID int64     `json:"documentId"`
	UUID       string    `json:"uuid"`
	Filename   string    `json:"filename"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

// latestPlanDTO converts the shared model.LatestPlan into the
// camelCase Wails-side shape. Returns nil when the input is nil so
// the JSON omitempty drops the field for issues with no plan.
func latestPlanDTO(p *model.LatestPlan) *LatestPlanDTO {
	if p == nil {
		return nil
	}
	return &LatestPlanDTO{
		DocumentID: p.DocumentID,
		UUID:       p.UUID,
		Filename:   p.Filename,
		UpdatedAt:  p.UpdatedAt,
	}
}

// ShippedIssueDTO is one row in the BACI-187 shipping-log popover.
// Lean by design — the popover renders key + title + a relative date
// chip + tags + an optional PR chip. The DTO shape mirrors the HTTP
// /repos/{prefix}/shipped response so the React side imports the
// same type from either api.ts (Wails) or api.http.ts (HTTP) without
// reshape.
type ShippedIssueDTO struct {
	Key          string    `json:"key"`
	Title        string    `json:"title"`
	TerminalAt   time.Time `json:"terminalAt"`
	Tags         []string  `json:"tags"`
	FeatureSlug  string    `json:"featureSlug,omitempty"`
	FeatureEmoji string    `json:"featureEmoji,omitempty"`
	PRURL        string    `json:"prUrl,omitempty"`
}

// ShippedListDTO (BACI-221) wraps the popover's per-fetch rows with
// the total count under the same scope so the popover header can
// render "showing N of TOTAL" without a second round trip. Mirrors
// api.ShippedListResponse on the HTTP side; both transports decode to
// the same shape on the React seam.
type ShippedListDTO struct {
	Rows  []ShippedIssueDTO `json:"rows"`
	Total int               `json:"total"`
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

// ListColumns returns the kanban columns — the legacy state-board
// columns, excluding the Pipeline states (those cards live on the
// Pipeline page).
func (b *BoardService) ListColumns() ([]BoardColumn, error) {
	states := model.BoardColumnStates()
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

// ListShipped (BACI-187, reshaped for BACI-221) returns the
// recently-shipped issues for one repo (newest-first) wrapped with the
// total count under the same scope. sinceDays clamps the window
// (0 = no lower bound — "Forever"); limit caps the row count
// (0 = the popover's default 20, max 100). Sibling of ListCards in
// shape — one repo, one trip, lean rows the popover renders without
// follow-up fetches.
func (b *BoardService) ListShipped(repoPrefix string, sinceDays, limit int) (ShippedListDTO, error) {
	ctx := context.Background()
	if repoPrefix == "" || repoPrefix == "all" {
		return ShippedListDTO{}, fmt.Errorf("ListShipped: a repo is required (cross-repo popover is out of scope)")
	}
	repo, err := b.client.GetRepoByPrefix(ctx, repoPrefix)
	if err != nil {
		return ShippedListDTO{}, err
	}
	f := store.ShippedFilter{}
	if limit > 0 {
		if limit > 100 {
			limit = 100
		}
		f.Limit = limit
	}
	if sinceDays > 0 {
		cutoff := time.Now().Add(-time.Duration(sinceDays) * 24 * time.Hour)
		f.Since = &cutoff
	}
	issues, err := b.client.ListShippedIssues(ctx, repo, f)
	if err != nil {
		return ShippedListDTO{}, err
	}
	rows := make([]ShippedIssueDTO, 0, len(issues))
	for _, iss := range issues {
		tags := iss.Tags
		if tags == nil {
			tags = []string{}
		}
		row := ShippedIssueDTO{
			Key:          iss.Key,
			Title:        iss.Title,
			Tags:         tags,
			FeatureSlug:  iss.FeatureSlug,
			FeatureEmoji: iss.FeatureEmoji,
		}
		if iss.TerminalAt != nil {
			row.TerminalAt = *iss.TerminalAt
		}
		// First PR by insertion order — same rule as the HTTP handler.
		// A list error is non-fatal — the row still renders without
		// the PR chip.
		prs, perr := b.client.ListPRs(ctx, repo, iss.Key)
		if perr == nil && len(prs) > 0 {
			row.PRURL = prs[0].URL
		}
		rows = append(rows, row)
	}
	// BACI-221: count uses the same WHERE as the list, ignoring the
	// per-fetch limit so the popover header can render "showing N of
	// TOTAL". Total includes archived / board-hidden rows the kanban
	// filters out — see CountShippedIssues.
	total, err := b.client.CountShippedIssues(ctx, repo, f)
	if err != nil {
		return ShippedListDTO{}, err
	}
	return ShippedListDTO{Rows: rows, Total: total}, nil
}

// CountShipped (BACI-221) returns the total number of shipped issues
// for one repo under the active Today / Last Week / Forever scope.
// Polled on the same 10s cadence as the other live read endpoints so
// the topbar pill reflects the current scope even when the popover
// isn't open. sinceDays==0 means "Forever" (no lower bound on
// terminal_at).
func (b *BoardService) CountShipped(repoPrefix string, sinceDays int) (int, error) {
	ctx := context.Background()
	if repoPrefix == "" || repoPrefix == "all" {
		return 0, fmt.Errorf("CountShipped: a repo is required (cross-repo pill is out of scope)")
	}
	repo, err := b.client.GetRepoByPrefix(ctx, repoPrefix)
	if err != nil {
		return 0, err
	}
	f := store.ShippedFilter{}
	if sinceDays > 0 {
		cutoff := time.Now().Add(-time.Duration(sinceDays) * 24 * time.Hour)
		f.Since = &cutoff
	}
	return b.client.CountShippedIssues(ctx, repo, f)
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
		LatestPlan:   latestPlanDTO(view.LatestPlan),
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
	latestPlan := latestPlanDTO(brief.LatestPlan)
	meta := IssueMetaDTO{
		Key:         iss.Key,
		Column:      string(iss.State),
		ColumnLabel: stateLabel(iss.State),
		Title:       iss.Title,
		Description: iss.Description,
		Tags:        tags,
		Assignees:   assigneeList(iss.Assignee),
		Claude:      iss.Assignee == "claude",
		Taken:       brief.Taken,
		LatestPlan:  latestPlan,
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

	// BACI-145 / BACI-255: derive WaitingState for the IssueLockBanner
	// directly from the dispatch table. WaitingDispatchForIssue returning
	// non-nil IS the "is this card waiting?" predicate now — the
	// denormalised issues.waiting_for_claim cache that used to gate this
	// branch was removed because it could drift. The "all" pseudo-board
	// doesn't have a *model.Repo handy, so resolve it from the issue's
	// canonical key (PREFIX-N) for that case.
	var waitingState *boardcards.WaitingState
	dispatchRepo := repo
	if dispatchRepo == nil {
		if r, err := b.resolveRepoForKey(ctx, repoPrefix, iss.Key); err == nil {
			dispatchRepo = r
		}
	}
	if dispatchRepo != nil {
		activeDispatch, derr := b.client.WaitingDispatchForIssue(ctx, dispatchRepo, iss.Key)
		if derr == nil && activeDispatch != nil {
			// BACI-227: per-(mode, branch) in-flight grouping so the
			// IssueLockBanner's WaitingState tracks the matcher's
			// per-branch concurrency gate exactly.
			inflight, ierr := b.client.InflightByModeBaseForRepo(ctx, dispatchRepo)
			if ierr != nil {
				inflight = map[store.InflightKey]int{}
			}
			templates, terr := b.client.ListPromptTemplates(ctx)
			if terr != nil {
				templates = nil
			}
			waitingState = boardcards.DeriveWaitingState(iss, activeDispatch, inflight, templates)
		}
	}

	return IssueBriefDTO{
		Issue:        meta,
		Feature:      feat,
		Relations:    rels,
		PullRequests: prs,
		Documents:    docs,
		Comments:     comments,
		Claimants:    agentcards.MapClaimants(brief.Claimants),
		Taken:        brief.Taken,
		WaitingState: waitingState,
		LatestPlan:   latestPlan,
		Warnings:     warnings,
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

// AddIssue creates a new issue in the named repo and returns the
// freshly-shaped BoardCard so the React composer can prepend it to the
// kanban + open IssueWorkspace on the new key. Mirrors DispatchIssue's
// shape line-for-line: required repo prefix in hand (the "all"
// cross-repo pseudo-board has no concept of "which repo do I create
// in", so the caller must pass a real prefix), one client.CreateIssue
// call, then a single-row Assemble round-trip so the new card carries
// the same feature-emoji / waiting / blocker shape as the rest of the
// kanban. Validation (empty title, control chars, etc.) lives at the
// store boundary inside client.CreateIssue.
func (b *BoardService) AddIssue(repoPrefix, title, description string) (BoardCard, error) {
	ctx := context.Background()
	if repoPrefix == "" || repoPrefix == "all" {
		return BoardCard{}, fmt.Errorf("AddIssue: a repo prefix is required (cross-repo pseudo-board has no target)")
	}
	repo, err := b.client.GetRepoByPrefix(ctx, repoPrefix)
	if err != nil {
		return BoardCard{}, err
	}
	iss, err := b.client.CreateIssue(ctx, repo, inputs.IssueAddInput{Title: title, Description: description}, false)
	if err != nil {
		return BoardCard{}, err
	}
	// Re-assemble the board for the repo so the new card inherits the
	// same feature-emoji / waiting / blocker shape ListCards produces.
	// A fresh issue has none of those yet, but going through the same
	// assembler keeps the card payload identical to what the next
	// listCards poll will draw — no flicker on the React side.
	showArchived, _ := b.client.GetDisplayShowArchived(ctx)
	var hiddenSlugs []string
	if slugs, herr := b.client.ListHiddenFeatureSlugs(ctx, repo); herr == nil {
		hiddenSlugs = slugs
	}
	cards, err := boardcards.Assemble(ctx, b.client, repo, showArchived, hiddenSlugs)
	if err != nil {
		return BoardCard{}, err
	}
	for _, c := range cards {
		if c.Key == iss.Key {
			return c, nil
		}
	}
	// Fall back to the lightweight shape if the assembler didn't surface
	// the row (e.g. the repo's per-feature hide-on-board filter caught it
	// — possible if the issue was filed against a hidden feature). The
	// composer still needs a real card to route to.
	return cardFromIssue(iss, false), nil
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

// DispatchIssueChain (BACI-209) queues a state-gated primary dispatch
// PLUS a dormant follow-on against the brand-new parent in one
// transaction. Backs the kanban's "Plan, then Implement" compound
// picker on todo cards. Mirrors DispatchIssue's shape (returns the
// parent DispatchDTO; the follow-on rides on the next BoardCard
// refresh via card.followOn).
//
// All three surfaces — Wails, REST, CLI — funnel through
// client.AutoDispatchIssueWithFollowOn so the gate + payload + two
// audit rows live in one place.
func (b *BoardService) DispatchIssueChain(repoPrefix, issueKey, mode, followOnMode string) (DispatchDTO, error) {
	ctx := context.Background()
	repo, err := b.resolveRepoForKey(ctx, repoPrefix, issueKey)
	if err != nil {
		return DispatchDTO{}, err
	}
	parent, _, err := b.client.AutoDispatchIssueWithFollowOn(ctx, repo, issueKey, mode, followOnMode, false)
	if err != nil {
		return DispatchDTO{}, err
	}
	return dispatchDTO(parent), nil
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

// RescueDispatch (BACI-190) posts a `from="bacio-rescue"` channel
// event to an idle supervisor session, asking it to handle a dead
// worker's stranded worktree INLINE. Eligibility (status pending /
// delivered, real per-mode creator, target session ended, idle
// supervisor available) is enforced by client.CreateRescueDispatch
// — the binding just shapes the response. Returns an error string
// the frontend can surface in a toast when the rescue can't be
// queued (e.g. no idle supervisor in the repo).
func (b *BoardService) RescueDispatch(dispatchID int64) (DispatchDTO, error) {
	d, err := b.client.CreateRescueDispatch(context.Background(), dispatchID)
	if err != nil {
		return DispatchDTO{}, err
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

// DefaultFeatureDTO (BACI-235) is the per-repo default_feature setting
// shaped for the desktop / web Settings panel. Slug is empty when the
// setting is unset; Title / Emoji are inflated when the setting is set
// so the dropdown can render the same glyph + label as the rest of the
// feature affordances. Mirrors FeatureSummary's slim shape.
type DefaultFeatureDTO struct {
	Slug  string `json:"slug"`
	Title string `json:"title,omitempty"`
	Emoji string `json:"emoji,omitempty"`
}

// GetDefaultFeature (BACI-235) returns the per-repo default_feature
// setting for repoPrefix. Empty Slug = unset (the legacy default).
func (b *BoardService) GetDefaultFeature(repoPrefix string) (DefaultFeatureDTO, error) {
	ctx := context.Background()
	repo, err := b.client.GetRepoByPrefix(ctx, repoPrefix)
	if err != nil {
		return DefaultFeatureDTO{}, err
	}
	feat, err := b.client.GetDefaultFeature(ctx, repo)
	if err != nil {
		return DefaultFeatureDTO{}, err
	}
	if feat == nil {
		return DefaultFeatureDTO{}, nil
	}
	return DefaultFeatureDTO{Slug: feat.Slug, Title: feat.Title, Emoji: feat.Emoji}, nil
}

// SetDefaultFeature (BACI-235) writes the per-repo default_feature
// setting. slug must reference an existing feature in the same repo;
// the client records the audit row.
func (b *BoardService) SetDefaultFeature(repoPrefix, slug string) (DefaultFeatureDTO, error) {
	ctx := context.Background()
	repo, err := b.client.GetRepoByPrefix(ctx, repoPrefix)
	if err != nil {
		return DefaultFeatureDTO{}, err
	}
	feat, err := b.client.SetDefaultFeature(ctx, repo, slug, false)
	if err != nil {
		return DefaultFeatureDTO{}, err
	}
	return DefaultFeatureDTO{Slug: feat.Slug, Title: feat.Title, Emoji: feat.Emoji}, nil
}

// ClearDefaultFeature (BACI-235) clears the per-repo default_feature
// setting. Returns a zero-valued DTO so the caller can re-render
// without a separate read.
func (b *BoardService) ClearDefaultFeature(repoPrefix string) (DefaultFeatureDTO, error) {
	ctx := context.Background()
	repo, err := b.client.GetRepoByPrefix(ctx, repoPrefix)
	if err != nil {
		return DefaultFeatureDTO{}, err
	}
	if err := b.client.ClearDefaultFeature(ctx, repo, false); err != nil {
		return DefaultFeatureDTO{}, err
	}
	return DefaultFeatureDTO{}, nil
}

// BoardHiddenStatesDTO (BACI-248) is the per-repo board-hidden-states
// envelope shaped for the desktop / web Per-repository Settings pane.
// `States` is the canonical sorted slice of state names hidden from
// the kanban board for this repo. Empty when nothing is hidden.
// Mirrors the response shape on the BACI-177 features/hidden Wails
// path; the React seam picks up the same field name for both.
type BoardHiddenStatesDTO struct {
	States []string `json:"states"`
}

// GetBoardHiddenStates (BACI-248) returns the per-repo set of
// kanban-column states hidden on this machine. Threaded through the
// client so the Wails binding and the HTTP API stay in lockstep on
// the audit ledger and the canonical state-name validation.
func (b *BoardService) GetBoardHiddenStates(repoPrefix string) (BoardHiddenStatesDTO, error) {
	ctx := context.Background()
	repo, err := b.client.GetRepoByPrefix(ctx, repoPrefix)
	if err != nil {
		return BoardHiddenStatesDTO{}, err
	}
	states, err := b.client.ListBoardHiddenStates(ctx, repo)
	if err != nil {
		return BoardHiddenStatesDTO{}, err
	}
	if states == nil {
		states = []string{}
	}
	return BoardHiddenStatesDTO{States: states}, nil
}

// SetBoardHiddenStates (BACI-248) replaces the per-repo
// board-hidden-states set with `states`. Replace-not-merge: the array
// is the full new set. Unknown state names are silently dropped at the
// store boundary so a future state rename doesn't break old saved
// settings. Records a `repo_setting.update` audit row when the set
// actually changes (idempotent no-op writes skip the audit).
func (b *BoardService) SetBoardHiddenStates(repoPrefix string, states []string) (BoardHiddenStatesDTO, error) {
	ctx := context.Background()
	repo, err := b.client.GetRepoByPrefix(ctx, repoPrefix)
	if err != nil {
		return BoardHiddenStatesDTO{}, err
	}
	if states == nil {
		states = []string{}
	}
	out, err := b.client.SetBoardHiddenStates(ctx, repo, states, false)
	if err != nil {
		return BoardHiddenStatesDTO{}, err
	}
	if out == nil {
		out = []string{}
	}
	return BoardHiddenStatesDTO{States: out}, nil
}
