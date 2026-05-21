//go:build windows

package procfind

import (
	"bufio"
	"bytes"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// listenersOnPort uses `netstat -ano -p TCP` to find LISTENING sockets
// on the given port, then resolves each matched PID's image name with
// a targeted `tasklist` call.
//
// netstat rows look like:
//
//	  TCP    127.0.0.1:5401    0.0.0.0:0    LISTENING    48213
//
// We keep rows in the LISTENING state whose local address ends in
// `:<port>`, collect the trailing PID column, then run one tasklist
// per matched PID (only the few we matched, so it stays cheap).
func listenersOnPort(port int) ([]Listener, error) {
	if _, err := exec.LookPath("netstat"); err != nil {
		return nil, ErrToolMissing
	}
	out, err := runCommand("netstat", "-ano", "-p", "TCP")
	if err != nil {
		return nil, fmt.Errorf("procfind: netstat failed: %w", err)
	}
	pids := parseNetstat(out, port)
	listeners := make([]Listener, 0, len(pids))
	for _, pid := range pids {
		listeners = append(listeners, Listener{
			PID:     pid,
			Command: tasklistImage(pid),
		})
	}
	return listeners, nil
}

// parseNetstat scans `netstat -ano` output for LISTENING TCP rows on
// the given port and returns the deduped PID set.
func parseNetstat(out []byte, port int) []int {
	suffix := ":" + strconv.Itoa(port)
	var (
		pids []int
		seen = map[int]bool{}
	)
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		// proto, local, foreign, state, pid
		if len(fields) < 5 {
			continue
		}
		if !strings.EqualFold(fields[0], "TCP") {
			continue
		}
		if !strings.EqualFold(fields[3], "LISTENING") {
			continue
		}
		if !strings.HasSuffix(fields[1], suffix) {
			continue
		}
		pid, err := strconv.Atoi(fields[4])
		if err != nil || seen[pid] {
			continue
		}
		seen[pid] = true
		pids = append(pids, pid)
	}
	return pids
}

// tasklistImage resolves a PID's image name via tasklist. Best-effort:
// an empty string on any failure — the command name is for display and
// the bacio safety check, and the caller treats an unrecognised name
// as "not a bacio process" (leaves it running), which is the safe
// default.
func tasklistImage(pid int) string {
	out, err := runCommand("tasklist", "/FI",
		fmt.Sprintf("PID eq %d", pid), "/FO", "CSV", "/NH")
	if err != nil {
		return ""
	}
	// CSV row: "image.exe","PID","Session","Session#","Mem"
	line := strings.TrimSpace(string(out))
	if line == "" || !strings.HasPrefix(line, "\"") {
		return ""
	}
	end := strings.Index(line[1:], "\"")
	if end < 0 {
		return ""
	}
	return line[1 : 1+end]
}

// signalTerm on Windows has no graceful SIGTERM equivalent — a console
// process can't trap one — so it aliases SignalKill. The caller's
// SIGTERM→SIGKILL grace dance therefore collapses to a single kill on
// Windows, which is correct.
func signalTerm(pid int) error {
	return SignalKill(pid)
}

// alive reports whether pid still names a live process by re-scanning
// the process table via tasklist.
func alive(pid int) bool {
	out, err := runCommand("tasklist", "/FI",
		fmt.Sprintf("PID eq %d", pid), "/FO", "CSV", "/NH")
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "\""+strconv.Itoa(pid)+"\"")
}
