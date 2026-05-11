package api

import "net/http"

// isDryRun reports whether the caller asked to validate without committing.
// Accepts either ?dry_run=true|1 or X-Dry-Run: true|1 so callers can use
// whichever surface their HTTP client is most ergonomic with.
func isDryRun(r *http.Request) bool {
	q := r.URL.Query().Get("dry_run")
	if q == "true" || q == "1" {
		return true
	}
	h := r.Header.Get("X-Dry-Run")
	return h == "true" || h == "1"
}

// writeDryRun emits the projected entity at the same status the real call
// would use. The X-Dry-Run header MUST be set before WriteHeader fires,
// which writeJSON does internally — hence the order here.
func writeDryRun(w http.ResponseWriter, status int, body any) {
	w.Header().Set("X-Dry-Run", "applied")
	writeJSON(w, status, body)
}
