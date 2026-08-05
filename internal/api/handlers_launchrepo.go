// BACI-368: GET /launch-repo tells the UI which repo the server
// process was started in, so a bare /ui/ load opens on that repo
// instead of the last-remembered pick. The git resolution (and the
// auto-enrolment that comes with it) happens in the cobra command at
// startup — this layer only echoes the answer it was handed.
package api

import "net/http"

// LaunchRepoOut is the response shape for /launch-repo. An empty
// prefix means the process wasn't started inside a git repo (or the
// resolution failed); the UI falls back to its remembered pick.
type LaunchRepoOut struct {
	Prefix string `json:"prefix"`
}

func (d deps) handleLaunchRepo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, &LaunchRepoOut{Prefix: d.opts.LaunchRepoPrefix})
}
