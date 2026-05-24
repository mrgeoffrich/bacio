package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/mrgeoffrich/bacio/internal/sync"
)

// `bacio sync remotes` — read-only listing of the per-machine sync-repo
// registry (BACI-106). Mirrors the other read-only sync verbs
// (`inspect`, `verify`): no `--json` / `--dry-run` / `bacio schema`
// entry, every call early-returns under `--remote` like its peers.
//
// Each row is a `sync_remotes` table row enriched with three derivations
// shared with the desktop / web / TUI sync surfaces:
//
//   - `label`           — derived from the URL via sync.DeriveSyncLabel.
//   - `clone_present`   — does row.LocalPath still exist on disk?
//   - `projects`        — sync.DiscoverMembership of LocalPath against the
//     store, classifying each on-disk prefix as linked / phantom / absent.
//
// Order: rows come back in the deterministic `remote_url ASC` order
// that Store.ListSyncRemotes pins; per-row `projects` come back in the
// `prefix ASC` order DiscoverMembership pins. Both orderings are part of
// the contract — `jq` pipelines on top of `-o json` rely on them.

// syncRemoteProject is one entry in the per-row projects slice. Mirrors
// sync.MemberProject's shape but flattens the embedded *model.Repo to a
// single `repo_uuid` so the JSON payload stays small — callers that want
// the full repo can `bacio repo show <prefix>` from the prefix.
type syncRemoteProject struct {
	Prefix   string `json:"prefix"`
	Status   string `json:"status"`
	RepoUUID string `json:"repo_uuid,omitempty"`
}

// syncRemoteEntry is one row of the registry plus its derivations. The
// pointer fields (`last_sync_at`, `last_sync_error`) are omitted from
// the JSON payload when nil so consumers don't have to special-case the
// `null` form.
type syncRemoteEntry struct {
	RemoteURL     string              `json:"remote_url"`
	Label         string              `json:"label"`
	LocalPath     string              `json:"local_path"`
	ClonedAt      time.Time           `json:"cloned_at"`
	LastSyncAt    *time.Time          `json:"last_sync_at,omitempty"`
	LastSyncError *string             `json:"last_sync_error,omitempty"`
	ClonePresent  bool                `json:"clone_present"`
	Projects      []syncRemoteProject `json:"projects"`
}

// syncRemotesResult is the wrapper the JSON encoder / text renderer
// dispatch on. The named `remotes` field (rather than a top-level array)
// keeps the shape forward-compatible — future additions like a
// background-sync echo can land alongside without breaking consumers.
type syncRemotesResult struct {
	Remotes []syncRemoteEntry `json:"remotes"`
}

func newSyncRemotesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remotes",
		Short: "List the per-machine sync-repo registry",
		Long: `Lists every sync-repo this bacio has cloned (the sync_remotes
table), each with its derived label, local clone path, last-sync
status, and the projects the on-disk clone carries (cross-referenced
against the local DB as linked / phantom / absent).

Read-only — no --json / --dry-run / schema entry; mirrors
'bacio sync inspect' and 'bacio sync verify'. Runs from anywhere;
the registry is per-machine, not per-cwd.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if inRemoteMode() {
				return fmt.Errorf("bacio sync remotes: not supported in remote mode (operates on the local DB only)")
			}
			s, err := openStore()
			if err != nil {
				return err
			}
			defer s.Close()

			rows, err := s.ListSyncRemotes()
			if err != nil {
				return err
			}

			entries := make([]syncRemoteEntry, 0, len(rows))
			for _, row := range rows {
				entry := syncRemoteEntry{
					RemoteURL:     row.RemoteURL,
					Label:         sync.DeriveSyncLabel(row.RemoteURL),
					LocalPath:     row.LocalPath,
					ClonedAt:      row.ClonedAt,
					LastSyncAt:    row.LastSyncAt,
					LastSyncError: row.LastSyncError,
					Projects:      []syncRemoteProject{},
				}
				// Stat the clone path so the consumer can tell "carries
				// no projects" from "clone is gone". DiscoverMembership
				// already tolerates a missing dir by returning [] + nil,
				// but the explicit boolean is what the renderer uses to
				// pick between "projects: 0" and "(missing)".
				if _, statErr := os.Stat(row.LocalPath); statErr == nil {
					entry.ClonePresent = true
					members, err := sync.DiscoverMembership(row.LocalPath, s)
					if err != nil {
						return fmt.Errorf("discover membership for %s: %w", row.RemoteURL, err)
					}
					for _, m := range members {
						p := syncRemoteProject{Prefix: m.Prefix, Status: string(m.Status)}
						if m.Repo != nil {
							p.RepoUUID = m.Repo.UUID
						}
						entry.Projects = append(entry.Projects, p)
					}
				}
				entries = append(entries, entry)
			}

			return emit(syncRemotesResult{Remotes: entries})
		},
	}
}
