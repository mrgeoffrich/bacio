package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/git"
	"github.com/mrgeoffrich/bacio/internal/logging"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
	"github.com/mrgeoffrich/bacio/internal/sync"
	"github.com/mrgeoffrich/bacio/internal/wtenv"
)

// errSyncRepoMode signals that the current working tree is the root
// of a bacio sync repo (a bacio-sync.yaml lives there). Tracking commands
// (bacio issue create, bacio feature edit, …) should refuse and surface this
// error verbatim so the user gets a clear message and AI agents can
// branch on the sentinel. Sync-admin commands (bacio sync verify / inspect,
// landing in Phase 5) can detect this error and switch to sync-repo
// mode instead.
var errSyncRepoMode = errors.New("this is a bacio sync repo (bacio-sync.yaml at root); cd to your project repo to track issues")

// auto-register a repo on first use. Each call site records its own
// history once we get a repo back.

// openStore opens the configured database.
func openStore() (*store.Store, error) {
	res, err := resolveEnv()
	if err != nil {
		return nil, err
	}
	return store.Open(res.DBPath)
}

// resolveEnv runs the wtenv resolver with the current globals applied.
// Used by openStore, openClient, and `bacio api`'s default-addr logic
// so every entry point honours the same precedence chain.
func resolveEnv() (wtenv.Resolved, error) {
	envLookup := os.Getenv
	if opts.envPath != "" {
		envLookup = func(k string) string {
			if k == wtenv.EnvVar {
				return opts.envPath
			}
			return os.Getenv(k)
		}
	}
	return wtenv.Resolve(wtenv.ResolveOpts{
		FlagDB:    opts.dbPath,
		EnvLookup: envLookup,
	})
}

// resolveLogging walks the BACI-73 log-destination chain (flag → env →
// worktree manifest → default fallback) on top of the wtenv resolver
// result. The wtenv layer already tells us whether a manifest fed
// resolution and, when it did, both the manifest's directory and its
// optional allocations.log_dir field — the two values logging.Resolve
// needs to synthesise the per-worktree default
// (`<worktree-root>/.bacio/logs/`) or honour an explicit override.
//
// Callers that haven't run the wtenv resolver yet should call this via
// resolveLoggingFromEnv below, which handles the call internally.
func resolveLogging(env wtenv.Resolved) (logging.Resolved, error) {
	opt := logging.ResolveOpts{
		FlagDir:   opts.logDir,
		FlagLevel: opts.logLevel,
	}
	if env.ManifestPath != "" {
		opt.ManifestDir = filepath.Dir(env.ManifestPath)
		if env.Manifest != nil {
			opt.ManifestLogDir = env.Manifest.Allocations.LogDir
		}
	}
	return logging.Resolve(opt)
}

// resolveLoggingFromEnv is the convenience wrapper used by command
// handlers that haven't already resolved wtenv. Calls resolveEnv
// internally and then defers to resolveLogging.
func resolveLoggingFromEnv() (logging.Resolved, error) {
	env, err := resolveEnv()
	if err != nil {
		return logging.Resolved{}, err
	}
	return resolveLogging(env)
}

// remoteURL returns the configured remote URL, falling back to
// BACIO_REMOTE so callers can switch backends per-shell without retyping
// the flag on every command.
func remoteURL() string {
	if opts.remote != "" {
		return opts.remote
	}
	return os.Getenv("BACIO_REMOTE")
}

// apiToken returns the bearer token for the remote API, falling back
// to BACIO_API_TOKEN.
func apiToken() string {
	if opts.token != "" {
		return opts.token
	}
	return os.Getenv("BACIO_API_TOKEN")
}

// openClient wires up the right backend (local SQLite or remote HTTP).
// Replaces openStore() at every CLI handler call site as Phase 6
// migrates them. Defer c.Close() in the same way you would the store.
func openClient() (client.Client, error) {
	dbPath := opts.dbPath
	if dbPath == "" {
		res, err := resolveEnv()
		if err != nil {
			return nil, err
		}
		dbPath = res.DBPath
	}
	return client.Open(context.Background(), client.Options{
		DBPath: dbPath,
		Remote: remoteURL(),
		Token:  apiToken(),
		Actor:  actor(),
	})
}

