package sync

// Mirror coverage (BACI-376). "Is this repo's data actually being
// mirrored?" is a different question from "does this repo have a
// sync.remote in its own .bacio/config.yaml", and the two answers
// diverge routinely.
//
// The export is whole-DB: Engine.Export walks store.ListRepos() with no
// filter and writes every tracked repo into repos/<prefix>/ of whichever
// sync repo the tick is running against. So a project that has never
// seen `bacio sync init` still has its issues mirrored the moment any
// *other* project on this machine drives a tick. The Sync settings
// screen already reflects that — such a repo shows up as a `linked`
// member of the sync repo, not under "Unsynced projects" — while the
// per-repo sync status reported only the config-file view and called it
// unconfigured. That contradiction is what BACI-376 was filed for.
//
// MirrorCoverage answers the on-disk question the same way membership
// discovery does: which prefixes does each sync repo's local clone
// actually carry. Filesystem-only, one os.ReadDir per registered sync
// remote — cheap enough for the status endpoints' 10s poll, and
// consistent by construction with the registry surface.

import (
	"os"
	"path/filepath"
	"time"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// MirrorSource is the sync repo whose local clone carries a project's
// exported data, plus that clone's last-run state — which is the
// meaningful "when was I last mirrored" timestamp for a repo that has
// no sync_remotes row of its own.
type MirrorSource struct {
	RemoteURL  string
	Label      string
	LastSyncAt *time.Time
	LastError  *string
}

// CoverageStore is the seam MirrorCoverage reads through. *store.Store
// satisfies it via its existing ListSyncRemotes.
type CoverageStore interface {
	ListSyncRemotes() ([]*model.SyncRemote, error)
}

// MirrorCoverage maps every project prefix carried by a sync repo on
// this machine to the sync repo carrying it. A prefix carried by more
// than one sync repo resolves to the first in registry order — the
// status surfaces name one mirror, and listing several would be noise
// for a configuration nobody runs today.
//
// An unreadable clone (never cloned, deleted underneath us) contributes
// nothing rather than erroring: a missing mirror is "not covered", which
// is exactly what the caller should report. Only a real ListSyncRemotes
// failure propagates.
func MirrorCoverage(s CoverageStore) (map[string]MirrorSource, error) {
	remotes, err := s.ListSyncRemotes()
	if err != nil {
		return nil, err
	}
	out := map[string]MirrorSource{}
	for _, rec := range remotes {
		src := MirrorSource{
			RemoteURL:  rec.RemoteURL,
			Label:      DeriveSyncLabel(rec.RemoteURL),
			LastSyncAt: rec.LastSyncAt,
			LastError:  rec.LastSyncError,
		}
		for _, prefix := range prefixesOnDisk(rec.LocalPath) {
			if _, seen := out[prefix]; !seen {
				out[prefix] = src
			}
		}
	}
	return out, nil
}

// prefixesOnDisk lists the repos/<prefix>/ folder names in a sync repo's
// local clone. Same walk DiscoverMembership does, without the per-prefix
// DB classification — callers here already hold the repo row.
func prefixesOnDisk(syncRepoRoot string) []string {
	if syncRepoRoot == "" {
		return nil
	}
	entries, err := os.ReadDir(filepath.Join(syncRepoRoot, "repos"))
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			out = append(out, entry.Name())
		}
	}
	return out
}
