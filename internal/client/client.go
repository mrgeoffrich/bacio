// Package client is the abstraction the CLI uses to talk to either the
// local SQLite store or a remote `bacio api` instance over HTTP. The
// selection happens once per invocation, in cli.openClient(), based on
// the --remote / BACIO_REMOTE flag.
//
// The local backend writes audit-log rows inline (matching what the CLI
// did before this package existed). The remote backend does not — the
// API server stamps audit rows on every mutation it accepts, so writing
// them client-side would double-count.
package client

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/git"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// Options configures the backend selection. Remote != "" picks the
// remote (HTTP) backend; otherwise the local (SQLite) backend opens
// DBPath (or store.DefaultPath() when DBPath is empty).
type Options struct {
	DBPath string
	Remote string
	Token  string
	Actor  string
}

// Mode names. Exposed so callers can branch on backend type for
// local-only verbs without comparing to magic strings inline.
const (
	ModeLocal  = "local"
	ModeRemote = "remote"
)

// ErrLocalOnly is returned by remote-backend methods that have no HTTP
// analogue (filesystem-touching operations on the developer machine).
// Callers should wrap with a verb-specific message when surfacing.
var ErrLocalOnly = errors.New("not supported in remote mode")

// RepoConfirmError is returned by DeleteRepo when the supplied
// `confirm` is missing or doesn't match the target prefix. The
// embedded Preview gives the caller everything needed to render the
// impact alert without a second round-trip; callers can errors.As
// against this type to recognise the case and format their warning.
type RepoConfirmError struct {
	Prefix     string
	GotConfirm string
	Preview    *RepoDeletePreview
}

func (e *RepoConfirmError) Error() string {
	if e.GotConfirm == "" {
		return "destructive operation requires --confirm <prefix>; ask the user before proceeding"
	}
	return "confirm value " + e.GotConfirm + " does not match repo prefix " + e.Prefix
}

// RepoLinkError is the typed-error envelope LinkPhantomRepo (BACI-112)
// returns for every refusal that has structured detail the HTTP handler
// must map to a specific status code. Kind is one of:
//   - "not_phantom"          — the row exists but already has a path.
//   - "path_not_absolute"    — the user passed a relative path.
//   - "path_not_exists"      — the path does not exist on disk.
//   - "path_not_git"         — the path exists but is not a git working tree.
//   - "path_already_bound"   — the path is already the working tree of another repo.
//   - "no_owning_sync_repo"  — no sync_remotes row carries repos/<prefix>/ on disk.
//
// CurrentPath / ExistingPrefix populate the per-Kind detail fields.
// `errors.As` against this type lets callers (handler, CLI renderer)
// branch on Kind without string-matching the message.
type RepoLinkError struct {
	Kind           string
	Prefix         string
	Path           string
	CurrentPath    string
	ExistingPrefix string
	Message        string
}

func (e *RepoLinkError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	switch e.Kind {
	case "not_phantom":
		return "repo " + e.Prefix + " is not a phantom (path=" + e.CurrentPath + ")"
	case "path_not_absolute":
		return "path must be absolute: " + e.Path
	case "path_not_exists":
		return "path does not exist: " + e.Path
	case "path_not_git":
		return "path is not a git working tree: " + e.Path
	case "path_already_bound":
		return "path " + e.Path + " is already bound to repo " + e.ExistingPrefix
	case "no_owning_sync_repo":
		return "no sync repository on this machine carries repos/" + e.Prefix + "/"
	}
	return "repo link: " + e.Kind
}

// Open constructs a Client based on opts. Remote backends do not open
// the local DB; the DBPath is ignored when Remote is set.
func Open(ctx context.Context, opts Options) (Client, error) {
	if strings.TrimSpace(opts.Remote) != "" {
		return newRemoteClient(opts)
	}
	return newLocalClient(opts)
}

