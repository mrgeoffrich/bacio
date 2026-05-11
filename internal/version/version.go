// Package version exposes the build's version string. Release builds
// override Version via ldflags (see scripts/release.sh); plain
// `go build` leaves it as "dev" and falls back to VCS info from the
// embedded build info, so a dev build prints e.g.
// "dev (commit abc1234, dirty)".
package version

import (
	"fmt"
	"runtime/debug"
)

// Version is replaced at link time by:
//
//	-ldflags "-X github.com/mrgeoffrich/bacio/internal/version.Version=v0.2.1"
var Version = "dev"

// String returns the version string used by `bacio --version`.
func String() string {
	if Version != "dev" {
		return Version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	var revision string
	var modified bool
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	if revision == "" {
		return "dev"
	}
	short := revision
	if len(short) > 7 {
		short = short[:7]
	}
	if modified {
		return fmt.Sprintf("dev (commit %s, dirty)", short)
	}
	return fmt.Sprintf("dev (commit %s)", short)
}
