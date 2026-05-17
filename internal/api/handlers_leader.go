package api

import "net/http"

// handleLeader returns the elector's current state to a polling browser.
// Auth-gated like /version — local-loopback callers without a token still
// get it freely; cross-origin callers need the bearer header. The route is
// thin on purpose: the elector caches state, so this is a single map read.
func (d deps) handleLeader(w http.ResponseWriter, r *http.Request) {
	if d.leader == nil {
		// Server.Run starts the elector before ListenAndServe, so this
		// branch shouldn't fire in production. Defensive — returns the
		// "nobody is leader" shape rather than 500-ing the browser.
		writeJSON(w, http.StatusOK, &LeaderStatusDTO{})
		return
	}
	s := d.leader.currentState()
	writeJSON(w, http.StatusOK, &LeaderStatusDTO{
		AmLeader:    s.AmLeader,
		HolderLabel: s.HolderLabel,
	})
}