// Client is the surface the CLI talks to. Methods that mutate take a
// dryRun bool; the local backend replicates today's in-memory dry-run
// projection, while the remote backend appends ?dry_run=true and
// trusts the server's projection.
//
// All methods are safe to call from any goroutine on the local
// backend (database/sql pools internally). The remote backend is
// http.Client-backed and equally goroutine-safe.
type Client interface {
	// Mode reports "local" or "remote" so callers can short-circuit
	// for verbs that have no remote analogue.
	Mode() string
	Close() error

	// ----- Repos -----
	ListRepos(ctx context.Context) ([]*model.Repo, error)
	GetRepoByPrefix(ctx context.Context, prefix string) (*model.Repo, error)
	GetRepoByPath(ctx context.Context, path string) (*model.Repo, error)
	// EnsureRepo resolves the repo for the given git working tree,
	// creating it on first use. Mirrors the auto-register behaviour of
	// the previous resolveRepo() helper. created reports whether a new
	// row was inserted.
	EnsureRepo(ctx context.Context, info *git.Info) (repo *model.Repo, created bool, err error)
	// DeleteRepo removes the repo identified by prefix and every row
	// that hangs off it (issues, comments, features, documents, links,
	// relations, PRs, tags, TUI settings, history). Confirm MUST equal
	// prefix (case-insensitive) — the backend errors with
	// ErrRepoConfirmRequired and a populated preview otherwise, so
	// callers / agents can show the impact and ask the user before
	// retrying.
	DeleteRepo(ctx context.Context, prefix, confirm string, dryRun bool) (deletedRepo *model.Repo, preview *RepoDeletePreview, err error)
	// LinkPhantomRepo (BACI-112) binds a phantom repo (path=='') to a
	// local working tree at `path`. Runs UpgradePhantomRepo and writes
	// the project's .bacio/config.yaml pointing at the owning sync
	// repo's remote URL. The owning sync repo is discovered by walking
	// the sync_remotes registry and finding the first row whose local
	// clone carries repos/<prefix>/ on disk; if none does the call
	// fails with a no_owning_sync_repo RepoLinkError. Idempotent: when
	// the row's path already equals `path`, returns AlreadyLinked=true
	// and skips both the store write and the config write — no audit
	// row. Every other refusal returns a typed RepoLinkError so the
	// HTTP handler can map to the right status code without
	// string-matching. dryRun short-circuits before the upgrade and the
	// config write and returns the projected result.
	LinkPhantomRepo(ctx context.Context, prefix, path string, dryRun bool) (*RepoLinkResult, error)

	// ----- Features -----
	// ListFeatures returns the features in repo. Archived features
	// (BACI-68) are hidden by default — pass includeArchived=true to
	// inflate.
	ListFeatures(ctx context.Context, repo *model.Repo, withDescription, includeArchived bool) ([]*model.Feature, error)
	GetFeatureBySlug(ctx context.Context, repo *model.Repo, slug string) (*model.Feature, error)
	GetFeatureByID(ctx context.Context, repo *model.Repo, id int64) (*model.Feature, error)
	CreateFeature(ctx context.Context, repo *model.Repo, in inputs.FeatureAddInput, dryRun bool) (*model.Feature, error)
	// UpdateFeature accepts pointers + a presence-map decoded view of
	// the input: nil = field absent (no change), non-nil = field
	// present (apply). For emoji a non-nil empty string clears the
	// glyph (BACI-172); for branchName a non-nil empty string clears
	// the column back to NULL (ship to main; BACI-225).
	UpdateFeature(ctx context.Context, repo *model.Repo, slug string, title, description, emoji, branchName *string, dryRun bool) (*model.Feature, error)
	DeleteFeature(ctx context.Context, repo *model.Repo, slug string, dryRun bool) (deletedFeature *model.Feature, preview *FeatureDeletePreview, err error)
	ShowFeature(ctx context.Context, repo *model.Repo, slug string) (*FeatureView, error)
	// PlanFeature returns the feature's execution plan as a topo-sorted
	// list of issues with their `blocks` dependencies. The default
	// (includeClosed=false) emits only open issues and only edges
	// between two open issues — the historical shape backing
	// `bacio feature plan` and `bacio issue brief`. includeClosed=true
	// (BACI-236) additionally emits done/cancelled issues plus every
	// blocks edge whose endpoints are both in the feature, with
	// Closed=true stamped on each terminal entry; the desktop / web
	// dependency-graph view uses this path to mute closed nodes
	// alongside the live work.
	PlanFeature(ctx context.Context, repo *model.Repo, slug string, includeClosed bool) (*PlanView, error)
	// ArchiveFeature / UnarchiveFeature stamp / clear archived_at on
	// the feature row (BACI-68). Same semantics as ArchiveIssue.
	ArchiveFeature(ctx context.Context, repo *model.Repo, slug string, dryRun bool) (*model.Feature, error)
	UnarchiveFeature(ctx context.Context, repo *model.Repo, slug string, dryRun bool) (*model.Feature, error)
	// SetFeatureState (BACI-199) writes the feature's three-state
	// column (active|done|cancelled). Records an audit row
	// (`feature.state`) under the resolved actor.
	//
	// BACI-250 decoupled this from auto-close pinning: this method no
	// longer touches `state_manual`. Use SetFeatureAutoClose to flip
	// the sticky bit that gates the archive-sweep's auto-completion
	// pass.
	SetFeatureState(ctx context.Context, repo *model.Repo, slug string, state model.FeatureState, dryRun bool) (*model.Feature, error)
	// SetFeatureAutoClose (BACI-250) flips the per-feature auto-close
	// toggle — the sticky-bit `state_manual` column that gates the
	// BACI-199 auto-completion sweep. Auto-close ON clears the bit
	// (the sweep may promote this feature once every child is
	// terminal); auto-close OFF sets the bit (long-lived catch-alls
	// stay `active` indefinitely). Records an audit row
	// (`feature.auto-close`) on every real flip; idempotent no-ops
	// skip the audit. The remote backend hits PUT
	// /repos/{prefix}/features/{slug}/auto-close.
	SetFeatureAutoClose(ctx context.Context, repo *model.Repo, slug string, enabled, dryRun bool) (*model.Feature, error)
	// IsFeatureHiddenOnBoard / SetFeatureHiddenOnBoard / ListHiddenFeatureSlugs
	// (BACI-177) expose the per-feature "Show on board" toggle that
	// drives the Features-screen affordance. The flag is per-repo
	// display state (stored in tui_settings), not bacio state — same
	// shape as GetDisplayShowArchived / SetDisplayShowArchived. The
	// remote backend hits the REST endpoints
	// GET /repos/{prefix}/features/hidden and
	// PUT /repos/{prefix}/features/{slug}/hide.
	IsFeatureHiddenOnBoard(ctx context.Context, repo *model.Repo, slug string) (bool, error)
	SetFeatureHiddenOnBoard(ctx context.Context, repo *model.Repo, slug string, hidden, dryRun bool) (bool, error)
	ListHiddenFeatureSlugs(ctx context.Context, repo *model.Repo) ([]string, error)

	// ListBoardHiddenStates / SetBoardHiddenStates (BACI-248) expose
	// the per-repo `tui_settings[board.hidden_states]` KV — the set of
	// kanban-column state names hidden from the board on this machine.
	// Same shape as ListHiddenFeatureSlugs: a sorted slice of canonical
	// state names. Lives behind a per-repo board-hide endpoint pair
	// (GET/PUT /repos/{prefix}/board/hidden-states) so the desktop / web
	// Per-repository Settings pane can edit what was previously TUI-only
	// state. SetBoardHiddenStates is replace-not-merge: pass the full new
	// set. Unknown state names are silently dropped at the store boundary.
	// Records an audit row under `repo_setting.update`, target_label
	// `board.hidden_states` when the set actually changes; idempotent
	// no-op writes skip the audit row.
	ListBoardHiddenStates(ctx context.Context, repo *model.Repo) ([]string, error)
	SetBoardHiddenStates(ctx context.Context, repo *model.Repo, states []string, dryRun bool) ([]string, error)

	// GetDefaultFeature / SetDefaultFeature / ClearDefaultFeature (BACI-235)
	// expose the per-repo `default_feature` setting. When set, issues
	// created without an explicit `feature_slug` auto-apply to this
	// feature; an explicit slug always wins. GetDefaultFeature returns
	// nil when the setting is unset (or when the referenced feature has
	// been deleted — the FK ON DELETE SET NULL cleared the column).
	// SetDefaultFeature looks the slug up first (must exist in the same
	// repo) and records an audit row under `repo_setting.update`,
	// target_label `default_feature`. ClearDefaultFeature unsets the
	// column. The remote backend hits the REST endpoints
	// GET/PUT/DELETE /repos/{prefix}/settings/default-feature.
	GetDefaultFeature(ctx context.Context, repo *model.Repo) (*model.Feature, error)
	SetDefaultFeature(ctx context.Context, repo *model.Repo, slug string, dryRun bool) (*model.Feature, error)
	ClearDefaultFeature(ctx context.Context, repo *model.Repo, dryRun bool) error

	// ----- Issues -----
	// ResolveIssueKey converts a possibly-bare key ("42" or "MINI-42")
	// into the canonical PREFIX-N form, using repo as the implicit
	// current repo when needed. Returned key is always canonical.
	ResolveIssueKey(ctx context.Context, repo *model.Repo, key string) (string, error)
	ListIssues(ctx context.Context, f IssueFilter) ([]*model.Issue, error)
	GetIssueByKey(ctx context.Context, repo *model.Repo, key string) (*model.Issue, error)
	ShowIssue(ctx context.Context, repo *model.Repo, key string) (*IssueView, error)
	BriefIssue(ctx context.Context, repo *model.Repo, key string, opts BriefOptions) (*IssueBrief, error)
	CreateIssue(ctx context.Context, repo *model.Repo, in inputs.IssueAddInput, dryRun bool) (*model.Issue, error)
	UpdateIssue(ctx context.Context, repo *model.Repo, key string, edit IssueEdit, dryRun bool) (*model.Issue, error)
	SetIssueState(ctx context.Context, repo *model.Repo, key string, state model.State, dryRun bool) (*model.Issue, error)
	// Pipeline verbs (the CLI-facing subset — Start/Stop/engine-mode stay
	// API/engine-only). ReorderIssue moves a card within its Backlog /
	// Shipping ordering band; SetIssueProcess materialises a job chain from
	// either a preset slug (process) or an explicit ordered stage list
	// (stages) — mutually exclusive, resolved via model.ResolveProcess;
	// ShipIssue is the in_pipeline → to_be_shipped hand-off; SetRepoAutoShip
	// toggles the per-repo Shipping auto-ship.
	ReorderIssue(ctx context.Context, repo *model.Repo, key string, position int, dryRun bool) (*model.Issue, error)
	SetIssueProcess(ctx context.Context, repo *model.Repo, key, process string, stages []string, dryRun bool) ([]*model.PipelineJob, error)
	ShipIssue(ctx context.Context, repo *model.Repo, key string, dryRun bool) (*model.Issue, error)
	SetRepoAutoShip(ctx context.Context, repo *model.Repo, enabled, dryRun bool) (bool, error)
	// GetRepoAutoShip reads the per-repo Shipping auto-ship toggle — the
	// DB value the controller's auto-ship ticker acts on, so the UI can
	// seed the Shipping switch from the source of truth rather than a
	// local cache.
	GetRepoAutoShip(ctx context.Context, repo *model.Repo) (bool, error)
	// GetRepoBacklogCollapsed / SetRepoBacklogCollapsed (BACI-288) front
	// the per-repo Pipeline-page "collapse the Backlog column to a rail"
	// display preference, persisted in the `tui_settings` KV (purely
	// client-side chrome the backend never reads — unlike auto-ship).
	// Threaded through both transports so desktop (Wails) and web (REST,
	// GET/PUT /repos/{prefix}/backlog-collapsed) seed and persist the
	// toggle the same way. Set records a `repo_setting.update` audit row
	// when the value changes; get is read-only.
	GetRepoBacklogCollapsed(ctx context.Context, repo *model.Repo) (bool, error)
	SetRepoBacklogCollapsed(ctx context.Context, repo *model.Repo, collapsed, dryRun bool) (bool, error)
	// StartPipelineJob / StopPipelineJob / SetEngineMode are the engine
	// controls behind the in-process card's Start / Stop / Auto buttons.
	// They have no CLI verb (engine/UI only) but ARE on the client so the
	// desktop (Wails) and web (REST) surfaces drive the engine the same
	// way. Start/Stop return the refreshed job chain; SetEngineMode the
	// refreshed issue.
	StartPipelineJob(ctx context.Context, repo *model.Repo, key string) ([]*model.PipelineJob, error)
	StopPipelineJob(ctx context.Context, repo *model.Repo, key string) ([]*model.PipelineJob, error)
	// RerunPipelineJob re-runs an aborted (cancelled) job at the given
	// 1-based sequence — the BACI-291 per-job Re-run control. Engine/UI
	// only (no CLI verb), same class as Start/Stop. Returns the refreshed
	// chain.
	RerunPipelineJob(ctx context.Context, repo *model.Repo, key string, seq int) ([]*model.PipelineJob, error)
	SetEngineMode(ctx context.Context, repo *model.Repo, key, mode string) (*model.Issue, error)
	GetPipelineJobs(ctx context.Context, repo *model.Repo, key string) ([]*model.PipelineJob, error)
	AssignIssue(ctx context.Context, repo *model.Repo, key, assignee string, dryRun bool) (*model.Issue, error)
	UnassignIssue(ctx context.Context, repo *model.Repo, key string, dryRun bool) (*model.Issue, error)
	DeleteIssue(ctx context.Context, repo *model.Repo, key string, dryRun bool) (deletedIssue *model.Issue, preview *IssueDeletePreview, err error)
	PeekNextIssue(ctx context.Context, repo *model.Repo, slug string) (*model.Issue, error)
	ClaimNextIssue(ctx context.Context, repo *model.Repo, slug string, dryRun bool) (*model.Issue, error)
	// ArchiveIssue / UnarchiveIssue stamp / clear archived_at (BACI-68).
	// Archive is sticky — reopening an archived issue does NOT
	// auto-unarchive it. Idempotent: archiving an already-archived row
	// is a no-op. Records an audit row (issue.archive /
	// issue.unarchive) under the resolved actor.
	ArchiveIssue(ctx context.Context, repo *model.Repo, key string, dryRun bool) (*model.Issue, error)
	UnarchiveIssue(ctx context.Context, repo *model.Repo, key string, dryRun bool) (*model.Issue, error)
	// ListShippedIssues (BACI-187) returns recently-shipped issues
	// (state='done' AND terminal_at IS NOT NULL), newest-first by
	// terminal_at. Per-repo only; the popover the call backs is a
	// per-repo surface. f.RepoID is ignored here — the wrapper sets
	// it from `repo.ID` so the call shape matches the other per-repo
	// readers in this interface. The local backend delegates to
	// store.ListShippedIssues; the remote backend hits
	// GET /repos/{prefix}/shipped (DTOs decoded back into a sparse
	// *model.Issue — pull request URLs and other DTO-side fields are
	// not part of model.Issue and are dropped on the remote path).
	ListShippedIssues(ctx context.Context, repo *model.Repo, f store.ShippedFilter) ([]*model.Issue, error)
	// CountShippedIssues (BACI-221) is the sibling count read for the
	// Pipeline Shipping-column Shipped pill. Returns the total number of
	// shipped issues matching the same WHERE that ListShippedIssues
	// applies, ignoring f.Limit (the count is total under the scope, not
	// per-fetch). The pre-BACI-221 pill derived its count client-side
	// from the polled cards array, which undercounted (filtered by
	// show_archived + per-feature board-hide) and couldn't represent
	// "Forever"; moving
	// the count server-side fixes both. The local backend delegates to
	// store.CountShippedIssues; the remote backend hits
	// GET /repos/{prefix}/shipped/count.
	CountShippedIssues(ctx context.Context, repo *model.Repo, f store.ShippedFilter) (int, error)

	// ----- Comments / relations / PRs / tags -----
	ListComments(ctx context.Context, repo *model.Repo, key string) ([]*model.Comment, error)
	AddComment(ctx context.Context, repo *model.Repo, in inputs.CommentAddInput, dryRun bool) (*model.Comment, error)
	DeleteComment(ctx context.Context, repo *model.Repo, in inputs.CommentRmInput, dryRun bool) (preview *CommentDeletePreview, removed int64, err error)

	// ----- Feature comments (BACI-124) -----
	// Feature comments are the feature-scoped chronological-handoff
	// scratchpad implement-mode workers post to on close-out so the
	// next worker on a sibling issue in the same feature inherits the
	// context. Same shape as issue comments but parented on slug.
	ListFeatureComments(ctx context.Context, repo *model.Repo, slug string) ([]*model.FeatureComment, error)
	AddFeatureComment(ctx context.Context, repo *model.Repo, in inputs.FeatureCommentAddInput, dryRun bool) (*model.FeatureComment, error)
	DeleteFeatureComment(ctx context.Context, repo *model.Repo, in inputs.FeatureCommentRmInput, dryRun bool) (preview *FeatureCommentDeletePreview, removed int64, err error)

	LinkRelation(ctx context.Context, repo *model.Repo, in inputs.LinkInput, dryRun bool) (*model.Relation, error)
	UnlinkRelation(ctx context.Context, repo *model.Repo, in inputs.UnlinkInput, dryRun bool) (preview *RelationDeletePreview, removed int64, err error)

	ListPRs(ctx context.Context, repo *model.Repo, key string) ([]*model.PullRequest, error)
	AttachPR(ctx context.Context, repo *model.Repo, key, url string, dryRun bool) (*model.PullRequest, error)
	DetachPR(ctx context.Context, repo *model.Repo, key, url string, dryRun bool) (preview *PRDetachPreview, removed int64, err error)

	AddTags(ctx context.Context, repo *model.Repo, key string, tags []string, dryRun bool) (*model.Issue, error)
	RemoveTags(ctx context.Context, repo *model.Repo, key string, tags []string, dryRun bool) (*model.Issue, error)

	// ----- Documents -----
	// ListDocuments returns the docs in repo, optionally filtered to a
	// single type. archived docs are hidden by default — pass
	// includeArchived=true to inflate (BACI-68).
	ListDocuments(ctx context.Context, repo *model.Repo, typeStr string, includeArchived bool) ([]*model.Document, error)
	ShowDocument(ctx context.Context, repo *model.Repo, filename string, withContent bool) (*DocView, error)
	GetDocumentRaw(ctx context.Context, repo *model.Repo, filename string) (*model.Document, error)
	DownloadDocument(ctx context.Context, repo *model.Repo, filename string) (body []byte, err error)
	CreateDocument(ctx context.Context, repo *model.Repo, in DocCreateInput, dryRun bool) (*model.Document, error)
	UpsertDocument(ctx context.Context, repo *model.Repo, in DocCreateInput, dryRun bool) (*model.Document, error)
	EditDocument(ctx context.Context, repo *model.Repo, filename string, newType *string, newContent *string, dryRun bool) (*model.Document, error)
	RenameDocument(ctx context.Context, repo *model.Repo, oldName, newName, typeStr string, dryRun bool) (*model.Document, error)
	DeleteDocument(ctx context.Context, repo *model.Repo, filename string, dryRun bool) (deletedDocument *model.Document, preview *DocumentDeletePreview, err error)
	LinkDocument(ctx context.Context, repo *model.Repo, in inputs.DocLinkInput, dryRun bool) (*model.DocumentLink, error)
	UnlinkDocument(ctx context.Context, repo *model.Repo, in inputs.DocUnlinkInput, dryRun bool) (preview *DocumentUnlinkPreview, removed int64, err error)
	// ArchiveDocument / UnarchiveDocument stamp / clear archived_at
	// on the document row (BACI-68). Same semantics as ArchiveIssue.
	ArchiveDocument(ctx context.Context, repo *model.Repo, filename string, dryRun bool) (*model.Document, error)
	UnarchiveDocument(ctx context.Context, repo *model.Repo, filename string, dryRun bool) (*model.Document, error)
	// ArchiveSweep runs the BACI-68 archive sweep on demand — same
	// three SQL passes the leader-elected Controller runs hourly.
	// Local-only (mechanical janitor work; no remote surface).
	// Returns the per-pass counts.
	ArchiveSweep(ctx context.Context, dryRun bool) (store.ArchiveSweepResult, error)
	// GetDisplayShowArchived / SetDisplayShowArchived expose the
	// BACI-68 display.show_archived global toggle. Local-only.
	GetDisplayShowArchived(ctx context.Context) (bool, error)
	SetDisplayShowArchived(ctx context.Context, value, dryRun bool) (bool, error)
	// GetArchivePreferences / SetArchivePreferences expose the BACI-162
	// auto-archive settings (archive.auto_enabled and
	// archive.retention_days). Both fields are written atomically by
	// SetArchivePreferences; the validator on retention_days runs at
	// the client boundary so the HTTP / desktop callers get the same
	// rejection.
	GetArchivePreferences(ctx context.Context) (ArchivePreferences, error)
	SetArchivePreferences(ctx context.Context, in ArchivePreferences, dryRun bool) (ArchivePreferences, error)

	// SyncStatuses returns the BACI-89 background-sync status of every
	// tracked repo — last_sync_at, last error, configured, and the
	// global background_enabled toggle. The local backend reads the
	// store + each repo's machine-local .bacio/config.yaml; the
	// remote backend calls GET /sync. InProgress is process-local on
	// the leader and always false over HTTP from a non-leader.
	SyncStatuses(ctx context.Context) ([]SyncStatus, error)
	// GetSyncBackgroundEnabled / SetSyncBackgroundEnabled expose the
	// BACI-89 sync.background_enabled opt-out toggle.
	GetSyncBackgroundEnabled(ctx context.Context) (bool, error)
	SetSyncBackgroundEnabled(ctx context.Context, value, dryRun bool) (bool, error)
	// GetUIShippedSfx / SetUIShippedSfx expose the BACI-240
	// ui.shipped_sfx global toggle controlling whether the Pipeline
	// Shipping-column Shipped pill plays a ka-ching SFX on genuine
	// increments. Defaults to false (audio is opt-in).
	GetUIShippedSfx(ctx context.Context) (bool, error)
	SetUIShippedSfx(ctx context.Context, value, dryRun bool) (bool, error)
	// SyncRegistry (BACI-108) returns the registry of sync repos this
	// machine knows (one per sync_remotes row) with the project members
	// each carries, plus the residual tracked project repos that don't
	// yet have a sync.remote in their .bacio/config.yaml. Backs the
	// standalone Sync view on desktop / web. The local backend composes
	// the BACI-105 primitives (ListSyncRemotes + DiscoverMembership +
	// ReadProjectConfig); the remote calls GET /sync/repos.
	SyncRegistry(ctx context.Context) (*SyncRegistry, error)
	// SetupSync (BACI-111) sets up sync for one project repo against an
	// existing or new sync remote — the cross-transport entry point the
	// desktop / web setup modal drives. Three modes mirror the HTTP
	// surface: init (bootstrap a fresh sync repo), clone (clone an
	// existing one), attach (re-attach to a registry entry already on
	// this machine). On a renumber-collision refusal the returned
	// *SyncSetupResult carries PreviewCollisions and the returned error
	// is ErrSetupCollision; the caller re-runs with
	// in.AllowRenumber=true to confirm. Local backend acquires the
	// cross-process sync lock and dispatches into sync.Engine directly;
	// remote backend POSTs /repos/{prefix}/sync/setup and decodes the
	// 200 / 409 body.
	SetupSync(ctx context.Context, repo *model.Repo, in inputs.SyncSetupInput) (*SyncSetupResult, error)

	// ----- History -----
	// ListHistory queries the audit log. When repo is non-nil, results
	// are scoped to that repo (the remote backend uses the repo's
	// prefix in the URL). repo == nil means "across all repos".
	ListHistory(ctx context.Context, repo *model.Repo, f store.HistoryFilter) ([]*model.HistoryEntry, error)

	// ----- Agent registry (local-only in v1; remote returns ErrLocalOnly) -----
	// The agent registry records which AI agent sessions are alive
	// against which repos, and which issues they're focused on.
	// Local-only data — never synced. HTTP parity is a v2 follow-up;
	// the remote backend returns ErrLocalOnly for now so callers get a
	// clear "use --remote unset" message instead of a 404.
	RegisterAgent(ctx context.Context, repo *model.Repo, in inputs.AgentRegisterInput, dryRun bool) (*model.AgentSession, error)
	HeartbeatAgent(ctx context.Context, repo *model.Repo, in inputs.AgentHeartbeatInput, dryRun bool) (*model.AgentSession, error)
	EndAgent(ctx context.Context, repo *model.Repo, in inputs.AgentEndInput, dryRun bool) (*model.AgentSession, error)
	ClaimAgent(ctx context.Context, repo *model.Repo, in inputs.AgentClaimInput, dryRun bool) (*model.AgentClaim, error)
	ReleaseAgent(ctx context.Context, repo *model.Repo, in inputs.AgentReleaseInput, dryRun bool) (*model.AgentClaim, error)
	ListAgentSessions(ctx context.Context, f AgentSessionFilter) ([]*model.AgentSession, error)
	ShowAgentSession(ctx context.Context, sessionID string) (*AgentSessionView, error)
	// MarkAgentErrored records an Anthropic API failure on the session
	// (BACI-296), driven by the StopFailure hook; ClearAgentError wipes it
	// on the next successful heartbeat (recovery). FailPipelineForSession
	// reconciles the session's in-flight Pipeline job per the error class
	// (transient → pause the chain; terminal → move the card to
	// needs_action). All three are local-only — remote returns ErrLocalOnly.
	MarkAgentErrored(ctx context.Context, sessionID, errType, errMsg string) error
	ClearAgentError(ctx context.Context, sessionID string) error
	FailPipelineForSession(ctx context.Context, sessionID, errType, errMsg string) error
	// ListOpenClaims returns every open (unreleased) agent claim for repo,
	// or across all repos when repo is nil. Local-only; remote returns
	// ErrLocalOnly. Used by the desktop Board to derive each card's `taken`.
	ListOpenClaims(ctx context.Context, repo *model.Repo) ([]*model.AgentClaim, error)
	// OpenClaimsForSession returns the open (unreleased) claim issue keys
	// held by one alive session. Empty slice for an unknown / ended /
	// claimless session. Backs the BACI-126b issue-group gate: agents
	// must have at least one open claim (and the held set must include
	// the targeted key for key-bearing verbs) before `bacio issue *` /
	// adjacent groups will run. Local-only — remote returns ErrLocalOnly.
	OpenClaimsForSession(ctx context.Context, sessionID string) ([]string, error)
	// UpsertSessionTodoFromTask records one TaskCreate (insert) or
	// TaskUpdate (update by task_id) event from the PostToolUse hook.
	// issueKey stamps the inserted row's per-issue scope (BACI-62) so
	// the Agents view filters per-(session, issue); pass "" for orphan
	// rows the hook couldn't attribute to a single open claim. On the
	// update path the issueKey parameter is ignored — the row keeps the
	// issue key it was created with. dispatchID (BACI-132) stamps the
	// row's per-dispatch scope so two dispatches on the same issue get
	// separate task lists; pass nil for orphan rows (paired with an
	// empty issueKey) or when called from non-dispatched contexts. On
	// the update path dispatchID is ignored — the row keeps the
	// dispatch id it was created with. Local-only — the agent registry
	// has no HTTP write surface in v1.
	UpsertSessionTodoFromTask(ctx context.Context, sessionID, taskID, issueKey, content string, status model.TodoStatus, dispatchID *int64) error
	// ListSessionTodos returns the latest snapshot for one session,
	// position-ordered. issueKey == "" returns every row regardless of
	// issue scope (the back-compat path the REST `?issue_key=` unset
	// route uses); a non-empty issueKey filters to that issue's rows.
	// Empty slice for an unknown / empty session. Local-only.
	ListSessionTodos(ctx context.Context, sessionID, issueKey string) ([]model.SessionTodo, error)
	// ListTodosBySessionsAndIssue returns a session_pk → []SessionTodo
	// map keyed off the (session, issue) pairs the caller asked for
	// (BACI-62) — used by the desktop / TUI / web agent views to
	// hydrate the current job's todos for every visible card in one
	// trip. A session that's worked multiple issues only flows the
	// requested pair's rows back. Local-only.
	ListTodosBySessionsAndIssue(ctx context.Context, pairs []store.SessionIssuePair) (map[int64][]model.SessionTodo, error)
	// BlockersFor returns, keyed by blocked-issue id, every `blocks`
	// edge whose blocked-end id is in `ids` (BACI-114). Used by
	// boardcards.Assemble to derive each card's blocked-by-open marker
	// without N+1 lookups. Local-only — the remote backend errors with
	// ErrLocalOnly until the kanban surface switches to a bulk REST
	// endpoint (today the UI runs against the local DB / Wails / `bacio
	// web`, never `bacio --remote`).
	BlockersFor(ctx context.Context, ids []int64) (map[int64][]store.IssueBlocker, error)
	// CountEvalCommentsByIssue (BACI-141) returns issue_id → count of
	// eval-flagged comments. Used by boardcards.Assemble to render the
	// per-card eval-notes indicator on the kanban board. Local-only —
	// only the assembler uses it, and the assembler runs server-side on
	// the local client or the REST handler.
	CountEvalCommentsByIssue(ctx context.Context, ids []int64) (map[int64]int, error)
	// CountTranscriptDocsByIssue (BACI-141) returns issue_id → count of
	// `.jsonl` transcript documents linked to each issue. Matches both
	// typed-transcript rows (BACI-115) and legacy `project_complete` rows
	// whose filename matches the bacio-transcript naming convention.
	// Used alongside CountEvalCommentsByIssue to drive the kanban card's
	// combined transcript+eval indicator. Local-only.
	CountTranscriptDocsByIssue(ctx context.Context, ids []int64) (map[int64]int, error)
	// LatestPlanByIssue (BACI-216) returns issue_id → the newest
	// `plan`-typed document linked directly to each issue (issues
	// with no plan are absent from the map). Used by
	// boardcards.Assemble to fan the per-card plan affordance on so
	// the kanban doesn't need an extra fetch per card. Local-only —
	// same precedent as BlockersFor / CountTranscriptDocsByIssue:
	// the assembler runs server-side, never via `bacio --remote`.
	LatestPlanByIssue(ctx context.Context, ids []int64) (map[int64]*model.LatestPlan, error)
	// LatestPRByIssue (BACI-239) returns issue_id → the
	// most-recently-attached PR for each issue (issues with no PR
	// are absent from the map). Sibling of LatestPlanByIssue; used
	// by boardcards.Assemble to fan the per-card PR chip on so the
	// kanban doesn't need an extra fetch per card. Local-only.
	LatestPRByIssue(ctx context.Context, ids []int64) (map[int64]*model.LatestPR, error)
	// EnsureAgentIdentity mints a fresh persistent agent identity (a
	// random slug, retried against the UNIQUE constraint until it
	// sticks) and adopts it as this client's audit actor. It's the
	// `bacio hook` session-start path for a `claude` process with no
	// .bacio/agents.json entry yet — the caller records the returned
	// slug there. Does NOT create a session row. Local-only.
	EnsureAgentIdentity(ctx context.Context, repo *model.Repo) (string, error)
	// CreateSessionStub inserts a minimal agent_sessions row at SessionStart
	// — just session_id + repo + claude_pid + host + an "(unregistered)"
	// placeholder actor. agent identity, model, branch, permission_mode are
	// all left unset; registered_at stays NULL. The session is invisible to
	// the default-filtered agent list until the bacio channel's `register`
	// tool completes the registration via CompleteRegistration. Idempotent
	// on session_id (a /clear that fires a fresh SessionStart with a new
	// session_id is a separate stub). Local-only.
	CreateSessionStub(ctx context.Context, repo *model.Repo, sessionID, host string, claudePID int64) (*model.AgentSession, error)
	// SessionsByClaudePID returns the open sessions matching (host,
	// claudePID) — the channel's coordinates. Used by the bacio channel
	// MCP server to find which sessions it's serving so it can queue a
	// setup dispatch at each one. Local-only.
	SessionsByClaudePID(ctx context.Context, host string, claudePID int64) ([]*model.AgentSession, error)
	// CompleteRegistration is the register-tool counterpart to
	// CreateSessionStub: it enriches the stub with agent identity, model,
	// branch, permission_mode, channel_version, and stamps registered_at.
	// The agent identity is resolved or minted (via EnsureAgentIdentity
	// semantics) and recorded in .bacio/agents.json. Idempotent — re-running
	// register is a no-op on registered_at (first-mark-wins) and re-stamps
	// the other fields with the new values. Local-only.
	CompleteRegistration(ctx context.Context, repo *model.Repo, in inputs.AgentRegisterInput, channelVersion string) (*model.AgentSession, error)
	// UpsertAgentChannel records (or heartbeats) a live `bacio channel`
	// subprocess, keyed on the `claude` pid it descends from. agentName
	// is best-effort: an unknown/empty name just leaves the row's
	// agent_id NULL. Local-only — called from the channel poll loop.
	UpsertAgentChannel(ctx context.Context, repo *model.Repo, agentName, host string, claudePID, channelPID int64) error
	// LinkSessionChannel stamps claude_pid onto a session and lights up
	// channel_seen_at when a live agent_channels row matches (host,
	// claude_pid). The `bacio hook` side of the channel<->session join.
	// Local-only.
	LinkSessionChannel(ctx context.Context, sessionID string, claudePID int64, host string) error

	// ----- Agent questions (BACI-53; ask_user_question MCP tool) -----
	// Questions are the agent->user counterpart to dispatches: a
	// clarification an agent asked through the bacio channel's
	// ask_user_question MCP tool, parked until the user answers (or
	// dismisses) it through the TUI / desktop / web modal. Local-only
	// in v1; the REST routes (Phase 4) cover the desktop and web
	// surfaces.
	//
	// AddSessionQuestion is the channel's entry point — it inserts a new
	// open row, mints the request_uuid the channel uses to correlate
	// the parked JSON-RPC reply with the answered row on its next poll
	// tick, and emits a question.ask audit row.
	AddSessionQuestion(ctx context.Context, in AddSessionQuestionInput) (*model.SessionQuestion, error)
	// ListSessionQuestions returns the questions for one session,
	// optionally filtered by state. An empty states list returns every
	// state.
	ListSessionQuestions(ctx context.Context, sessionID string, states []model.QuestionState) ([]*model.SessionQuestion, error)
	// ListOpenQuestionsBySessions returns a session_pk -> []*SessionQuestion
	// map for the given session ids in one query — used by the desktop
	// and TUI agent views to hydrate open questions for every live
	// session in one trip.
	ListOpenQuestionsBySessions(ctx context.Context, sessionIDs []string) (map[int64][]*model.SessionQuestion, error)
	// PipelineJobsForIssues bulk-reads the Pipeline job chains for a set
	// of issue ids, keyed by issue_id — the board assembler attaches each
	// in_pipeline card's chain in one query. Server-side only (the board
	// is assembled local-side), so the remote client stubs it out.
	PipelineJobsForIssues(ctx context.Context, issueIDs []int64) (map[int64][]*model.PipelineJob, error)
	// GetSessionQuestion fetches one question by primary key.
	GetSessionQuestion(ctx context.Context, id int64) (*model.SessionQuestion, error)
	// AnswerSessionQuestion records the user's answer and flips the
	// row to `answered`. Emits a question.answer audit row.
	AnswerSessionQuestion(ctx context.Context, id int64, answers model.QuestionAnswers, dryRun bool) (*model.SessionQuestion, error)
	// CancelSessionQuestion dismisses an open question — the agent
	// receives a tool error on the next channel poll tick. Emits a
	// question.cancel audit row.
	CancelSessionQuestion(ctx context.Context, id int64, dryRun bool) (*model.SessionQuestion, error)
	// AbandonOpenQuestionsForSession is the channel's startup sweep —
	// any rows the previous channel process parked on are flipped to
	// `abandoned`. No audit row (mechanical janitor work; matches the
	// existing dispatch-drain precedent in internal/cli/hook.go).
	AbandonOpenQuestionsForSession(ctx context.Context, sessionID string) (int, error)
	// DrainSettledQuestionsForSession returns the answered + cancelled
	// rows for the channel's poll tick to re-correlate against its
	// in-memory parked-reply map. No audit row.
	DrainSettledQuestionsForSession(ctx context.Context, sessionID string) ([]*model.SessionQuestion, error)

	// ----- User→agent steer messages (BACI-286) -----
	// A steer message is the user→agent counterpart to a question: a
	// free-form note the user pushes at a specific busy session, delivered
	// by the channel at the worker's next turn boundary as a
	// `<channel kind="message">` tag. NOT a dispatch — no matcher, no ack
	// lifecycle, fire-and-forget.
	//
	// AddUserMessage enqueues one against a session id; the REST endpoint
	// is the only write surface (a steer-message is a harness affordance,
	// not a `bacio` mutation verb). Emits an agent.message audit row. The
	// remote backend supports it over REST; the drain stays local-only
	// (channel-internal, like the question drains).
	AddUserMessage(ctx context.Context, in AddUserMessageInput) (*model.UserMessage, error)
	// DrainUserMessagesForSession returns a session's un-consumed steer
	// messages oldest-first and marks them consumed — the channel's push
	// path. Local-only (the channel talks to the local store), so the
	// remote backend returns ErrLocalOnly. No audit row.
	DrainUserMessagesForSession(ctx context.Context, sessionID string) ([]*model.UserMessage, error)

	// ----- Agent→user notifications (BACI-287) -----
	// A notification is the agent→user counterpart to a steer message: a
	// non-blocking, fire-and-forget note a running agent sends the user via
	// the channel's `send_user_notification` MCP tool. Surfaced by a global
	// notification bell (desktop / web topbar) + a TUI Notifications tab.
	// NOT a dispatch and NOT a question — no matcher, no answer, no
	// lifecycle beyond unread→read.
	//
	// AddNotification inserts one against a repo (and optional session /
	// issue). The channel is the only write surface (the tool has no CLI /
	// REST mutate-equivalent, like the other channel tools). Emits a
	// notification.send audit row. Local-only — the channel always talks to
	// the local store, so the remote backend returns ErrLocalOnly.
	AddNotification(ctx context.Context, in AddNotificationInput) (*model.Notification, error)
	// ListNotifications returns notifications matching the filter,
	// newest-first — a nil RepoID lists cross-repo (the global bell). The
	// remote backend supports it over REST so a remote-mode UI's bell works.
	ListNotifications(ctx context.Context, filter NotificationListFilter) ([]*model.Notification, error)
	// CountUnreadNotifications returns the unread badge count — a nil repoID
	// counts cross-repo. REST parity for the remote-mode bell poll.
	CountUnreadNotifications(ctx context.Context, repoID *int64) (int, error)
	// GetNotification fetches one notification by id. REST parity.
	GetNotification(ctx context.Context, id int64) (*model.Notification, error)
	// MarkNotificationRead stamps read_at on one notification (idempotent).
	// Emits a notification.read audit row. REST parity.
	MarkNotificationRead(ctx context.Context, id int64, dryRun bool) (*model.Notification, error)
	// MarkAllNotificationsRead stamps read_at on every unread row (nil
	// repoID = cross-repo) and returns the count flipped. Emits a
	// notification.read-all audit row. REST parity.
	MarkAllNotificationsRead(ctx context.Context, repoID *int64, dryRun bool) (int, error)

	// ----- Agent dispatch queue (local-only in v1) -----
	// Dispatches are supervisor->agent work items. CreateDispatch
	// enqueues one; InboxDispatches drains everything aimed at a
	// session (its own id and its agent identity); AckDispatch records
	// the agent's acknowledgement.
	CreateDispatch(ctx context.Context, repo *model.Repo, in inputs.AgentDispatchInput, dryRun bool) (*model.AgentDispatch, error)
	InboxDispatches(ctx context.Context, sessionID string) ([]*model.AgentDispatch, error)
	AckDispatch(ctx context.Context, in inputs.AgentAckInput, dryRun bool) (*model.AgentDispatch, error)
	// CancelDispatch withdraws a pending or delivered dispatch (the
	// dispatcher's side of ack). Cancelling an acked dispatch is an
	// error; cancelling an already-cancelled one is a no-op. The
	// cancelled row stops satisfying WaitingDispatchForIssue, so the
	// kanban spinner on the targeted issue clears on the next refresh
	// (BACI-255).
	CancelDispatch(ctx context.Context, in inputs.AgentCancelInput, dryRun bool) (*model.AgentDispatch, error)
	// DrainDispatches returns a session's un-acked dispatches (pending
	// AND delivered) and marks any still-pending ones delivered — the
	// pull-delivery path used by the bacio hooks (which know their
	// session id from the hook payload). Delivered-but-un-acked
	// dispatches are returned every drain so a lost push is recovered on
	// the next prompt; only an ack retires a dispatch.
	DrainDispatches(ctx context.Context, sessionID string) ([]*model.AgentDispatch, error)
	// DrainAgentDispatches is the same drain, scoped to a repo + agent
	// identity rather than a session id — the push-delivery path used
	// by `bacio channel`, which (unlike a hook) is never told its
	// session id. Like DrainDispatches it returns un-acked dispatches;
	// the channel caller dedups per-process so it doesn't re-push every
	// poll tick. A nil repo or empty agent name drains nothing (the
	// channel runs idle rather than erroring).
	DrainAgentDispatches(ctx context.Context, repo *model.Repo, agentName string) ([]*model.AgentDispatch, error)
	// RepoDispatches returns every dispatch scoped to one repo, newest
	// first, regardless of status — the read surface the desktop Agents
	// screen needs. Local-only in v1.
	RepoDispatches(ctx context.Context, repo *model.Repo) ([]*model.AgentDispatch, error)
	// SessionDispatches returns every dispatch targeting one session,
	// newest first, regardless of status — used by the post-tool-use
	// hook to resolve the active dispatch id for a (session, issue) at
	// TaskCreate time (BACI-132). Matches both the bare session id and
	// the agent identity behind it (mirrors targetsSession in
	// internal/boardcards). Local-only.
	SessionDispatches(ctx context.Context, sessionID string) ([]*model.AgentDispatch, error)
	// EnsureSetupDispatch idempotently queues a dispatch telling the
	// agent to call the bacio channel's `register` tool — the path that
	// completes a SessionStart stub into a fully-registered session.
	// Scoped to one session_id (not an agent identity, since the agent
	// identity only exists post-register). Idempotent on
	// (TargetSessionID, CreatedBy=bacio-channel, status in [pending,
	// delivered]): returns the existing open dispatch rather than
	// creating a duplicate. sessionID="" is a no-op. Local-only.
	EnsureSetupDispatch(ctx context.Context, repo *model.Repo, sessionID string) (*model.AgentDispatch, error)
	// EnsurePingDispatch (BACI-57) idempotently queues an idle-check
	// ping at a session — the BACI-57 idle-pinger reaper's enqueue
	// side. Skipped when any pending|delivered ping already exists
	// for the session (CreatedBy=IdlePingDispatchCreator). Writes an
	// agent.dispatch audit row on insert. Local-only.
	EnsurePingDispatch(ctx context.Context, sess *model.AgentSession) (*model.AgentDispatch, error)
	// AutoDispatchIssue is the state-gated auto-pick dispatch verb
	// (BACI-40 + BACI-51): re-checks the mode's state-gate against
	// the issue's current state, then enqueues a target-less queued
	// dispatch the background matcher will bind to a free agent when
	// one frees up. The desktop per-card action button is the
	// original caller; REST `POST /repos/{prefix}/issues/{key}/dispatch`
	// and target-less `bacio agent dispatch <key> --mode <stage>`
	// both route through this so the three surfaces share the same
	// gate + enqueue path. Never errors with "no free agent" — that
	// case is now expressed as a queued dispatch sitting in the per-
	// (repo, mode) FIFO until the matcher binds it.
	AutoDispatchIssue(ctx context.Context, repo *model.Repo, issueKey, mode string, dryRun bool) (*model.AgentDispatch, error)

	// WaitingDispatchForIssue returns the active (queued / pending /
	// delivered) dispatch targeting an issue, or (nil, nil) when none
	// exists. Used by the BACI-51 spinner-as-cancel UI to resolve the
	// dispatch id without exposing dispatch internals through the
	// card DTO. Local-only — the remote backend returns ErrLocalOnly
	// today (REST parity is a follow-up).
	WaitingDispatchForIssue(ctx context.Context, repo *model.Repo, issueKey string) (*model.AgentDispatch, error)

	// CreateRescueDispatch (BACI-190) enqueues a `from="bacio-rescue"`
	// channel event asking an idle supervisor to handle a dead
	// worker's stranded worktree INLINE (no fresh Task spawn).
	// Validates the original dispatch is rescuable (pending/delivered,
	// per-mode creator, target session ended), picks an idle channel-
	// connected supervisor in the same repo, and writes one
	// `agent.rescue` audit row. Errors when the original isn't
	// eligible or no idle supervisor is available. Local-only —
	// remote returns ErrLocalOnly until REST parity lands as a follow-up.
	CreateRescueDispatch(ctx context.Context, dispatchID int64) (*model.AgentDispatch, error)

	// InflightByModeForRepo (BACI-145) returns mode → count of in-flight
	// dispatches in this repo, gated by the same staleness predicate the
	// matcher uses. Local-only — remote returns ErrLocalOnly.
	//
	// BACI-227: superseded for the kanban / brief WaitingState deriver
	// by InflightByModeBaseForRepo, which returns the same count
	// grouped per (mode, base_branch). Kept on the interface for
	// remote-parity bookkeeping and the chance that a future caller
	// genuinely wants the per-mode count regardless of branch.
	InflightByModeForRepo(ctx context.Context, repo *model.Repo) (map[model.DispatchMode]int, error)

	// InflightByModeBaseForRepo (BACI-227) is the per-(mode, branch)
	// sibling: returns store.InflightKey{Mode, BaseBranch} → count for
	// every (mode, branch) pair with at least one in-flight row.
	// Drives the kanban / brief assembler's WaitingState deriver so
	// the per-card concurrency-cap label tracks the matcher's
	// per-branch gate exactly. Local-only — remote returns
	// ErrLocalOnly.
	InflightByModeBaseForRepo(ctx context.Context, repo *model.Repo) (map[store.InflightKey]int, error)

	// ----- Prompt templates (local-only; `bacio settings template`) -----
	// ListPromptTemplates returns every registered template — slug,
	// name, body, state-gate, IsBuiltin — in stable order (created_at
	// ascending). This is the canonical iteration source for UIs that
	// list templates. Local-only — the remote backend returns
	// ErrLocalOnly.
	ListPromptTemplates(ctx context.Context) ([]*store.PromptTemplate, error)
	// GetPromptTemplate returns one template by slug. Local-only.
	GetPromptTemplate(ctx context.Context, slug string) (*store.PromptTemplate, error)
	// AddPromptTemplate creates a new template. Slug must be unique;
	// name must be unique case-insensitively. With dryRun set it
	// validates the payload but writes nothing — the returned template
	// has its server-time fields (ID, CreatedAt, UpdatedAt) left zero.
	// Local-only.
	AddPromptTemplate(ctx context.Context, in inputs.SettingsTemplateAddInput, dryRun bool) (*store.PromptTemplate, error)
	// RenamePromptTemplate renames a template — its slug and/or its
	// display name — cascading the slug change to
	// agent_dispatches.mode so historical dispatch rows continue to
	// resolve. With dryRun set it validates without writing. Local-only.
	RenamePromptTemplate(ctx context.Context, in inputs.SettingsTemplateRenameInput, dryRun bool) (*store.PromptTemplate, error)
	// DeletePromptTemplate removes a template by slug. Historical
	// dispatch rows that reference the slug are left intact (a dispatch
	// is a snapshot, not a live FK). With dryRun set it validates the
	// slug exists and projects the row that would have been removed,
	// without writing. Local-only.
	DeletePromptTemplate(ctx context.Context, in inputs.SettingsTemplateRmInput, dryRun bool) (*store.PromptTemplate, error)
	// RestoreBuiltinPromptTemplates re-seeds any built-in slug that
	// doesn't currently have a row from the embedded defaults.
	// Idempotent: existing rows are untouched. Returns the slugs that
	// were re-created. With dryRun set it inspects the table state and
	// returns the slugs it would have created without writing.
	// Local-only.
	RestoreBuiltinPromptTemplates(ctx context.Context, dryRun bool) ([]string, error)
	// SetPromptTemplateConcurrencyLimit (BACI-51) updates a template's
	// per-(repo, slug) in-flight cap the matcher enforces. limit must
	// be >= 0; 0 = unlimited. With dryRun set it validates without
	// writing. Local-only at the CLI level (the REST surface is the
	// PUT /settings/templates/{mode}/concurrency endpoint).
	SetPromptTemplateConcurrencyLimit(ctx context.Context, in inputs.SettingsTemplateSetConcurrencyInput, dryRun bool) (*store.PromptTemplate, error)
	// SetPromptTemplateActionLabel (BACI-67) updates a template's
	// imperative action_label override — the verb rendered on the
	// dispatch action menus instead of the gerund Name. An empty
	// ActionLabel clears the override (the UI then derives from Name
	// via the gerund→imperative rule). With dryRun set it validates
	// without writing. Local-only at the CLI level; matching REST is
	// PUT /settings/templates/{mode}/action-label.
	SetPromptTemplateActionLabel(ctx context.Context, in inputs.SettingsTemplateSetActionLabelInput, dryRun bool) (*store.PromptTemplate, error)

	// GetPromptTemplates is a legacy lookup shape for the dispatch
	// renderer paths that still expect a slug→body map. Equivalent to
	// iterating ListPromptTemplates and projecting Body. Local-only.
	//
	// Deprecated: prefer ListPromptTemplates for new code.
	GetPromptTemplates(ctx context.Context) (map[string]string, error)
	// SetPromptTemplate stores a custom body for one template slug —
	// the body-only edit path used by the desktop Save-on-blur flow.
	// An empty body reverts a built-in slug to its embedded default;
	// non-built-in slugs accept an empty body. With dryRun set it
	// validates and writes nothing. Local-only.
	//
	// Deprecated: prefer AddPromptTemplate / RenamePromptTemplate /
	// DeletePromptTemplate verbs for new agent-facing flows.
	SetPromptTemplate(ctx context.Context, mode, body string, dryRun bool) error
}

