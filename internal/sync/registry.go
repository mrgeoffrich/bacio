package sync

// BACI-109 lifted the three-pass sync-registry composition out of
// internal/api/handlers_sync.go's handleSyncRegistryList so both the
// HTTP surface (BACI-107) and the TUI Sync tab (BACI-109) call one
// helper. The shape is a pure value type — wire DTOs (snake-case JSON
// tags) stay in the api package, so the HTTP contract is unchanged.
//
// Composition (mirrors the original handler body):
//
//  1. ListSyncRemotes → one Registry.SyncRepos entry per row.
//  2. For each remote, DiscoverMembership → nested Members, accumulating
//     the set of prefixes already accounted for (linked / phantom).
//  3. ListRepos → for each non-phantom repo, exclude prefixes already
//     accounted, exclude those whose .bacio/config.yaml sync.remote is in
//     the registry → unsynced residual.

import (
	"errors"
	"log/slog"
	"time"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// Registry is the value-typed shape returned by BuildRegistry: the
// per-machine sync-repo list with its discovered project members, plus
// the residual of tracked project repos that aren't yet covered by any
// sync remote in the registry.
//
// Both slices are non-nil even when empty so callers that wrap them in
// a JSON encoder emit `[]` rather than `null`.
type Registry struct {
	SyncRepos        []SyncRepoEntry
	UnsyncedProjects []UnsyncedProjectEntry
}

// SyncRepoEntry is one entry in Registry.SyncRepos — a `sync_remotes`
// row enriched with the URL-derived label and the project prefixes the
// sync repo's local clone carries.
//
// Deliberately does NOT carry an in-progress bit. That flag is process-
// local (only meaningful on the leader); the HTTP wire DTO stitches it
// in at handler time, and the TUI does not surface it (the tab can't
// trust the value when another bacio holds the lease — see BACI-109
// plan §"Risks & open questions").
type SyncRepoEntry struct {
	RemoteURL  string
	Label      string
	LocalPath  string
	ClonedAt   time.Time
	LastSyncAt *time.Time
	LastError  *string
	Members    []MemberProjectEntry
}

// MemberProjectEntry is one project entry inside a SyncRepoEntry. Mirrors
// MemberProject's three-status enum but flattens the embedded *model.Repo
// into the leaf string fields renderers actually use.
type MemberProjectEntry struct {
	// Prefix is the project's 4-letter key prefix (always set).
	Prefix string
	// Name is the local repo's display name. Empty for "absent" status
	// (the prefix exists on disk in the sync repo, but the local DB has
	// no row for it) and empty for phantoms that pre-date Name capture.
	Name string
	// UUID is the local repo's UUID, empty for absent entries.
	UUID string
	// Status is one of StatusLinked, StatusPhantom, StatusAbsent.
	Status MembershipStatus
}

// UnsyncedProjectEntry is one entry in Registry.UnsyncedProjects: a
// tracked project repo (non-empty path) that doesn't yet have a
// sync.remote in its .bacio/config.yaml, or whose remote isn't in the
// registry. Phantom rows are excluded — they're already surfaced under
// their sync repo's Members.
//
// Workspaces are included and carry an empty Path: they are pathless by
// construction, cannot drive a sync tick, and are mirrored only when
// some git repo drives one — so "no sync repo I have carries this
// prefix" is exactly what a user needs to be told. See BuildRegistry's
// second pass.
type UnsyncedProjectEntry struct {
	Prefix string
	Name   string
	UUID   string
	Path   string
}

// RegistryStore is the seam BuildRegistry uses to read the rows it
// composes. *store.Store satisfies it via its existing methods;
// declaring the interface here keeps the helper testable with a fake
// without pulling in the SQLite store.
//
// The Members lookup is the same PrefixLookup contract DiscoverMembership
// uses, so a *store.Store satisfies both by virtue of GetRepoByPrefix.
type RegistryStore interface {
	PrefixLookup
	ListSyncRemotes() ([]*model.SyncRemote, error)
	ListRepos() ([]*model.Repo, error)
}

// BuildRegistry composes the sync-repo registry: one entry per
// sync_remotes row with its members discovered on disk, plus the
// residual of tracked project repos that have no sync remote in the
// registry. Pure-function over the store + on-disk sync clones.
//
// Membership-failure tolerance matches the original handler: a single
// remote's DiscoverMembership error is logged through `log` (nil-safe)
// and the entry still appears with empty members so the surface
// continues to render the registry row. A read failure on
// ListSyncRemotes / ListRepos propagates — those are real store errors.
func BuildRegistry(s RegistryStore, log *slog.Logger) (*Registry, error) {
	remotes, err := s.ListSyncRemotes()
	if err != nil {
		return nil, err
	}
	repos, err := s.ListRepos()
	if err != nil {
		return nil, err
	}

	// First pass: assemble the registry list AND remember every prefix
	// the registry already accounts for (linked or phantom under any
	// sync repo). Absent prefixes don't disqualify a tracked repo from
	// unsynced_projects — they signal "this sync repo carries something
	// the local DB has never heard of", not "this prefix is accounted for".
	accountedPrefixes := make(map[string]bool)
	registryURLs := make(map[string]bool, len(remotes))
	syncRepos := make([]SyncRepoEntry, 0, len(remotes))
	for _, rec := range remotes {
		registryURLs[rec.RemoteURL] = true
		members, err := DiscoverMembership(rec.LocalPath, s)
		if err != nil {
			// Membership failure on one row shouldn't sink the whole
			// payload — log it and surface an empty membership list so
			// the UI still renders the registry card.
			if log != nil {
				log.Warn("sync.discover_membership",
					slog.String("remote_url", rec.RemoteURL),
					slog.String("local_path", rec.LocalPath),
					slog.String("err", err.Error()),
				)
			}
			members = nil
		}
		for _, m := range members {
			// StatusWorkspace counts as accounted for the same reason
			// linked and phantom do: the prefix is present in this sync
			// repo's clone, so it is already surfaced under Members and
			// must not be double-reported in the unsynced residual.
			if m.Status == StatusLinked || m.Status == StatusPhantom || m.Status == StatusWorkspace {
				accountedPrefixes[m.Prefix] = true
			}
		}
		syncRepos = append(syncRepos, SyncRepoEntry{
			RemoteURL:  rec.RemoteURL,
			Label:      DeriveSyncLabel(rec.RemoteURL),
			LocalPath:  rec.LocalPath,
			ClonedAt:   rec.ClonedAt,
			LastSyncAt: rec.LastSyncAt,
			LastError:  rec.LastSyncError,
			Members:    membersFor(members),
		})
	}

	// Second pass: the unsynced-projects residual. A repo qualifies iff
	// it has a real working tree AND it isn't already surfaced via the
	// registry AND its .bacio/config.yaml either is missing, empty, or
	// points at a remote the registry doesn't carry.
	//
	// Pathless rows are skipped, and that covers BOTH kinds for
	// different reasons. A phantom is git-kind with the checkout on
	// another machine, and the row only exists because some sync repo
	// already carries the prefix — there is nothing to set up. A
	// workspace has no working tree and therefore no .bacio/config.yaml,
	// so under D4 it can never drive a sync tick of its own; it is
	// mirrored for free by the whole-DB export the moment ANY git repo
	// drives one.
	//
	// This list is a CALL TO ACTION, not an inventory: every row renders
	// as a "set up sync" affordance (SyncSettingsSection ->
	// SyncSetupModal), and SetupSync refuses a pathless repo outright.
	// Listing a workspace would therefore offer a button that cannot
	// work. Whether a workspace is actually mirrored is real and useful
	// information, but its home is the BACI-376 sync badge, which already
	// reports actual mirroring rather than per-repo config.
	//
	// internal/client/sync_status.go's parallel implementation gates on
	// the same predicate; the two must not diverge.
	unsynced := make([]UnsyncedProjectEntry, 0)
	for _, repo := range repos {
		if !repo.HasWorkingTree() {
			continue
		}
		if accountedPrefixes[repo.Prefix] {
			continue
		}
		// A workspace has no path to read a project config from; the
		// registry lookup only makes sense for a real checkout.
		if repo.HasWorkingTree() && isRepoInRegistry(repo.Path, registryURLs, log) {
			continue
		}
		unsynced = append(unsynced, UnsyncedProjectEntry{
			Prefix: repo.Prefix,
			Name:   repo.Name,
			UUID:   repo.UUID,
			Path:   repo.Path,
		})
	}

	return &Registry{
		SyncRepos:        syncRepos,
		UnsyncedProjects: unsynced,
	}, nil
}

// membersFor maps DiscoverMembership output to the registry's leaf
// MemberProjectEntry shape, flattening the embedded *model.Repo into
// the string fields renderers actually use.
func membersFor(members []MemberProject) []MemberProjectEntry {
	out := make([]MemberProjectEntry, 0, len(members))
	for _, m := range members {
		entry := MemberProjectEntry{
			Prefix: m.Prefix,
			Status: m.Status,
		}
		if m.Repo != nil {
			entry.Name = m.Repo.Name
			entry.UUID = m.Repo.UUID
		}
		out = append(out, entry)
	}
	return out
}

// isRepoInRegistry reports whether the project repo at path has a
// .bacio/config.yaml whose sync.remote is in the registry. A missing
// config (ErrNoConfig) returns false. A broken config (any other read
// error) is logged through `log` (nil-safe) and treated as
// not-registered — the user still sees the repo in "Unsynced projects"
// so they can fix it.
func isRepoInRegistry(path string, registryURLs map[string]bool, log *slog.Logger) bool {
	cfg, err := ReadProjectConfig(path)
	if err != nil {
		if !errors.Is(err, ErrNoConfig) && log != nil {
			log.Warn("sync.read_project_config",
				slog.String("path", path),
				slog.String("err", err.Error()),
			)
		}
		return false
	}
	if cfg.Sync.Remote == "" {
		return false
	}
	return registryURLs[cfg.Sync.Remote]
}
