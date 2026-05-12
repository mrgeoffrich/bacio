package cli

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/git"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
	"github.com/mrgeoffrich/bacio/internal/sync"
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
	path := opts.dbPath
	if path == "" {
		p, err := store.DefaultPath()
		if err != nil {
			return nil, err
		}
		path = p
	}
	return store.Open(path)
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
	return client.Open(context.Background(), client.Options{
		DBPath: opts.dbPath,
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

// resolveRepo finds the repo row for the current working directory, creating
// it on first use. Errors out if not inside a git repo. Used by handlers
// that haven't migrated to the client yet; new code should prefer
// resolveRepoC.
func resolveRepo(s *store.Store) (*model.Repo, error) {
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
	return created, nil
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
		if r.Path == "" && r.RemoteURL == remoteURL {
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
