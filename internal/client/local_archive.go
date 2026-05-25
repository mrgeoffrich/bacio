// BACI-68: client-side wrappers for the archive sweep and the
// display.show_archived global toggle. Both routes are local-only
// (mechanical janitor work, and a global preference; the remote
// counterpart returns ErrLocalOnly in remote_archive.go).
package client

import (
	"context"
	"fmt"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// ArchiveSweep runs the BACI-68 archive sweep on demand — the same
// three SQL passes the leader-elected Controller runs. The dry-run
// path runs the same UPDATEs inside a transaction and rolls back, so
// the returned counts preview exactly what a real sweep would have
// archived without modifying any rows or emitting an audit entry.
// On a wet run, records an `archive.sweep` audit row with the
// per-pass counts when at least one row was archived (a no-op sweep
// produces no audit noise).
func (c *localClient) ArchiveSweep(ctx context.Context, dryRun bool) (store.ArchiveSweepResult, error) {
	res, err := c.store.ArchiveSweep(dryRun)
	if err != nil {
		return store.ArchiveSweepResult{}, err
	}
	if !dryRun && res.Total() > 0 {
		c.recordOp(model.HistoryEntry{
			Op:      "archive.sweep",
			Kind:    "sweep",
			Details: detailsForSweep(res),
		})
	}
	return res, nil
}

func detailsForSweep(r store.ArchiveSweepResult) string {
	// Compact, parseable details string — readable in `bacio history`
	// without needing the JSON formatter.
	return fmt.Sprintf(`{"issues":%d,"features":%d,"documents":%d}`,
		r.IssuesArchived, r.FeaturesArchived, r.DocumentsArchived)
}

// GetDisplayShowArchived reads the BACI-68 global toggle.
func (c *localClient) GetDisplayShowArchived(ctx context.Context) (bool, error) {
	return c.store.GetDisplayShowArchived()
}

// SetDisplayShowArchived writes the BACI-68 toggle and records an
// audit row. Returns the post-write value (or the projected value on
// dry-run).
func (c *localClient) SetDisplayShowArchived(ctx context.Context, value, dryRun bool) (bool, error) {
	if dryRun {
		return value, nil
	}
	if err := c.store.SetDisplayShowArchived(value); err != nil {
		return false, err
	}
	c.recordOp(model.HistoryEntry{
		// kind/details shape matches handlers_archive.go's
		// handleDisplayPreferencesSet so `bacio history --kind app_setting`
		// returns the same rows whether the toggle was flipped from the CLI
		// or the HTTP surface (nit #8 on PR #103).
		Op:          "display.update",
		Kind:        "app_setting",
		TargetLabel: "display.show_archived",
		Details:     fmt.Sprintf("show_archived=%t", value),
	})
	return value, nil
}

// GetArchivePreferences reads the BACI-162 auto-archive settings pair.
func (c *localClient) GetArchivePreferences(ctx context.Context) (ArchivePreferences, error) {
	autoEnabled, err := c.store.GetArchiveAutoEnabled()
	if err != nil {
		return ArchivePreferences{}, err
	}
	retention, err := c.store.GetArchiveRetentionDays()
	if err != nil {
		return ArchivePreferences{}, err
	}
	return ArchivePreferences{AutoEnabled: autoEnabled, RetentionDays: retention}, nil
}

// SetArchivePreferences writes both BACI-162 keys atomically and
// records an audit row. The retention_days validator runs at the
// client boundary so the HTTP / desktop callers all see the same
// rejection. Returns the projected pair on dry-run; the post-write
// pair otherwise.
func (c *localClient) SetArchivePreferences(ctx context.Context, in ArchivePreferences, dryRun bool) (ArchivePreferences, error) {
	retention, err := store.ValidateArchiveRetentionDays(in.RetentionDays)
	if err != nil {
		return ArchivePreferences{}, err
	}
	out := ArchivePreferences{AutoEnabled: in.AutoEnabled, RetentionDays: retention}
	if dryRun {
		return out, nil
	}
	if err := c.store.SetArchiveAutoEnabled(in.AutoEnabled); err != nil {
		return ArchivePreferences{}, err
	}
	if err := c.store.SetArchiveRetentionDays(retention); err != nil {
		return ArchivePreferences{}, err
	}
	c.recordOp(model.HistoryEntry{
		// kind / target / details match the CLI's newSettingsArchiveCmd
		// audit row so `bacio history --kind app_setting` returns the
		// same rows whether the pair was changed from the CLI, HTTP,
		// or the desktop.
		Op:          "archive.update",
		Kind:        "app_setting",
		TargetLabel: "archive.preferences",
		Details:     fmt.Sprintf("auto_enabled=%t retention_days=%d", in.AutoEnabled, retention),
	})
	return out, nil
}
