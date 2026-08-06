package client

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/mrgeoffrich/bacio/internal/git"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// localClient is the in-process backend wrapping a *store.Store. It
// owns the store's lifecycle (Open in newLocalClient, Close on Close).
// Audit-log writes happen here, mirroring what cli handlers used to do
// inline.
type localClient struct {
	store *store.Store
	actor string
}

func newLocalClient(opts Options) (*localClient, error) {
	path := opts.DBPath
	if path == "" {
		p, err := store.DefaultPath()
		if err != nil {
			return nil, err
		}
		path = p
	}
	s, err := store.Open(path)
	if err != nil {
		return nil, err
	}
	return &localClient{store: s, actor: opts.Actor}, nil
}

// NewLocalFromStore wraps an already-open *store.Store as a Client without
// taking ownership of its lifecycle — the caller's Close is a no-op on
// the store. Used by the REST handler layer (which owns the store) when
// it needs to reach a Client method like AutoDispatchIssue that the
// handlers don't reimplement inline. actor is stamped on every audit
// row the wrapped client records (typically the request's X-Actor).
func NewLocalFromStore(s *store.Store, actor string) Client {
	return &borrowedLocalClient{localClient: localClient{store: s, actor: actor}}
}

// borrowedLocalClient embeds localClient but overrides Close so the
// caller's store keeps running after the wrapper is dropped.
type borrowedLocalClient struct {
	localClient
}

func (c *borrowedLocalClient) Close() error { return nil }

func (c *localClient) Mode() string { return ModeLocal }
func (c *localClient) Close() error { return c.store.Close() }

// Store exposes the underlying *store.Store. Used by CLI verbs that
// keep some local-only computation (e.g. bacio status's filesystem-aware
// stats). Callers in remote mode must NOT hit this — the caller is
// responsible for branching on Mode() first.
func (c *localClient) Store() *store.Store { return c.store }

// recordOp writes an audit-log entry. Failures are logged to stderr
// but never fail the user-visible command — losing one history row is
// preferable to rolling back the work the user just asked for.
func (c *localClient) recordOp(e model.HistoryEntry) {
	if e.Actor == "" {
		e.Actor = c.actor
	}
	if e.Actor == "" {
		e.Actor = "unknown"
	}
	if err := c.store.RecordHistory(e); err != nil {
		fmt.Fprintln(os.Stderr, "bacio: warning: failed to record history:", err)
	}
}

// updatedFieldList mirrors internal/cli/audit.go:updatedFieldList. Same
// audit-log Details text on both backends.
func updatedFieldList(fields map[string]bool) string {
	var parts []string
	for name, touched := range fields {
		if touched {
			parts = append(parts, name)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "updated " + joinCSV(parts)
}

func joinCSV(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out += "," + p
	}
	return out
}

// EnsureRepo replicates the CLI's resolveRepo() behaviour: look up by
// path, auto-create on miss, write the repo.create audit row.
func (c *localClient) EnsureRepo(ctx context.Context, info *git.Info) (*model.Repo, bool, error) {
	repo, err := c.store.GetRepoByPath(info.Root)
	if err == nil {
		return repo, false, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return nil, false, err
	}
	// Phantom-upgrade: a phantom row with this remote_url is the
	// matching project for this working tree (sync brought the
	// metadata in earlier). Upgrade rather than create a duplicate.
	// Mirrors the CLI's resolveRepo behaviour for the same reason.
	//
	// IsPhantom() rather than `Path == ""`: a workspace is also pathless,
	// and binding this working tree to one would silently turn a
	// bacio-only container into a git repo. In practice a workspace's
	// remote_url is always '' and the outer guard already skips that
	// case, but the predicate makes the intent explicit rather than
	// leaving the invariant load-bearing three lines away.
	if info.RemoteURL != "" {
		repos, lerr := c.store.ListRepos()
		if lerr == nil {
			for _, r := range repos {
				if r.IsPhantom() && r.RemoteURL == info.RemoteURL {
					if err := c.store.UpgradePhantomRepo(r.UUID, info.Root); err != nil {
						return nil, false, fmt.Errorf("upgrade phantom %s: %w", r.Prefix, err)
					}
					c.recordOp(model.HistoryEntry{
						RepoID: &r.ID, RepoPrefix: r.Prefix,
						Op: "repo.upgrade_phantom", Kind: "repo",
						TargetID: &r.ID, TargetLabel: r.Prefix,
						Details: fmt.Sprintf("path=%s", info.Root),
					})
					upgraded, err := c.store.GetRepoByID(r.ID)
					if err != nil {
						return nil, false, err
					}
					return upgraded, false, nil
				}
			}
		}
	}
	prefix, err := c.store.AllocatePrefix(info.Name)
	if err != nil {
		return nil, false, fmt.Errorf("allocate prefix: %w", err)
	}
	created, err := c.store.CreateRepo(prefix, info.Name, info.Root, info.RemoteURL)
	if err != nil {
		return nil, false, err
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &created.ID, RepoPrefix: created.Prefix,
		Op: "repo.create", Kind: "repo",
		TargetID: &created.ID, TargetLabel: created.Prefix,
		Details: "auto-registered (" + created.Name + ")",
	})
	// Features are mandatory (Pipeline): seed the catch-all features +
	// repo default on first registration. Best-effort — a blip must not
	// fail repo registration. Idempotent.
	if err := c.store.BootstrapRepoDefaults(created.ID); err != nil {
		fmt.Fprintln(os.Stderr, "bacio: warning: bootstrap repo defaults:", err)
	}
	return created, true, nil
}

