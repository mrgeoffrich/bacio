package sync

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// Membership discovery for the sync-repo registry (BACI-105). Given a
// sync repo's local clone on disk, list the project prefixes it carries
// and cross-reference each one against the local DB. The result feeds
// every surface that shows "what's in this sync repo" — `bacio sync
// remotes` CLI (BACI-106), the desktop / web Sync view (BACI-108), the
// TUI sync surface (BACI-109).
//
// Filesystem-only: walks the sync repo's `repos/<prefix>/` folders with
// a cheap os.ReadDir and looks up each prefix in the local DB via the
// supplied PrefixLookup. NEVER opens a per-prefix project DB — that
// would require touching the user's working trees, which membership
// discovery shouldn't do.

// MembershipStatus is the per-project resolution outcome.
type MembershipStatus string

const (
	// StatusLinked: the local DB has a real row for this prefix
	// (path != ""). The user works in this project locally.
	StatusLinked MembershipStatus = "linked"
	// StatusPhantom: the local DB has a row but it's a phantom
	// (path == ""), inserted by `bacio sync clone` for a prefix
	// the user hasn't linked to a working tree yet.
	StatusPhantom MembershipStatus = "phantom"
	// StatusAbsent: the sync repo carries a prefix the local DB
	// has never seen — neither real nor phantom. Treated as "you
	// could clone this project if you wanted to".
	StatusAbsent MembershipStatus = "absent"
)

// MemberProject is one entry in the discovered membership list. For
// Absent entries Repo is nil; for Linked / Phantom it points at the
// matched row.
type MemberProject struct {
	Prefix string           `json:"prefix"`
	Status MembershipStatus `json:"status"`
	Repo   *model.Repo      `json:"repo,omitempty"`
}

// PrefixLookup is the seam DiscoverMembership uses to resolve a prefix
// to a local DB row. *store.Store satisfies it via its existing
// GetRepoByPrefix; the interface is here so a future remote-mode shim
// can supply a different lookup without changing the discovery logic.
//
// Implementations MUST return store.ErrNotFound for a missing prefix
// (matches the existing GetRepoByPrefix contract).
type PrefixLookup interface {
	GetRepoByPrefix(prefix string) (*model.Repo, error)
}

// DiscoverMembership walks `<syncRepoRoot>/repos/` for top-level
// prefix folders and cross-references each against the supplied
// lookup. Returns one MemberProject per on-disk prefix, sorted by
// Prefix ASC.
//
// Tolerates a missing/unreadable clone (empty syncRepoRoot, missing
// repos/ subdir) by returning an empty slice + nil error — matches the
// feature brief's "report the registry row, empty membership" stance.
// Real DB errors (anything other than ErrNotFound from the lookup, or
// a Read failure on the repos/ dir itself) propagate so a UI doesn't
// silently show a misleading "empty" list.
//
// Non-directory entries under repos/ are ignored (no error, no entry);
// the sync-repo layout only writes directories at that level, but a
// stray dotfile or README shouldn't break discovery.
func DiscoverMembership(syncRepoRoot string, lookup PrefixLookup) ([]MemberProject, error) {
	if lookup == nil {
		return nil, fmt.Errorf("DiscoverMembership: lookup is nil")
	}
	if syncRepoRoot == "" {
		return []MemberProject{}, nil
	}
	reposDir := filepath.Join(syncRepoRoot, "repos")
	entries, err := os.ReadDir(reposDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []MemberProject{}, nil
		}
		return nil, fmt.Errorf("read sync repos dir: %w", err)
	}
	out := make([]MemberProject, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		prefix := entry.Name()
		repo, err := lookup.GetRepoByPrefix(prefix)
		switch {
		case errors.Is(err, store.ErrNotFound):
			out = append(out, MemberProject{Prefix: prefix, Status: StatusAbsent})
		case err != nil:
			return nil, fmt.Errorf("lookup prefix %s: %w", prefix, err)
		case repo.Path == "":
			out = append(out, MemberProject{Prefix: prefix, Status: StatusPhantom, Repo: repo})
		default:
			out = append(out, MemberProject{Prefix: prefix, Status: StatusLinked, Repo: repo})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Prefix < out[j].Prefix })
	return out, nil
}
