package main

import (
	"context"
	"fmt"
	"os/user"
	"sort"
	"strings"
	"time"

	"github.com/mrgeoffrich/bacio/internal/agentcards"
	"github.com/mrgeoffrich/bacio/internal/boardcards"
	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/git"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
	"github.com/mrgeoffrich/bacio/internal/timeparse"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// stateLabels maps each bacio issue state to a human-friendly column label.
var stateLabels = map[model.State]string{
	model.StateTodo:        "Todo",
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
//
// Two further fields (BACI-376) are what let the badge stop lying:
// SyncBackgroundEnabled is the *global* sync.background_enabled toggle,
// repeated on every board — a repo can be fully configured and still not
// be mirrored because the ticker is off app-wide. SyncMirroredBy is the
// sync repo already carrying this repo's exported data, which (because
// the export is whole-DB) is routinely set for repos that have no sync
// config of their own.
type Board struct {
	Prefix string `json:"prefix"`
	Name   string `json:"name"`
	// Kind is the repos.kind discriminator — "git" for a repo backed by
	// a working tree, "workspace" for a manual, pathless one. The React
	// tree reads it to hide the Agentic Pipeline nav entry on a
	// workspace (there is no worktree, so a dispatched agent would have
	// nowhere to work) and to group the repository picker by kind.
	// Legacy rows with an empty kind normalise to "git" here, so the
	// field is never blank on the wire.
	Kind string `json:"kind"`
	// The per-space nav-surface gates — which top-nav tabs this space
	// exposes. ShowAgentSurfaces is the "Agent Mode" setting (the
	// Agentic Pipeline / Agents / Monitor group); ShowKanban is "Show
	// Kanban Board". Both are RESOLVED values: a space that has never
	// been configured gets the default for its kind (agents on + Kanban
	// off for a git repo, the reverse for a workspace), never the Go
	// zero value. See model.ResolveRepoSurfaces.
	//
	// They ride the Board rather than getting their own call because
	// RepoProvider.pickBoard needs the target space's gates
	// synchronously, inside a click handler, to pick its home view.
	ShowAgentSurfaces     bool       `json:"showAgentSurfaces"`
	ShowKanban            bool       `json:"showKanban"`
	IssueCount            int        `json:"issueCount"`
	SyncEnabled           bool       `json:"syncEnabled"`
	SyncBackgroundEnabled bool       `json:"syncBackgroundEnabled"`
	SyncMirroredBy        string     `json:"syncMirroredBy,omitempty"`
	SyncInProgress        bool       `json:"syncInProgress"`
	SyncLastAt            *time.Time `json:"syncLastAt,omitempty"`
	SyncLastError         string     `json:"syncLastError,omitempty"`
}

// RepoActivity (BACI-369) is one repo's activity summary for the topbar
// picker's ordering: when anything last happened in it, and how many
// agent jobs it has running right now. Deliberately separate from Board
// — Board is loaded once at mount, this is polled on the shared cadence.
type RepoActivity struct {
	Prefix         string     `json:"prefix"`
	LastActivityAt *time.Time `json:"lastActivityAt,omitempty"`
	ActiveJobs     int        `json:"activeJobs"`
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
	Key         string `json:"key"`
	Column      string `json:"column"`
	ColumnLabel string `json:"columnLabel"`
	Title       string `json:"title"`
	// CustomerImpact (BACI-349) is the issue's optional one-line customer
	// impact. The React detail header renders it inline and lets the user
	// edit it; empty is omitted from JSON.
	CustomerImpact string   `json:"customerImpact,omitempty"`
	Description    string   `json:"description"`
	Tags           []string `json:"tags"`
	Assignees      []string `json:"assignees"`
	Claude         bool     `json:"claude"`
	Taken          bool     `json:"taken"`
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
	Key         string `json:"key"`
	Column      string `json:"column"`
	ColumnLabel string `json:"columnLabel"`
	Title       string `json:"title"`
	// CustomerImpact (BACI-349) — see IssueMetaDTO.CustomerImpact.
	CustomerImpact string        `json:"customerImpact,omitempty"`
	Description    string        `json:"description"`
	Tags           []string      `json:"tags"`
	Assignees      []string      `json:"assignees"`
	Claude         bool          `json:"claude"`
	Comments       []CommentDTO  `json:"comments"`
	PullRequests   []PRDTO       `json:"pullRequests"`
	Documents      []DocLinkDTO  `json:"documents"`
	Claimants      []ClaimantDTO `json:"claimants"`
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
	Key   string `json:"key"`
	Title string `json:"title"`
	// CustomerImpact (BACI-349) — the popover renders this as the primary
	// line, falling back to Title (with a muted/italic class) when empty.
	CustomerImpact string    `json:"customerImpact,omitempty"`
	TerminalAt     time.Time `json:"terminalAt"`
	Tags           []string  `json:"tags"`
	FeatureSlug    string    `json:"featureSlug,omitempty"`
	FeatureEmoji   string    `json:"featureEmoji,omitempty"`
	PRURL          string    `json:"prUrl,omitempty"`
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
	// launchRepoPrefix is the repo the desktop binary was started in
	// (BACI-368), resolved once in main() from the cwd captured before
	// Wails can chdir. Empty when the app wasn't launched from inside a
	// git repo — e.g. from the Finder / dock.
	launchRepoPrefix string
}

func NewBoardService(c client.Client, launchRepoPrefix string) *BoardService {
	return &BoardService{client: c, launchRepoPrefix: launchRepoPrefix}
}

// LaunchRepo returns the prefix of the repo the app was launched from,
// or "" when there wasn't one. The React tree uses it as the
// highest-priority default when the URL carries no repo prefix
// (BACI-368) — the desktop half of the web's GET /launch-repo.
//
// The error return is vestigial (there is nothing to fail) but keeps
// the generated binding a rejectable promise like its siblings.
func (b *BoardService) LaunchRepo() (string, error) {
	return b.launchRepoPrefix, nil
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
	// Per-space nav-surface gates, keyed by prefix. Unlike sync status
	// above, a failure here is NOT shrugged off into zero values: the
	// zero value hides every tab, so a missing entry falls back to the
	// kind default in surfaceFor rather than blanking the nav.
	surfaceByPrefix, serr := b.client.RepoSurfaces(ctx)
	if serr != nil {
		surfaceByPrefix = nil
	}
	boards := make([]Board, 0, len(repos))
	for _, r := range repos {
		issues, err := b.client.ListIssues(ctx, client.IssueFilter{Repo: r})
		if err != nil {
			return nil, err
		}
		boards = append(boards, boardWithSync(r, len(issues), syncByPrefix[r.Prefix], surfaceFor(surfaceByPrefix, r)))
	}
	return boards, nil
}

// surfaceFor pulls one repo's resolved gates out of the bulk map,
// falling back to the kind default when the map is missing or doesn't
// carry the prefix. Never returns the zero value, which would read as
// "hide every tab".
func surfaceFor(byPrefix map[string]model.RepoSurfaces, r *model.Repo) model.RepoSurfaces {
	if s, ok := byPrefix[r.Prefix]; ok {
		return s
	}
	return model.ResolveRepoSurfaces(r.Kind, nil, nil)
}

// oneSurface resolves a single repo's gates for the add-repo /
// add-workspace paths, which build one Board and don't already hold the
// bulk map. Same never-zero-value guarantee as surfaceFor.
func (b *BoardService) oneSurface(ctx context.Context, r *model.Repo) model.RepoSurfaces {
	byPrefix, err := b.client.RepoSurfaces(ctx)
	if err != nil {
		return model.ResolveRepoSurfaces(r.Kind, nil, nil)
	}
	return surfaceFor(byPrefix, r)
}

// boardWithSync builds a Board from a repo, issue count, the repo's
// sync status and its resolved nav-surface gates. Centralises the
// SyncEnabled / Sync* / Show* mapping so ListBoards, AddRepository and
// AddWorkspace stay in lockstep.
func boardWithSync(r *model.Repo, issueCount int, st client.SyncStatus, surfaces model.RepoSurfaces) Board {
	// A repo row written before the kind column existed scans as "git"
	// at the store boundary, but a *model.Repo constructed in Go can
	// still carry the zero value — normalise so the frontend's
	// 'git' | 'workspace' union is total.
	kind := string(r.Kind)
	if kind == "" {
		kind = string(model.RepoKindGit)
	}
	bd := Board{
		Prefix:                r.Prefix,
		Name:                  r.Name,
		Kind:                  kind,
		ShowAgentSurfaces:     surfaces.ShowAgentSurfaces,
		ShowKanban:            surfaces.ShowKanban,
		IssueCount:            issueCount,
		SyncEnabled:           st.Configured,
		SyncBackgroundEnabled: st.BackgroundEnabled,
		SyncMirroredBy:        st.MirroredBy,
	}
	bd.SyncInProgress = st.InProgress
	bd.SyncLastAt = st.LastSyncAt
	if st.LastError != nil {
		bd.SyncLastError = *st.LastError
	}
	return bd
}

// ListRepoActivity returns the per-repo activity summary the topbar's
// repository picker ranks itself by (BACI-369). One row per repo,
// including repos with nothing happening — the picker needs every repo
// in its ordering input.
func (b *BoardService) ListRepoActivity() ([]RepoActivity, error) {
	ctx := context.Background()
	rows, err := b.client.RepoActivities(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]RepoActivity, 0, len(rows))
	for _, a := range rows {
		out = append(out, RepoActivity{
			Prefix:         a.Prefix,
			LastActivityAt: a.LastActivityAt,
			ActiveJobs:     a.ActiveJobs,
		})
	}
	return out, nil
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
	return boardWithSync(repo, len(issues), st, b.oneSurface(ctx, repo)), nil
}

// AddWorkspace registers a manual workspace — a repo row with
// kind='workspace', no path and no remote — and returns it as a Board.
//
// It deliberately does NOT go through AddRepository's folder picker or
// git.Detect: a workspace has no directory at all, so the "%q is not
// inside a git repository" refusal that guards the git path is exactly
// the wrong check here. name is required; prefix is optional — empty
// allocates one from the name through the same AllocatePrefix machinery
// a git registration uses, so workspaces and git repos share one prefix
// namespace. The store bootstraps the mandatory catch-all features and
// the starter Kanban lanes as part of the insert.
func (b *BoardService) AddWorkspace(name, prefix string) (Board, error) {
	ctx := context.Background()
	repo, err := b.client.CreateWorkspace(ctx, client.WorkspaceCreateInput{
		Name:   name,
		Prefix: prefix,
	}, false)
	if err != nil {
		return Board{}, err
	}
	issues, err := b.client.ListIssues(ctx, client.IssueFilter{Repo: repo})
	if err != nil {
		return Board{}, err
	}
	// A workspace can't drive a sync tick of its own, but it IS mirrored
	// for free by the whole-DB export any git repo drives — so read the
	// status rather than assuming a zero one, and the badge can report
	// "mirrored by" straight away. Non-fatal on failure.
	var st client.SyncStatus
	if statuses, serr := b.client.SyncStatuses(ctx); serr == nil {
		for _, s := range statuses {
			if s.Prefix == repo.Prefix {
				st = s
				break
			}
		}
	}
	return boardWithSync(repo, len(issues), st, b.oneSurface(ctx, repo)), nil
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

// shippedSince derives the BACI-187 ShippedFilter.Since lower bound from
// the two mutually-exclusive frontend inputs. sinceTs (BACI-312) is an
// absolute RFC3339 cutoff — the local-midnight "Today" scope — and wins
// when non-empty; otherwise sinceDays drives the legacy relative window
// (0 = "Forever", no lower bound). The browser computes local-midnight in
// the user's timezone and passes it absolute, so this side stays
// timezone-agnostic.
func shippedSince(sinceDays int, sinceTs string) (*time.Time, error) {
	if strings.TrimSpace(sinceTs) != "" {
		ts, err := timeparse.Timestamp(sinceTs)
		if err != nil {
			return nil, err
		}
		return &ts, nil
	}
	if sinceDays > 0 {
		cutoff := time.Now().Add(-time.Duration(sinceDays) * 24 * time.Hour)
		return &cutoff, nil
	}
	return nil, nil
}

// ListShipped (BACI-187, reshaped for BACI-221) returns the
// recently-shipped issues (newest-first) wrapped with the
// total count under the same scope. sinceDays clamps the relative window
// (0 = no lower bound — "Forever"); sinceTs (BACI-312) is the absolute
// local-midnight "Today" cutoff and wins over sinceDays when non-empty;
// limit caps the row count (0 = the popover's default 20, max 100).
// Sibling of ListCards in shape, repo resolution included — one repo, or
// every repo when repoPrefix is empty or "all" (BACI-371, the popover's
// default scope).
func (b *BoardService) ListShipped(repoPrefix string, sinceDays int, sinceTs string, limit int) (ShippedListDTO, error) {
	ctx := context.Background()
	var repo *model.Repo
	if repoPrefix != "" && repoPrefix != "all" {
		r, err := b.client.GetRepoByPrefix(ctx, repoPrefix)
		if err != nil {
			return ShippedListDTO{}, err
		}
		repo = r
	}
	f := store.ShippedFilter{}
	if limit > 0 {
		if limit > 100 {
			limit = 100
		}
		f.Limit = limit
	}
	since, err := shippedSince(sinceDays, sinceTs)
	if err != nil {
		return ShippedListDTO{}, err
	}
	f.Since = since
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
			Key:            iss.Key,
			Title:          iss.Title,
			CustomerImpact: iss.CustomerImpact,
			Tags:           tags,
			FeatureSlug:    iss.FeatureSlug,
			FeatureEmoji:   iss.FeatureEmoji,
		}
		if iss.TerminalAt != nil {
			row.TerminalAt = *iss.TerminalAt
		}
		// First PR by insertion order — same rule as the HTTP handler.
		// A list error is non-fatal — the row still renders without
		// the PR chip. repo may be nil on the cross-repo scope; iss.Key
		// is canonical, so it resolves without a repo context.
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
// under the active Today / Last Week / Forever scope — for one repo, or
// across every repo when repoPrefix is empty or "all" (BACI-371).
// Polled on the same 10s cadence as the other live read endpoints so
// the Shipped pill reflects the current scope even
// when the popover isn't open. sinceDays==0 means "Forever" (no lower
// bound on terminal_at); sinceTs (BACI-312) is the absolute
// local-midnight "Today" cutoff and wins over sinceDays when non-empty.
func (b *BoardService) CountShipped(repoPrefix string, sinceDays int, sinceTs string) (int, error) {
	ctx := context.Background()
	var repo *model.Repo
	if repoPrefix != "" && repoPrefix != "all" {
		r, err := b.client.GetRepoByPrefix(ctx, repoPrefix)
		if err != nil {
			return 0, err
		}
		repo = r
	}
	since, err := shippedSince(sinceDays, sinceTs)
	if err != nil {
		return 0, err
	}
	return b.client.CountShippedIssues(ctx, repo, store.ShippedFilter{Since: since})
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
		Key:            iss.Key,
		Column:         string(iss.State),
		ColumnLabel:    stateLabel(iss.State),
		Title:          iss.Title,
		CustomerImpact: iss.CustomerImpact,
		Description:    iss.Description,
		Tags:           tags,
		Assignees:      assigneeList(iss.Assignee),
		Claude:         iss.Assignee == "claude",
		Comments:       comments,
		PullRequests:   prs,
		Documents:      docs,
		Claimants:      claimants,
		Taken:          view.Taken,
		LatestPlan:     latestPlanDTO(view.LatestPlan),
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
		Key:            iss.Key,
		Column:         string(iss.State),
		ColumnLabel:    stateLabel(iss.State),
		Title:          iss.Title,
		CustomerImpact: iss.CustomerImpact,
		Description:    iss.Description,
		Tags:           tags,
		Assignees:      assigneeList(iss.Assignee),
		Claude:         iss.Assignee == "claude",
		Taken:          brief.Taken,
		LatestPlan:     latestPlan,
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

// UpdateIssueTitle replaces an issue's title and returns the refreshed
// issue-drawer payload. Mirrors UpdateIssueDescription — the store rejects
// an empty title (invalid_input) so callers gate the value first.
// repoPrefix may be empty or "all" — the prefix is then derived from the
// canonical issue key.
func (b *BoardService) UpdateIssueTitle(repoPrefix, key, title string) (IssueDetail, error) {
	ctx := context.Background()
	repo, err := b.resolveRepoForKey(ctx, repoPrefix, key)
	if err != nil {
		return IssueDetail{}, err
	}
	if _, err := b.client.UpdateIssue(ctx, repo, key, client.IssueEdit{Title: &title}, false); err != nil {
		return IssueDetail{}, err
	}
	return b.GetIssue(repoPrefix, key)
}

// UpdateIssueCustomerImpact (BACI-349) replaces an issue's one-line
// customer impact and returns the refreshed issue-drawer payload. Unlike
// the title, an empty value is legitimate — it clears the field back to
// the "no impact" state — so the value is always sent as a non-nil
// pointer (empty = clear). repoPrefix may be empty or "all" — the prefix
// is then derived from the canonical issue key.
func (b *BoardService) UpdateIssueCustomerImpact(repoPrefix, key, customerImpact string) (IssueDetail, error) {
	ctx := context.Background()
	repo, err := b.resolveRepoForKey(ctx, repoPrefix, key)
	if err != nil {
		return IssueDetail{}, err
	}
	if _, err := b.client.UpdateIssue(ctx, repo, key, client.IssueEdit{CustomerImpact: &customerImpact}, false); err != nil {
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

// Pipeline (Wails seam): the desktop mirrors of the REST pipeline ops, so
// the desktop and web Pipeline pages drive the same client methods. Card
// mutations reshape to a BoardCard (carrying the issue's taken flag);
// chain mutations return the refreshed job collection.

func (b *BoardService) ReorderCard(repoPrefix, key string, position int) (BoardCard, error) {
	ctx := context.Background()
	repo, err := b.resolveRepoForKey(ctx, repoPrefix, key)
	if err != nil {
		return BoardCard{}, err
	}
	iss, err := b.client.ReorderIssue(ctx, repo, key, position, false)
	if err != nil {
		return BoardCard{}, err
	}
	return cardFromIssue(iss, iss.Taken), nil
}

// CreateRelation wires a `blocks` edge so `blocked` ends up blocked by
// `blocker` — the Pipeline drag-to-block gesture (BACI-342). A `blocks`
// edge is stored from = blocker, to = blocked, so the drop target (the
// blocker) is `blocker` and the dragged card (which becomes blocked) is
// `blocked`. type is hard-coded to "blocks"; the gesture only creates
// blocks/blocked-by. The store's INSERT OR IGNORE makes a duplicate edge
// a silent no-op, and the React caller guards a self-drop, so neither
// reaches this method as an error in normal use. The repo is resolved
// from the blocker key (both cards share the active board's repo).
func (b *BoardService) CreateRelation(repoPrefix, blocker, blocked string) error {
	ctx := context.Background()
	repo, err := b.resolveRepoForKey(ctx, repoPrefix, blocker)
	if err != nil {
		return err
	}
	_, err = b.client.LinkRelation(ctx, repo, inputs.LinkInput{
		From: blocker,
		Type: "blocks",
		To:   blocked,
	}, false)
	return err
}

// SetCardProcess materialises a card's job chain from either a preset
// slug (process) or an explicit ordered stage list (stages) — mutually
// exclusive. The cumulative-stepper picker sends stages; the kept
// skip-Plan preset buttons send a slug.
func (b *BoardService) SetCardProcess(repoPrefix, key, process string, stages []string) ([]*model.PipelineJob, error) {
	ctx := context.Background()
	repo, err := b.resolveRepoForKey(ctx, repoPrefix, key)
	if err != nil {
		return nil, err
	}
	return b.client.SetIssueProcess(ctx, repo, key, process, stages, false)
}

// EditCardProcessTail edits the pending tail of an in_pipeline card's job
// chain (BACI-294): the completed / running / cancelled jobs stay as a
// locked prefix; stages is the re-ordered pending tail that replaces the
// card's pending jobs, re-sequenced after the prefix.
func (b *BoardService) EditCardProcessTail(repoPrefix, key string, stages []string) ([]*model.PipelineJob, error) {
	ctx := context.Background()
	repo, err := b.resolveRepoForKey(ctx, repoPrefix, key)
	if err != nil {
		return nil, err
	}
	return b.client.EditIssueProcessTail(ctx, repo, key, stages, false)
}

// ResetCardProcess wipes an in_pipeline card's ENTIRE job chain (BACI-314)
// so it drops back to the from-scratch "Pick a process" picker. The UI
// never dry-runs, so dryRun is always false. Refused by the engine while a
// job is running (the UI hides Reset in that state; the client surfaces the
// error otherwise). Returns the refreshed (empty) chain.
func (b *BoardService) ResetCardProcess(repoPrefix, key string) ([]*model.PipelineJob, error) {
	ctx := context.Background()
	repo, err := b.resolveRepoForKey(ctx, repoPrefix, key)
	if err != nil {
		return nil, err
	}
	return b.client.ResetIssueProcess(ctx, repo, key, false)
}

func (b *BoardService) StartCardJob(repoPrefix, key string) ([]*model.PipelineJob, error) {
	ctx := context.Background()
	repo, err := b.resolveRepoForKey(ctx, repoPrefix, key)
	if err != nil {
		return nil, err
	}
	return b.client.StartPipelineJob(ctx, repo, key)
}

func (b *BoardService) StopCardJob(repoPrefix, key string) ([]*model.PipelineJob, error) {
	ctx := context.Background()
	repo, err := b.resolveRepoForKey(ctx, repoPrefix, key)
	if err != nil {
		return nil, err
	}
	return b.client.StopPipelineJob(ctx, repo, key)
}

// RerunCardJob re-runs an aborted (cancelled) job at the given 1-based
// sequence — the BACI-291 per-job Re-run control. Returns the refreshed
// chain.
func (b *BoardService) RerunCardJob(repoPrefix, key string, seq int) ([]*model.PipelineJob, error) {
	ctx := context.Background()
	repo, err := b.resolveRepoForKey(ctx, repoPrefix, key)
	if err != nil {
		return nil, err
	}
	return b.client.RerunPipelineJob(ctx, repo, key, seq)
}

func (b *BoardService) SetCardEngineMode(repoPrefix, key, mode string) (BoardCard, error) {
	ctx := context.Background()
	repo, err := b.resolveRepoForKey(ctx, repoPrefix, key)
	if err != nil {
		return BoardCard{}, err
	}
	iss, err := b.client.SetEngineMode(ctx, repo, key, mode)
	if err != nil {
		return BoardCard{}, err
	}
	return cardFromIssue(iss, iss.Taken), nil
}

func (b *BoardService) ShipCard(repoPrefix, key string) (BoardCard, error) {
	ctx := context.Background()
	repo, err := b.resolveRepoForKey(ctx, repoPrefix, key)
	if err != nil {
		return BoardCard{}, err
	}
	iss, err := b.client.ShipIssue(ctx, repo, key, false)
	if err != nil {
		return BoardCard{}, err
	}
	return cardFromIssue(iss, iss.Taken), nil
}

// MarkCardDone is the in_pipeline → done hand-off (BACI-352): close a card
// out as done directly, bypassing the Shipping column and the ship agent.
// The direct-done counterpart to ShipCard.
func (b *BoardService) MarkCardDone(repoPrefix, key string) (BoardCard, error) {
	ctx := context.Background()
	repo, err := b.resolveRepoForKey(ctx, repoPrefix, key)
	if err != nil {
		return BoardCard{}, err
	}
	iss, err := b.client.MarkDoneIssue(ctx, repo, key, false)
	if err != nil {
		return BoardCard{}, err
	}
	return cardFromIssue(iss, iss.Taken), nil
}

func (b *BoardService) SetAutoShip(repoPrefix string, enabled bool) (bool, error) {
	ctx := context.Background()
	repo, err := b.client.GetRepoByPrefix(ctx, repoPrefix)
	if err != nil {
		return false, err
	}
	return b.client.SetRepoAutoShip(ctx, repo, enabled, false)
}

// SetShowAgentSurfaces persists the per-space "Agent Mode" gate — which
// controls whether the Agentic Pipeline, Agents and Monitor tabs are in
// the top nav for this space.
//
// There is no Get twin: the resolved value already rides every Board
// from ListBoards, and the Settings toggle patches the provider's board
// list optimistically rather than re-reading.
func (b *BoardService) SetShowAgentSurfaces(repoPrefix string, enabled bool) (bool, error) {
	ctx := context.Background()
	repo, err := b.client.GetRepoByPrefix(ctx, repoPrefix)
	if err != nil {
		return false, err
	}
	return b.client.SetRepoShowAgentSurfaces(ctx, repo, enabled, false)
}

// SetShowKanban persists the per-space "Show Kanban Board" gate.
func (b *BoardService) SetShowKanban(repoPrefix string, enabled bool) (bool, error) {
	ctx := context.Background()
	repo, err := b.client.GetRepoByPrefix(ctx, repoPrefix)
	if err != nil {
		return false, err
	}
	return b.client.SetRepoShowKanban(ctx, repo, enabled, false)
}

// GetAutoShip reads the per-repo Shipping-column auto-ship toggle so the
// Pipeline's Shipping switch seeds from the DB (the value the
// controller's auto-ship ticker acts on), not a local cache.
func (b *BoardService) GetAutoShip(repoPrefix string) (bool, error) {
	ctx := context.Background()
	repo, err := b.client.GetRepoByPrefix(ctx, repoPrefix)
	if err != nil {
		return false, err
	}
	return b.client.GetRepoAutoShip(ctx, repo)
}

// SetBacklogCollapsed (BACI-288) persists the per-repo Pipeline
// Backlog-column collapse preference and returns the resulting value.
func (b *BoardService) SetBacklogCollapsed(repoPrefix string, collapsed bool) (bool, error) {
	ctx := context.Background()
	repo, err := b.client.GetRepoByPrefix(ctx, repoPrefix)
	if err != nil {
		return false, err
	}
	return b.client.SetRepoBacklogCollapsed(ctx, repo, collapsed, false)
}

// GetBacklogCollapsed (BACI-288) reads the per-repo Pipeline
// Backlog-column collapse preference so the Pipeline page seeds its rail
// state from the persisted KV, not a local cache.
func (b *BoardService) GetBacklogCollapsed(repoPrefix string) (bool, error) {
	ctx := context.Background()
	repo, err := b.client.GetRepoByPrefix(ctx, repoPrefix)
	if err != nil {
		return false, err
	}
	return b.client.GetRepoBacklogCollapsed(ctx, repo)
}

// SetImpactPrimary (BACI-349) persists the per-repo Pipeline
// impact-primary display preference and returns the resulting value.
func (b *BoardService) SetImpactPrimary(repoPrefix string, impactPrimary bool) (bool, error) {
	ctx := context.Background()
	repo, err := b.client.GetRepoByPrefix(ctx, repoPrefix)
	if err != nil {
		return false, err
	}
	return b.client.SetRepoImpactPrimary(ctx, repo, impactPrimary, false)
}

// GetImpactPrimary (BACI-349) reads the per-repo Pipeline impact-primary
// display preference so the Pipeline page seeds its toggle state from the
// persisted KV, not a local cache.
func (b *BoardService) GetImpactPrimary(repoPrefix string) (bool, error) {
	ctx := context.Background()
	repo, err := b.client.GetRepoByPrefix(ctx, repoPrefix)
	if err != nil {
		return false, err
	}
	return b.client.GetRepoImpactPrimary(ctx, repo)
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
// featureSlug (Phase 4) optionally associates the new issue with a
// feature at creation time. Empty defers to the repo default feature
// (store.ResolveCreateIssueFeatureID) — features are mandatory, so the
// composer always supplies one, but an empty string stays valid.
// autoRun (BACI-374) arms the new card to run the full
// Scope → Plan → Implement → Ship chain under the engine; the composer
// switch defaults it on, and the returned card comes back in the
// Pipeline column rather than Backlog.
func (b *BoardService) AddIssue(repoPrefix, title, description, featureSlug string, autoRun bool) (BoardCard, error) {
	ctx := context.Background()
	if repoPrefix == "" || repoPrefix == "all" {
		return BoardCard{}, fmt.Errorf("AddIssue: a repo prefix is required (cross-repo pseudo-board has no target)")
	}
	repo, err := b.client.GetRepoByPrefix(ctx, repoPrefix)
	if err != nil {
		return BoardCard{}, err
	}
	iss, err := b.client.CreateIssue(ctx, repo, inputs.IssueAddInput{
		Title: title, Description: description, FeatureSlug: featureSlug, AutoRun: autoRun,
	}, false)
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

// SendSessionMessage (BACI-286) enqueues a user→agent steer message at a
// busy session — the Pipeline running-job card / Agents-page session
// card "message" button. The channel serving that session pushes it as a
// `<channel kind="message">` tag at the worker's next turn boundary.
// NOT a dispatch — no matcher, no ack lifecycle. An unknown session id
// or empty/over-cap body surfaces as an error the frontend toasts.
func (b *BoardService) SendSessionMessage(sessionID, body string) (*model.UserMessage, error) {
	return b.client.AddUserMessage(context.Background(), client.AddUserMessageInput{
		SessionID: sessionID,
		Body:      body,
	})
}

// ListNotifications (BACI-287) returns the global (cross-repo) agent→user
// notification list, newest-first — the notification bell's dropdown.
// unreadOnly drops read rows (the bell's default); pass false for the
// "show all" filter. limit <= 0 falls back to the store default cap.
func (b *BoardService) ListNotifications(unreadOnly bool, limit int) ([]*model.Notification, error) {
	rows, err := b.client.ListNotifications(context.Background(), client.NotificationListFilter{
		UnreadOnly: unreadOnly,
		Limit:      limit,
	})
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []*model.Notification{}
	}
	return rows, nil
}

// CountUnreadNotifications (BACI-287) returns the cross-repo unread badge
// count the bell polls.
func (b *BoardService) CountUnreadNotifications() (int, error) {
	return b.client.CountUnreadNotifications(context.Background(), nil)
}

// GetNotification (BACI-287) fetches one notification by id.
func (b *BoardService) GetNotification(id int64) (*model.Notification, error) {
	return b.client.GetNotification(context.Background(), id)
}

// MarkNotificationRead (BACI-287) stamps read_at on one notification —
// the bell's per-row "mark read". Idempotent.
func (b *BoardService) MarkNotificationRead(id int64) (*model.Notification, error) {
	return b.client.MarkNotificationRead(context.Background(), id, false)
}

// MarkAllNotificationsRead (BACI-287) marks every unread row read,
// cross-repo — the bell's "mark all read" footer. Returns the count flipped.
func (b *BoardService) MarkAllNotificationsRead() (int, error) {
	return b.client.MarkAllNotificationsRead(context.Background(), nil, false)
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

// ---------------------------------------------------------------------
// Kanban lanes — the human board, orthogonal to model.State.
//
// A card is on the Kanban iff its kanban_column_id is non-NULL. That
// NULL is what stops the Kanban and the Agentic Pipeline double-
// rendering the same `todo` card: a git repo's issues start OFF the
// board and are opted in explicitly, while a workspace's issues default
// onto the first lane at create time.
//
// Lanes are addressed by **uuid**, never their int64 id — uuid is the
// only lane identity that survives a sync round trip. NOTE the naming:
// the pre-existing BoardColumn / ListColumns pair is the legacy
// state-keyed board and is deliberately left alone (the per-repo
// settings pane still consumes it). Everything below is KanbanColumn.
// ---------------------------------------------------------------------

// KanbanCardRefDTO is one card's membership of a lane: the issue key
// plus its 0-based slot. Deliberately a *reference*, not a card — the
// React side already holds the full BoardCard rows from ListCards and
// joins them by key, so lane membership costs one small array rather
// than a second copy of every card payload.
type KanbanCardRefDTO struct {
	Key      string `json:"key"`
	Position int    `json:"position"`
}

// KanbanColumnDTO is one lane plus its ordered card references. Cards is
// always non-nil (an empty lane serialises as []) so the React side can
// map over it without a guard.
type KanbanColumnDTO struct {
	UUID     string             `json:"uuid"`
	Name     string             `json:"name"`
	Position int                `json:"position"`
	Cards    []KanbanCardRefDTO `json:"cards"`
}

// KanbanColumnDeletePreviewDTO is the blast radius of deleting a lane:
// the cards it holds come OFF the board (kanban_column_id back to NULL).
// The issues themselves are never deleted.
type KanbanColumnDeletePreviewDTO struct {
	UUID                   string `json:"uuid"`
	Name                   string `json:"name"`
	IssuesRemovedFromBoard int    `json:"issuesRemovedFromBoard"`
}

// requireRepo resolves a concrete repo, rejecting the empty / "all"
// pseudo-board. Kanban lanes are per-repo, so unlike ListCards there is
// no meaningful cross-repo board to assemble.
func (b *BoardService) requireRepo(ctx context.Context, repoPrefix string) (*model.Repo, error) {
	if repoPrefix == "" || repoPrefix == "all" {
		return nil, fmt.Errorf("select a repository to view its Kanban board")
	}
	return b.client.GetRepoByPrefix(ctx, repoPrefix)
}

// kanbanBoard assembles the lanes with their ordered membership. It
// applies the same two visibility filters ListCards does — the
// display.show_archived global and the per-repo hidden-feature set — so
// a lane never references a card the board didn't ship.
func (b *BoardService) kanbanBoard(ctx context.Context, repo *model.Repo) ([]KanbanColumnDTO, error) {
	cols, err := b.client.ListKanbanColumns(ctx, repo)
	if err != nil {
		return nil, err
	}
	showArchived, _ := b.client.GetDisplayShowArchived(ctx)
	var hiddenSlugs []string
	if slugs, herr := b.client.ListHiddenFeatureSlugs(ctx, repo); herr == nil {
		hiddenSlugs = slugs
	}
	issues, err := b.client.ListIssues(ctx, client.IssueFilter{
		Repo:               repo,
		IncludeArchived:    showArchived,
		HiddenFeatureSlugs: hiddenSlugs,
	})
	if err != nil {
		return nil, err
	}
	byColumn := make(map[int64][]KanbanCardRefDTO, len(cols))
	for _, iss := range issues {
		if iss.KanbanColumnID == nil {
			continue // not on the board
		}
		id := *iss.KanbanColumnID
		byColumn[id] = append(byColumn[id], KanbanCardRefDTO{
			Key:      iss.Key,
			Position: iss.KanbanPosition,
		})
	}
	out := make([]KanbanColumnDTO, 0, len(cols))
	for _, c := range cols {
		refs := byColumn[c.ID]
		// Kanban positions are a DENSE 0-based index maintained by the
		// store, but sorting here keeps the lane stable even if an older
		// binary left a gap.
		sort.SliceStable(refs, func(i, j int) bool { return refs[i].Position < refs[j].Position })
		if refs == nil {
			refs = []KanbanCardRefDTO{}
		}
		out = append(out, KanbanColumnDTO{
			UUID:     c.UUID,
			Name:     c.Name,
			Position: c.Position,
			Cards:    refs,
		})
	}
	return out, nil
}

func pickKanbanColumn(cols []KanbanColumnDTO, uuid string) (KanbanColumnDTO, error) {
	for _, c := range cols {
		if c.UUID == uuid {
			return c, nil
		}
	}
	return KanbanColumnDTO{}, fmt.Errorf("kanban column %s not found after write", uuid)
}

// ListKanbanColumns returns the repo's lanes in board order, each with
// its ordered card references.
func (b *BoardService) ListKanbanColumns(repoPrefix string) ([]KanbanColumnDTO, error) {
	ctx := context.Background()
	repo, err := b.requireRepo(ctx, repoPrefix)
	if err != nil {
		return nil, err
	}
	return b.kanbanBoard(ctx, repo)
}

// CreateKanbanColumn appends a lane to the right-hand end of the board.
func (b *BoardService) CreateKanbanColumn(repoPrefix, name string) (KanbanColumnDTO, error) {
	ctx := context.Background()
	repo, err := b.requireRepo(ctx, repoPrefix)
	if err != nil {
		return KanbanColumnDTO{}, err
	}
	col, err := b.client.CreateKanbanColumn(ctx, repo, name, false)
	if err != nil {
		return KanbanColumnDTO{}, err
	}
	cols, err := b.kanbanBoard(ctx, repo)
	if err != nil {
		return KanbanColumnDTO{}, err
	}
	return pickKanbanColumn(cols, col.UUID)
}

// RenameKanbanColumn renames a lane in place; its position and cards are
// untouched.
func (b *BoardService) RenameKanbanColumn(repoPrefix, uuid, newName string) (KanbanColumnDTO, error) {
	ctx := context.Background()
	repo, err := b.requireRepo(ctx, repoPrefix)
	if err != nil {
		return KanbanColumnDTO{}, err
	}
	if _, err := b.client.RenameKanbanColumn(ctx, repo, uuid, newName, false); err != nil {
		return KanbanColumnDTO{}, err
	}
	cols, err := b.kanbanBoard(ctx, repo)
	if err != nil {
		return KanbanColumnDTO{}, err
	}
	return pickKanbanColumn(cols, uuid)
}

// ReorderKanbanColumn moves a lane to `position` (0-based, clamped to
// the board). It returns the WHOLE refreshed board rather than the moved
// lane: the siblings re-densify underneath, so any caller rendering
// board order has to re-read them anyway.
func (b *BoardService) ReorderKanbanColumn(repoPrefix, uuid string, position int) ([]KanbanColumnDTO, error) {
	ctx := context.Background()
	repo, err := b.requireRepo(ctx, repoPrefix)
	if err != nil {
		return nil, err
	}
	if _, err := b.client.ReorderKanbanColumn(ctx, repo, uuid, position, false); err != nil {
		return nil, err
	}
	return b.kanbanBoard(ctx, repo)
}

// PreviewDeleteKanbanColumn reports how many cards a lane delete would
// take off the board, WITHOUT deleting anything — the dry-run behind the
// confirmation dialog. Pairs with DeleteKanbanColumn.
func (b *BoardService) PreviewDeleteKanbanColumn(repoPrefix, uuid string) (KanbanColumnDeletePreviewDTO, error) {
	ctx := context.Background()
	repo, err := b.requireRepo(ctx, repoPrefix)
	if err != nil {
		return KanbanColumnDeletePreviewDTO{}, err
	}
	_, preview, err := b.client.DeleteKanbanColumn(ctx, repo, uuid, true)
	if err != nil {
		return KanbanColumnDeletePreviewDTO{}, err
	}
	if preview == nil || preview.Column == nil {
		return KanbanColumnDeletePreviewDTO{}, fmt.Errorf("kanban column %s: no delete preview returned", uuid)
	}
	return KanbanColumnDeletePreviewDTO{
		UUID:                   preview.Column.UUID,
		Name:                   preview.Column.Name,
		IssuesRemovedFromBoard: preview.Cascade.IssuesRemovedFromBoard,
	}, nil
}

// DeleteKanbanColumn removes a lane for real and takes its cards off the
// board (the issues themselves survive). Returns the refreshed board.
func (b *BoardService) DeleteKanbanColumn(repoPrefix, uuid string) ([]KanbanColumnDTO, error) {
	ctx := context.Background()
	repo, err := b.requireRepo(ctx, repoPrefix)
	if err != nil {
		return nil, err
	}
	if _, _, err := b.client.DeleteKanbanColumn(ctx, repo, uuid, false); err != nil {
		return nil, err
	}
	return b.kanbanBoard(ctx, repo)
}

// SetIssueKanbanColumn places a card on the Kanban — the drag-drop
// write. columnUUID == "" takes the card OFF the board entirely; that
// empty string is the only way to un-opt a git-repo card, so it is a
// meaningful value rather than a missing argument.
//
// position is the 0-based top-to-bottom slot in the target lane; pass
// null to append to the end. Both the source and the target lane
// re-densify to 0..n-1 around the move, so this returns the WHOLE
// refreshed board rather than the moved card — the card DTO has nowhere
// to carry a lane, and the caller needs the neighbours' new slots
// regardless.
func (b *BoardService) SetIssueKanbanColumn(repoPrefix, key, columnUUID string, position *int) ([]KanbanColumnDTO, error) {
	ctx := context.Background()
	repo, err := b.resolveRepoForKey(ctx, repoPrefix, key)
	if err != nil {
		return nil, err
	}
	if _, err := b.client.MoveIssueToKanbanColumn(ctx, repo, client.IssueKanbanMoveInput{
		IssueKey:   key,
		ColumnUUID: columnUUID,
		Position:   position,
	}, false); err != nil {
		return nil, err
	}
	return b.kanbanBoard(ctx, repo)
}
