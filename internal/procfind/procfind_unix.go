//go:build !windows

package procfind

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

// listenersOnPort shells out to lsof to find LISTEN sockets on the
// given TCP port. The `-F` field output is stable and trivial to
// parse: lsof emits a `p<pid>` line then a `c<command>` line for each
// matched process (and an `n<name>` line we don't need here).
//
//	lsof -nP -iTCP:<port> -sTCP:LISTEN -Fpcn
//
// `-n` skips DNS, `-P` skips port-name lookup — both keep it fast and
// deterministic. lsof ships on macOS by default and almost always on
// Linux; a missing binary returns ErrToolMissing so the caller can
// tell the user to kill manually.
func listenersOnPort(port int) ([]Listener, error) {
	if _, err := exec.LookPath("lsof"); err != nil {
		return nil, ErrToolMissing
	}
	out, err := runCommand("lsof", "-nP",
		fmt.Sprintf("-iTCP:%d", port), "-sTCP:LISTEN", "-Fpcn")
	if err != nil {
		// lsof exits non-zero (1) when nothing matches the filter —
		// that is an empty result, not a failure. Distinguish it from
		// a real error by checking for any parsed output: if lsof
		// printed nothing and exited 1, treat it as "no listeners".
		if exitErr, ok := err.(*exec.ExitError); ok && len(bytes.TrimSpace(out)) == 0 {
			_ = exitErr
			return nil, nil
		}
		return nil, fmt.Errorf("procfind: lsof failed: %w", err)
	}
	return parseLsof(out), nil
}

// parseLsof turns lsof `-F` field output into Listener rows. Each
// process set starts with a `p<pid>` line; the `c<command>` line that
// follows carries the command name. lsof can emit multiple `n<name>`
// (socket) lines per process — we only want one Listener per PID, so
// later duplicate `p` lines for the same PID are deduped.
func parseLsof(out []byte) []Listener {
	var (
		listeners []Listener
		seen      = map[int]int{} // pid -> index in listeners
		curPID    = -1
	)
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		switch line[0] {
		case 'p':
			pid, err := strconv.Atoi(line[1:])
			if err != nil {
				curPID = -1
				continue
			}
			curPID = pid
			if _, ok := seen[pid]; !ok {
				seen[pid] = len(listeners)
				listeners = append(listeners, Listener{PID: pid})
			}
		case 'c':
			if curPID < 0 {
				continue
			}
			if idx, ok := seen[curPID]; ok && listeners[idx].Command == "" {
				listeners[idx].Command = line[1:]
			}
		}
	}
	return listeners
}

// signalTerm sends SIGTERM on unix.
func signalTerm(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Signal(syscall.SIGTERM)
}

// alive probes liveness with signal 0 — the standard unix idiom. A nil
// error (or EPERM, meaning the process exists but is owned by someone
// else) means alive; ESRCH means gone.
func alive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	return err == syscall.EPERM
}
