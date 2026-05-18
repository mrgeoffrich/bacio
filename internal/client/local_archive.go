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
// three SQL passes the leader-elected Controller runs hourly. The
// dry-run path returns a zeroed result without writing.
// Records an `archive.sweep` audit row with the per-pass counts when
// at least one row was archived (a no-op sweep produces no audit
// noise).
func (c *localClient) ArchiveSweep(ctx context.Context, dryRun bool) (store.ArchiveSweepResult, error) {
	if dryRun {
		return store.ArchiveSweepResult{}, nil
	}
	res, err := c.store.ArchiveSweep()
	if err != nil {
		return store.ArchiveSweepResult{}, err
	}
	if res.Total() > 0 {
		c.recordOp(model.HistoryEntry{
			Op:   "archive.sweep",
			Kind: "sweep",
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
		Op:   "display.update",
		Kind: "setting",
		TargetLabel: "display.show_archived",
		Details:     boolDetails(value),
	})
	return value, nil
}

func boolDetails(v bool) string {
	if v {
		return "true"
	}
	return "false"
}