// ArchivePreferences holds the BACI-162 auto-archive settings,
// persisted in the global app_settings KV. AutoEnabled gates the
// hourly issue auto-archive pass (defaults to true — opt-OUT);
// RetentionDays is the number of days a terminal-state issue's
// terminal_at must sit before the next sweep archives it (default 7,
// range 1..3650). Both fields travel together: SetArchivePreferences
// writes the pair atomically, GetArchivePreferences reads them back.
type ArchivePreferences struct {
	AutoEnabled   bool `json:"auto_enabled"`
	RetentionDays int  `json:"retention_days"`
}

// AgentSessionFilter mirrors store.AgentSessionFilter; the wrapper lets
// CLI callers pass a *model.Repo (so the remote backend can resolve
// prefix when v2 wires it up) instead of a raw repo id.
type AgentSessionFilter struct {
	Repo           *model.Repo // nil = all repos
	OnlyAlive      bool
	RegisteredOnly bool // registered_at IS NOT NULL — default for TUI/desktop/CLI agent list
	Since          time.Time
}

// AgentSessionView bundles a session with its claim history for the
// `bacio agent show` view.
type AgentSessionView struct {
	Session *model.AgentSession `json:"session"`
	Claims  []*model.AgentClaim `json:"claims"`
}

// IssueFilter mirrors store.IssueFilter but also carries an optional
// repo-prefix for remote calls (the remote backend uses prefix in the
// URL path, so the int64 RepoID alone isn't enough).
type IssueFilter struct {
	Repo               *model.Repo
	AllRepos           bool
	FeatureSlug        string
	States             []model.State
	Tags               []string
	IncludeDescription bool
	// IncludeArchived (BACI-68) — when true, the list includes rows
	// with archived_at IS NOT NULL. Defaults to false; archived rows
	// are hidden from default lists / board / kanban / API JSON.
	IncludeArchived bool
	// HiddenFeatureSlugs (BACI-177) excludes issues whose feature has
	// a slug in this list — the per-feature "Show on board" toggle
	// from the Features screen. Empty slice = no filter. Threaded
	// straight through to the store-side IssueFilter; the local
	// backend uses it as-is, and the remote backend hasn't yet wired
	// a URL-query equivalent (the board-cards REST endpoint loads
	// the set server-side so the wire shape doesn't need it).
	HiddenFeatureSlugs []string
}

