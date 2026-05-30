// BACI-312: HTTP handlers for the ui.timezone global setting. Lives in
// its own file (rather than crowding handlers_audio.go) so the timezone
// setting has a clear home — same per-domain split the audio toggle
// follows.
package api

import (
	"fmt"
	"net/http"

	"github.com/mrgeoffrich/bacio/internal/inputio"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// TimezonePreferencesOut is the response shape for
// /settings/timezone-preferences. An unset timezone returns "" — the
// React layer auto-detects the browser zone and PUTs it on first run.
type TimezonePreferencesOut struct {
	Timezone string `json:"timezone"`
}

// timezonePreferencesIn is the strict-decoded input for PUT.
type timezonePreferencesIn struct {
	Timezone string `json:"timezone"`
}

func (d deps) handleTimezonePreferencesGet(w http.ResponseWriter, r *http.Request) {
	v, err := d.store.GetUITimezone()
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, &TimezonePreferencesOut{Timezone: v})
}

func (d deps) handleTimezonePreferencesSet(w http.ResponseWriter, r *http.Request) {
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	parsed, _, err := inputio.DecodeStrict[timezonePreferencesIn](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	// Validate the IANA-name shape up front so a malformed zone is a 400
	// (the validator's shape error doesn't match statusForError's phrase
	// list, and re-validating here mirrors store.SetUITimezone's own
	// boundary check). The cleaned value is what we persist + echo back.
	clean, err := store.ValidateTimezone(parsed.Timezone)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), map[string]any{"field": "timezone"})
		return
	}
	if isDryRun(r) {
		writeDryRun(w, http.StatusOK, &TimezonePreferencesOut{Timezone: clean})
		return
	}
	if err := d.store.SetUITimezone(clean); err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		Actor:       ActorFromContext(r.Context()),
		Op:          "timezone.update",
		Kind:        "app_setting",
		TargetLabel: "ui.timezone",
		Details:     fmt.Sprintf("timezone=%s", clean),
	})
	writeJSON(w, http.StatusOK, &TimezonePreferencesOut{Timezone: clean})
}
