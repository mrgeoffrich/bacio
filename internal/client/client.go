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
	// EnsureAgentIdentity mints a fresh persistent agent identity (a
	// random slug, retried against the UNIQUE constraint until it
	// sticks) and adopts it as this client's audit actor. It's the
	// `bacio hook` session-start path for a repo that has no
	// .bacio/agent yet — the caller persists the returned slug to disk.
	// Does NOT create a session row. Local-only.
	EnsureAgentIdentity(ctx context.Context, repo *model.Repo) (string, error)
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
	// DrainDispatches returns a session's pending dispatches and marks
	// each one delivered — the pull-delivery path used by the bacio
	// hooks (which know their session id from the hook payload).
	// Delivered dispatches aren't re-drained but stay in the inbox
	// until acked.
	DrainDispatches(ctx context.Context, sessionID string) ([]*model.AgentDispatch, error)
	// DrainAgentDispatches is the same drain, scoped to a repo + agent
	// identity rather than a session id — the push-delivery path used
	// by `bacio channel`, which (unlike a hook) is never told its
	// session id. A nil repo or empty agent name drains nothing (the
	// channel runs idle rather than erroring).
	DrainAgentDispatches(ctx context.Context, repo *model.Repo, agentName string) ([]*model.AgentDispatch, error)
	// RepoDispatches returns every dispatch scoped to one repo, newest
	// first, regardless of status — the read surface the desktop Agents
	// screen needs. Local-only in v1.
	RepoDispatches(ctx context.Context, repo *model.Repo) ([]*model.AgentDispatch, error)
}

// AgentSessionFilter mirrors store.AgentSessionFilter; the wrapper lets
// CLI callers pass a *model.Repo (so the remote backend can resolve
// prefix when v2 wires it up) instead of a raw repo id.
type AgentSessionFilter struct {
	Repo      *model.Repo // nil = all repos
	OnlyAlive bool
	Since     time.Time
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