// IssueEdit is the parameter bundle for UpdateIssue. Pointer-of-pointer
// for FeatureID lets callers express "detach from feature" (outer non-nil,
// inner nil) versus "leave feature unchanged" (outer nil).
type IssueEdit struct {
	Title       *string
	Description *string
	FeatureID   **int64
	FeatureSlug *string // optional: when remote, the slug is sent in the JSON body
	// BaseBranch (BACI-232) — pointer-vs-presence: nil = leave
	// base_branch unchanged; non-nil empty string = clear (back to
	// NULL, inherit from feature); non-nil non-empty = set + validate.
	BaseBranch *string
}

// BriefOptions mirrors the `bacio issue brief` flag set.
type BriefOptions struct {
	NoFeatureDocs bool
	NoComments    bool
	NoDocContent  bool
}

// DocCreateInput is the validated tuple shared by CreateDocument and
// UpsertDocument. Filename and Type are required; SourcePath is set
// when the local CLI imported the doc with --from-path.
type DocCreateInput struct {
	Filename   string
	Type       model.DocumentType
	Body       string
	SourcePath string
}

// AddSessionQuestionInput is the validated tuple AddSessionQuestion
// consumes. SessionID is the external session id (the channel
// resolves the agent session FK from it). IssueKey is the issue key
// of the session's open claim at ask time, when there was one.
// Payload is the validated AskUserQuestion-shaped questions array.
// AskedBy is the persistent agent identity slug — recorded against
// the row's `asked_by` column.
type AddSessionQuestionInput struct {
	SessionID string
	IssueKey  string
	Payload   model.QuestionPayload
	AskedBy   string
}

// AddUserMessageInput is the validated tuple AddUserMessage consumes
// (BACI-286). SessionID is the external session id of the busy worker
// to steer; the store resolves the agent_sessions FK + repo from it.
// Body is the free-form steer note. CreatedBy is the actor that sent it
// (a user at the desktop, typically "user").
type AddUserMessageInput struct {
	SessionID string
	Body      string
	CreatedBy string
}

// AddNotificationInput is the validated tuple AddNotification consumes
// (BACI-287). RepoPrefix names the repo the notification is scoped to.
// SessionID is the external session id of the sending channel session when
// one had registered (optional — resolves the nullable session_pk FK).
// IssueKey is optional; a present key deep-links the row to that ticket.
// Body is the message; SourceAgent is the persistent agent identity that
// sent it (or the "bacio-channel" fallback).
type AddNotificationInput struct {
	RepoPrefix  string
	SessionID   string
	IssueKey    string
	Body        string
	SourceAgent string
}

// NotificationListFilter scopes a ListNotifications query. RepoID nil lists
// cross-repo (the global bell default). UnreadOnly drops read rows. Limit
// caps the result set; <= 0 uses the store's default cap.
type NotificationListFilter struct {
	RepoID     *int64
	UnreadOnly bool
	Limit      int
}
