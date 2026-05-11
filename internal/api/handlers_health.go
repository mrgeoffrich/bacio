package api

import (
	"encoding/json"
	"net/http"
	"runtime/debug"
)

type healthResponse struct {
	OK      bool   `json:"ok"`
	Version string `json:"version"`
}

// version is resolved once at process start. ReadBuildInfo returns
// "(devel)" for `go build` from a working tree and the module version
// string (e.g. "v0.3.1") when installed via `go install`.
var version = func() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return info.Main.Version
	}
	return "(devel)"
}()

func (d deps) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(healthResponse{OK: true, Version: version})
}
