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

	// ----- Features -----
	ListFeatures(ctx context.Context, repo *model.Repo, withDescription bool) ([]*model.Feature, error)
	GetFeatureBySlug(ctx context.Context, repo *model.Repo, slug string) (*model.Feature, error)
	GetFeatureByID(ctx context.Context, repo *model.Repo, id int64) (*model.Feature, error)
	CreateFeature(ctx context.Context, repo *model.Repo, in inputs.FeatureAddInput, dryRun bool) (*model.Feature, error)
	UpdateFeature(ctx context.Context, repo *model.Repo, slug string, title, description *string, dryRun bool) (*model.Feature, error)
	DeleteFeature(ctx context.Context, repo *model.Repo, slug string, dryRun bool) (deletedFeature *model.Feature, preview *FeatureDeletePreview, err error)
	ShowFeature(ctx context.Context, repo *model.Repo, slug string) (*FeatureView, error)
	PlanFeature(ctx context.Context, repo *model.Repo, slug string) (*PlanView, error)

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
	AssignIssue(ctx context.Context, repo *model.Repo, key, assignee string, dryRun bool) (*model.Issue, error)
	UnassignIssue(ctx context.Context, repo *model.Repo, key string, dryRun bool) (*model.Issue, error)
	DeleteIssue(ctx context.Context, repo *model.Repo, key string, dryRun bool) (deletedIssue *model.Issue, preview *IssueDeletePreview, err error)
	PeekNextIssue(ctx context.Context, repo *model.Repo, slug string) (*model.Issue, error)
	ClaimNextIssue(ctx context.Context, repo *model.Repo, slug string, dryRun bool) (*model.Issue, error)

	// ----- Comments / relations / PRs / tags -----
	ListComments(ctx context.Context, repo *model.Repo, key string) ([]*model.Comment, error)
	AddComment(ctx context.Context, repo *model.Repo, in inputs.CommentAddInput, dryRun bool) (*model.Comment, error)

	LinkRelation(ctx context.Context, repo *model.Repo, in inputs.LinkInput, dryRun bool) (*model.Relation, error)
	UnlinkRelation(ctx context.Context, repo *model.Repo, in inputs.UnlinkInput, dryRun bool) (preview *RelationDeletePreview, removed int64, err error)

	ListPRs(ctx context.Context, repo *model.Repo, key string) ([]*model.PullRequest, error)
	AttachPR(ctx context.Context, repo *model.Repo, key, url string, dryRun bool) (*model.PullRequest, error)
	DetachPR(ctx context.Context, repo *model.Repo, key, url string, dryRun bool) (preview *PRDetachPreview, removed int64, err error)

	AddTags(ctx context.Context, repo *model.Repo, key string, tags []string, dryRun bool) (*model.Issue, error)
	RemoveTags(ctx context.Context, repo *model.Repo, key string, tags []string, dryRun bool) (*model.Issue, error)

	// ----- Documents -----
	ListDocuments(ctx context.Context, repo *model.Repo, typeStr string) ([]*model.Document, error)
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
	// ListOpenClaims returns every open (unreleased) agent claim for repo,
	// or across all repos when repo is nil. Local-only; remote returns
	// ErrLocalOnly. Used by the desktop Board to derive each card's `taken`.
	ListOpenClaims(ctx context.Context, repo *model.Repo) ([]*model.AgentClaim, error)
	// UpsertSessionTodoFromTask records one TaskCreate (insert) or
	// TaskUpdate (update by task_id) event from the PostToolUse hook.
	// Local-only — the agent registry has no HTTP write surface in v1.
	UpsertSessionTodoFromTask(ctx context.Context, sessionID, taskID, content string, status model.TodoStatus) error
	// ListSessionTodos returns the latest snapshot for one session,
	// position-ordered. Empty slice for an unknown / empty session.
	// Local-only.
	ListSessionTodos(ctx context.Context, sessionID string) ([]model.SessionTodo, error)
	// ListTodosBySessions returns a session_pk → []SessionTodo map for
	// the given session ids in one query — used by the desktop and TUI
	// agent views to hydrate todos for every live session in one trip,
	// like ListOpenClaims does for claims. Local-only.
	ListTodosBySessions(ctx context.Context, sessionIDs []string) (map[int64][]model.SessionTodo, error)
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
	// error; cancelling an already-cancelled one is a no-op. If the
	// dispatch targets an issue, its waiting_for_claim flag is cleared
	// in the same transaction.
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

	// ----- Prompt state-gates (local-only; `bacio settings template states`) -----
	// GetPromptStates is the legacy lookup shape for the dispatch
	// gate-check paths that still expect a slug→[]state map. Local-only.
	//
	// Deprecated: prefer ListPromptTemplates for new code.
	GetPromptStates(ctx context.Context) (map[string][]string, error)
	// SetPromptStates stores a custom state-gate for one slug; an empty
	// slice reverts a built-in slug to its embedded default gate.
	// Local-only.
	//
	// Deprecated: prefer AddPromptTemplate / RenamePromptTemplate /
	// DeletePromptTemplate verbs for new agent-facing flows.
	SetPromptStates(ctx context.Context, mode string, states []string, dryRun bool) error

	// ----- Board preferences (local-only; desktop Settings panel) -----
	// GetBoardPreferences returns the desktop Board's UI preferences
	// (custom values, or the built-in defaults). SetBoardPreferences
	// stores them; with dryRun set it writes nothing. Local-only — the
	// remote backend returns ErrLocalOnly.
	GetBoardPreferences(ctx context.Context) (BoardPreferences, error)
	SetBoardPreferences(ctx context.Context, prefs BoardPreferences, dryRun bool) error
}

// BoardPreferences holds the desktop Board's UI preferences, persisted
// in the global app_settings KV. HideEmptyColumns drops kanban columns
// with zero cards from the Board. Local-only — there's no remote
// analogue in v1, like the rest of the app_settings store.
type BoardPreferences struct {
	HideEmptyColumns bool `json:"hideEmptyColumns"`
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
}

// IssueEdit is the parameter bundle for UpdateIssue. Pointer-of-pointer
// for FeatureID lets callers express "detach from feature" (outer non-nil,
// inner nil) versus "leave feature unchanged" (outer nil).
type IssueEdit struct {
	Title       *string
	Description *string
	FeatureID   **int64
	FeatureSlug *string // optional: when remote, the slug is sent in the JSON body
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
