package api

import (
	"encoding/json"
	"net/http"

	"github.com/mrgeoffrich/bacio/internal/version"
)

type healthResponse struct {
	OK      bool   `json:"ok"`
	Version string `json:"version"`
}

type versionResponse struct {
	Version string `json:"version"`
}

func (d deps) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(healthResponse{OK: true, Version: version.String()})
}

// handleVersion is the auth-gated counterpart to /healthz. The web bundle's
// Settings panel reads it to cross-check the server's bacio version against
// the per-session version each agent reports — so the two surfaces MUST
// agree, hence both use internal/version.String() rather than the older
// debug.ReadBuildInfo() shape.
func (d deps) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, &versionResponse{Version: version.String()})
}
