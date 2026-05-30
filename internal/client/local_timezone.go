// BACI-312: local-side wrappers for the ui.timezone global setting.
// Mirrors local_audio.go's GetUIShippedSfx / SetUIShippedSfx shape — a
// single value, defensive read, audit row on write.
package client

import (
	"context"
	"fmt"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// GetUITimezone reads the BACI-312 ui.timezone setting ("" when unset).
func (c *localClient) GetUITimezone(ctx context.Context) (string, error) {
	return c.store.GetUITimezone()
}

// SetUITimezone validates + writes the BACI-312 setting and records an
// audit row. Returns the post-write value (or the projected value on
// dry-run). The validator runs even on dry-run so a malformed IANA name
// is rejected before the projection is returned.
func (c *localClient) SetUITimezone(ctx context.Context, value string, dryRun bool) (string, error) {
	clean, err := store.ValidateTimezone(value)
	if err != nil {
		return "", err
	}
	if dryRun {
		return clean, nil
	}
	if err := c.store.SetUITimezone(clean); err != nil {
		return "", err
	}
	c.recordOp(model.HistoryEntry{
		// kind / target / details match the HTTP surface's
		// handleTimezonePreferencesSet so `bacio history --kind app_setting`
		// returns the same rows whether the value was set from the CLI or
		// the HTTP surface (same precedent as the BACI-240 audio toggle).
		Op:          "timezone.update",
		Kind:        "app_setting",
		TargetLabel: "ui.timezone",
		Details:     fmt.Sprintf("timezone=%s", clean),
	})
	return clean, nil
}