// inRemoteMode reports whether the CLI is configured to talk to a
// remote `bacio api` server. Used by local-only verbs to short-circuit
// with a clear error.
func inRemoteMode() bool { return remoteURL() != "" }

// resolveSyncRepoRoot reports whether the current working directory
// sits inside a bacio sync repo (i.e. the git working-tree root carries
// bacio-sync.yaml). Returns the absolute root + true when so; ("", false)
// otherwise. Unlike requireSyncRepoMode (which errors), this is a quiet
// probe used by read-only list commands to pick a branch.
func resolveSyncRepoRoot() (string, bool) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", false
	}
	info, err := git.Detect(cwd)
	if err != nil {
		return "", false
	}
	if !sync.IsSyncRepo(info.Root) {
		return "", false
	}
	return info.Root, true
}

// repoSelector returns the explicit repo/workspace prefix the caller
// picked: the global `--repo` flag, falling back to $BACIO_REPO. Empty
// means "resolve from the current working tree", which is the historical
// behaviour and stays the default.
//
// This exists because a WORKSPACE (repos.kind = 'workspace') has no path
// on disk — git.Detect can never find one, so without an explicit
// selector there is no way to drive a workspace from the CLI at all.
// Uppercased here so `--repo mini` and `--repo MINI` behave the same;
// prefixes are stored uppercase.
func repoSelector() string {
	prefix := strings.TrimSpace(opts.repoPrefix)
	if prefix == "" {
		prefix = strings.TrimSpace(os.Getenv("BACIO_REPO"))
	}
	return strings.ToUpper(prefix)
}

