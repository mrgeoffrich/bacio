// Package procfind answers one narrow question — *which processes hold
// a LISTEN socket on a given TCP port?* — and provides thin signalling
// helpers on top of the PIDs it discovers.
//
// It exists for `bacio worktree rm` (BACI-93): when a worktree is torn
// down, any long-running bacio process (`bacio api` / `bacio web` /
// `bacio-desktop`) still bound to that worktree's allocated API port
// is an orphan that keeps heartbeating the shared `ui_leader` lease.
// `worktree rm` finds those processes by their listening socket and
// signals them.
//
// The port-discovery surface is the only platform-specific code, so it
// gets the `_unix.go` / `_windows.go` build-tag split already used in
// `internal/sync/`. The package takes no dependency on `internal/cli`
// or `internal/wtenv` — it is pure process plumbing, unit-testable by
// swapping the `runCommand` exec seam.
package procfind

import (
	"errors"
	"os"
	"os/exec"
)

// Listener is one process holding a LISTEN socket on the queried port.
type Listener struct {
	// PID is the process id.
	PID int
	// Command is a best-effort process name / argv[0], used for display
	// and the caller's "is this actually a bacio process?" safety check.
	Command string
}

// ErrToolMissing is returned by ListenersOnPort when the platform's
// process-discovery binary (lsof on unix, netstat on Windows) is not on
// PATH. Callers should degrade to "couldn't check, kill manually"
// rather than failing the operation.
var ErrToolMissing = errors.New("procfind: process-discovery tool not found on PATH")

// runCommand is the exec seam. Tests swap it for a stub that returns
// canned tool output; production runs the real binary.
var runCommand = func(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).Output()
}

// ListenersOnPort returns every process with a LISTEN socket on the
// given TCP port. Best-effort: returns (nil, err) when the platform
// discovery tool is missing or fails — callers degrade gracefully
// rather than aborting their operation.
func ListenersOnPort(port int) ([]Listener, error) {
	return listenersOnPort(port)
}

// Alive reports whether pid still names a live process. Used for the
// SIGTERM grace poll. On unix a zero-signal kill probes liveness; on
// Windows os.FindProcess + a follow-up check is used (see the
// platform file).
func Alive(pid int) bool {
	return alive(pid)
}

// SignalKill force-terminates pid. This is os.Process.Kill everywhere
// (SIGKILL on unix, TerminateProcess on Windows).
func SignalKill(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}

// SignalTerm asks pid to shut down gracefully. On unix this is
// SIGTERM. On Windows there is no SIGTERM — a console process can't
// trap a graceful signal — so SignalTerm is an alias for SignalKill
// there (see procfind_windows.go); the SIGTERM→SIGKILL grace dance in
// the caller collapses to a single kill, which is correct.
func SignalTerm(pid int) error {
	return signalTerm(pid)
}
