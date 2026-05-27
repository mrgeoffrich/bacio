// BACI-240: local-side wrappers for the ui.shipped_sfx global toggle.
// Mirrors local_archive.go's GetDisplayShowArchived / SetDisplayShowArchived
// shape — single boolean, defensive read, audit row on write.
package client

import (
	"context"
	"fmt"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// GetUIShippedSfx reads the BACI-240 ui.shipped_sfx toggle.
func (c *localClient) GetUIShippedSfx(ctx context.Context) (bool, error) {
	return c.store.GetUIShippedSfx()
}

// SetUIShippedSfx writes the BACI-240 toggle and records an audit row.
// Returns the post-write value (or the projected value on dry-run).
func (c *localClient) SetUIShippedSfx(ctx context.Context, value, dryRun bool) (bool, error) {
	if dryRun {
		return value, nil
	}
	if err := c.store.SetUIShippedSfx(value); err != nil {
		return false, err
	}
	c.recordOp(model.HistoryEntry{
		// kind / target / details match the HTTP surface's
		// handleAudioPreferencesSet so `bacio history --kind app_setting`
		// returns the same rows whether the toggle was flipped from the
		// CLI or the HTTP surface (same precedent as the BACI-68 /
		// BACI-89 / BACI-162 toggles).
		Op:          "audio.update",
		Kind:        "app_setting",
		TargetLabel: "ui.shipped_sfx",
		Details:     fmt.Sprintf("shipped_sfx=%t", value),
	})
	return value, nil
}