// resolveRepo finds the repo row for the current working directory, creating
// it on first use. Errors out if not inside a git repo. Used by handlers
// that haven't migrated to the client yet; new code should prefer
// resolveRepoC.
func resolveRepo(s *store.Store) (*model.Repo, error) {
	// The explicit selector short-circuits cwd detection entirely —
	// including the sync-repo refusal and the auto-register path below.
	// An explicitly named prefix must already exist: `--repo` is a
	// lookup, never a create. Kept in lockstep with resolveRepoC so the
	// two twins can't drift; `bacio tui` is the only caller today, and
	// its own --repo flag shadows the global one, so in practice this
	// branch fires for $BACIO_REPO.
	if prefix := repoSelector(); prefix != "" {
		return s.GetRepoByPrefix(prefix)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	info, err := git.Detect(cwd)
	if err != nil {
		return nil, err
	}
	// Refuse to auto-register a sync repo as a project. The sentinel
	// (bacio-sync.yaml at the working-tree root) is the canonical signal.
	// Sync-admin commands handle this error explicitly; everything
	// else lets it bubble up to the user.
	if sync.IsSyncRepo(info.Root) {
		return nil, errSyncRepoMode
	}
	repo, err := s.GetRepoByPath(info.Root)
	if err == nil {
		return repo, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	// Before allocating a fresh prefix, see if this working tree is
	// the matching project for a phantom repo (a row with path = '',
	// imported from sync). Match by remote_url — the only natural
	// identifier shared between project repo and sync-repo
	// repo.yaml. If found, upgrade in place rather than creating a
	// duplicate row.
	if phantom, ok := matchPhantomByRemote(s, info.RemoteURL); ok {
		if err := s.UpgradePhantomRepo(phantom.UUID, info.Root); err != nil {
			return nil, fmt.Errorf("upgrade phantom %s: %w", phantom.Prefix, err)
		}
		recordOp(s, model.HistoryEntry{
			RepoID: &phantom.ID, RepoPrefix: phantom.Prefix,
			Op: "repo.upgrade_phantom", Kind: "repo",
			TargetID: &phantom.ID, TargetLabel: phantom.Prefix,
			Details: fmt.Sprintf("path=%s", info.Root),
		})
		return s.GetRepoByID(phantom.ID)
	}
	prefix, err := s.AllocatePrefix(info.Name)
	if err != nil {
		return nil, fmt.Errorf("allocate prefix: %w", err)
	}
	created, err := s.CreateRepo(prefix, info.Name, info.Root, info.RemoteURL)
	if err != nil {
		return nil, err
	}
	recordOp(s, model.HistoryEntry{
		RepoID: &created.ID, RepoPrefix: created.Prefix,
		Op: "repo.create", Kind: "repo",
		TargetID: &created.ID, TargetLabel: created.Prefix,
		Details: "auto-registered (" + created.Name + ")",
	})
	// Features are mandatory (Pipeline): seed the catch-all features +
	// repo default on first registration so issues created in this repo
	// auto-assign a feature. Best-effort — a blip must not fail repo
	// registration; the repo still works, just without a default until a
	// later bootstrap. Idempotent.
	if err := s.BootstrapRepoDefaults(created.ID); err != nil {
		fmt.Fprintln(os.Stderr, "bacio: warning: bootstrap repo defaults:", err)
	}
	return created, nil
}

// launchRepoPrefix resolves the repo the process was started in, so
// `bacio web` / `bacio api` can tell the UI which repo to open on
// (BACI-368). Registers the repo on first sight, exactly like every
// other bacio entry point.
//
// Never returns an error: a launch-repo hiccup must not stop a server
// booting, and "not started inside a git repo" is an ordinary outcome.
// Both cases yield "" and the UI falls back to its remembered pick.
func launchRepoPrefix(s *store.Store, logger *slog.Logger) string {
	cwd, err := os.Getwd()
	if err != nil {
		logger.Warn("launch repo: cannot resolve cwd", "err", err)
		return ""
	}
	repo, err := client.EnsureRepoForDir(context.Background(), client.NewLocalFromStore(s, actor()), cwd)
	if err != nil {
		logger.Warn("launch repo: resolve failed", "dir", cwd, "err", err)
		return ""
	}
	if repo == nil {
		return ""
	}
	return repo.Prefix
}

// matchPhantomByRemote looks for a phantom repo (path = '') whose
// remote_url matches the supplied URL. Returns the matched repo and
// true on hit, otherwise (nil, false). Empty remote URLs never match
// — pairing two repos that just happen to be both remoteless would
// be a worse failure mode than auto-registering a fresh prefix.
//
// Lives next to resolveRepo because it's specific to the
// phantom-upgrade flow at the project<->sync seam; promoting it to
// the store layer is fine if more callers ever need it.
func matchPhantomByRemote(s *store.Store, remoteURL string) (*model.Repo, bool) {
	if remoteURL == "" {
		return nil, false
	}
	repos, err := s.ListRepos()
	if err != nil {
		return nil, false
	}
	for _, r := range repos {
		// IsPhantom(), not `Path == ""`: a workspace is also pathless but
		// is NOT a phantom waiting to be paired with a checkout. In
		// practice a workspace's remote_url is always '' and the empty
		// guard above already filtered that, but state the intent
		// explicitly so the invariant survives a future change.
		if r.IsPhantom() && r.RemoteURL == remoteURL {
			return r, true
		}
	}
	return nil, false
}

// resolveRepoC resolves the repo for CWD via the Client abstraction so
// the same auto-register behaviour works in both local and remote
// modes. Local backend writes the audit row inline; remote backend
// triggers the server's POST /repos which writes the row server-side.
func resolveRepoC(c client.Client) (*model.Repo, error) {
	// --repo / $BACIO_REPO wins over everything, and is consumed BEFORE
	// git.Detect: a workspace has no working tree, so cwd detection
	// would either fail outright or — worse — auto-register whatever
	// unrelated git repo the user happens to be standing in. When the
	// selector is absent this function behaves exactly as it always has,
	// auto-registration included.
	if prefix := repoSelector(); prefix != "" {
		return c.GetRepoByPrefix(context.Background(), prefix)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	info, err := git.Detect(cwd)
	if err != nil {
		return nil, err
	}
	// Refuse to auto-register a sync repo as a project. Mirrors
	// resolveRepo's check; protects every command that goes through
	// the client abstraction (bacio issue, bacio feature, bacio doc, …).
	if sync.IsSyncRepo(info.Root) {
		return nil, errSyncRepoMode
	}
	repo, _, err := c.EnsureRepo(context.Background(), info)
	return repo, err
}