func (c *localClient) ListHistory(ctx context.Context, repo *model.Repo, f store.HistoryFilter) ([]*model.HistoryEntry, error) {
	if repo != nil {
		f.RepoID = &repo.ID
	}
	rows, err := c.store.ListHistory(f)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []*model.HistoryEntry{}
	}
	return rows, nil
}

func (c *localClient) ProxyStats(ctx context.Context, f store.ProxyStatsFilter) ([]*model.ProxyFQDNStat, error) {
	stats, err := c.store.ProxyStatsByFQDN(f)
	if err != nil {
		return nil, err
	}
	if stats == nil {
		stats = []*model.ProxyFQDNStat{}
	}
	return stats, nil
}

func (c *localClient) AnthropicCapture(ctx context.Context, id int64) (*model.ProxyMessage, error) {
	return c.store.CaptureMessage(id)
}

func (c *localClient) JobTranscript(ctx context.Context, dispatchID int64) (*model.AnthropicTranscript, error) {
	return c.store.JobTranscript(dispatchID)
}

func (c *localClient) ListProxyCaptures(ctx context.Context, f store.ProxyRequestFilter) ([]*model.ProxyCaptureRow, error) {
	rows, err := c.store.ListProxyCapturesEnriched(f)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []*model.ProxyCaptureRow{}
	}
	return rows, nil
}

func (c *localClient) ProxyCaptureRaw(ctx context.Context, id int64) ([]byte, error) {
	pr, err := c.store.GetProxyRequest(id)
	if err != nil {
		return nil, err
	}
	if pr.RawLogPath == "" {
		return nil, store.ErrNotFound
	}
	body, err := os.ReadFile(pr.RawLogPath)
	if err != nil {
		// Pruned / log dir wiped — a clean miss, matching the REST 404.
		return nil, store.ErrNotFound
	}
	return body, nil
}

func (c *localClient) SearchProxyMessages(ctx context.Context, f store.ProxyMessageFilter) ([]*model.ProxyMessageMatch, error) {
	matches, err := c.store.SearchProxyMessages(f)
	if err != nil {
		return nil, err
	}
	if matches == nil {
		matches = []*model.ProxyMessageMatch{}
	}
	return matches, nil
}

func (c *localClient) ListJobTranscripts(ctx context.Context, f store.JobTranscriptFilter) ([]*model.JobTranscriptRow, error) {
	rows, err := c.store.ListJobTranscripts(f)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []*model.JobTranscriptRow{}
	}
	return rows, nil
}
