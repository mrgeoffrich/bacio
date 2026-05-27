// BACI-240: HTTP handlers for the ui.shipped_sfx global toggle.
// Lives in its own file (rather than crowding handlers_archive.go)
// so audio settings have a clear home if more audio toggles ever
// land — keeps the per-domain handler files tidy.
package api

import (
	"fmt"
	"net/http"

	"github.com/mrgeoffrich/bacio/internal/inputio"
	"github.com/mrgeoffrich/bacio/internal/model"
)

// AudioPreferencesOut is the response shape for
// /settings/audio-preferences. Underscore on the wire to match every
// other JSON field in this package.
type AudioPreferencesOut struct {
	ShippedSfx bool `json:"shipped_sfx"`
}

// audioPreferencesIn is the strict-decoded input for PUT.
type audioPreferencesIn struct {
	ShippedSfx bool `json:"shipped_sfx"`
}

func (d deps) handleAudioPreferencesGet(w http.ResponseWriter, r *http.Request) {
	v, err := d.store.GetUIShippedSfx()
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, &AudioPreferencesOut{ShippedSfx: v})
}

func (d deps) handleAudioPreferencesSet(w http.ResponseWriter, r *http.Request) {
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	parsed, _, err := inputio.DecodeStrict[audioPreferencesIn](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	if isDryRun(r) {
		writeDryRun(w, http.StatusOK, &AudioPreferencesOut{ShippedSfx: parsed.ShippedSfx})
		return
	}
	if err := d.store.SetUIShippedSfx(parsed.ShippedSfx); err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		Actor:       ActorFromContext(r.Context()),
		Op:          "audio.update",
		Kind:        "app_setting",
		TargetLabel: "ui.shipped_sfx",
		Details:     fmt.Sprintf("shipped_sfx=%t", parsed.ShippedSfx),
	})
	writeJSON(w, http.StatusOK, &AudioPreferencesOut{ShippedSfx: parsed.ShippedSfx})
}
