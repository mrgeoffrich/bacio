package cli

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// procEntry is one row of a process-table snapshot.
type procEntry struct {
	ppid int
	comm string
}

// procSnapshot takes a single `ps` snapshot of the whole process table,
// keyed by pid. One exec, walked in memory — so an ancestry walk is
// internally consistent (no race against a process exiting mid-walk).
// Best-effort: returns (nil, err) on failure; callers only ever feed
// this into diagnostics or a best-effort correlation, never anything
// that should fail the caller's real work.
func procSnapshot() (map[int]procEntry, error) {
	out, err := exec.Command("ps", "-axo", "pid=,ppid=,comm=").Output()
	if err != nil {
		return nil, err
	}
	table := make(map[int]procEntry)
	for _, ln := range strings.Split(string(out), "\n") {
		fields := strings.Fields(ln)
		if len(fields) < 3 {
			continue
		}
		p, err1 := strconv.Atoi(fields[0])
		pp, err2 := strconv.Atoi(fields[1])
		if err1 != nil || err2 != nil {
			continue
		}
		table[p] = procEntry{ppid: pp, comm: strings.Join(fields[2:], " ")}
	}
	return table, nil
}

// walkAncestry yields pid then each ancestor pid up toward init,
// calling fn for every hop. It stops at pid<=1, a self-referential
// ppid, a pid missing from the snapshot, or after 64 hops (defensive
// against a pathological cycle).
func walkAncestry(table map[int]procEntry, pid int, fn func(pid int, e procEntry)) {
	for cur, hops := pid, 0; cur > 1 && hops < 64; hops++ {
		e, ok := table[cur]
		if !ok {
			return
		}
		fn(cur, e)
		if e.ppid == cur || e.ppid <= 1 {
			return
		}
		cur = e.ppid
	}
}

// processAncestry walks parent pids from `pid` up toward init, returning
// one "pid=… ppid=… comm=…" line per ancestor. A discovery/diagnostic
// aid — best-effort, returns a single diagnostic line on `ps` failure.
func processAncestry(pid int) []string {
	table, err := procSnapshot()
	if err != nil {
		return []string{fmt.Sprintf("ps failed: %v", err)}
	}
	var lines []string
	walkAncestry(table, pid, func(p int, e procEntry) {
		lines = append(lines, fmt.Sprintf("pid=%d ppid=%d comm=%s", p, e.ppid, e.comm))
	})
	if len(lines) == 0 {
		lines = append(lines, fmt.Sprintf("pid=%d <not in ps snapshot>", pid))
	}
	return lines
}

// findClaudeAncestor walks up from `pid` and returns the pid of the
// nearest ancestor whose command is the `claude` binary, or 0 if none
// is found. This is how both `bacio channel` and `bacio hook` resolve
// the `claude` process they belong to: the channel records it, the
// hooks join on it — Claude Code never hands either side the session id
// directly. The walk handles intermediate shells (a command hook may
// be `sh -c`-wrapped) since it matches by comm, not by depth. `pid`
// itself is skipped — we want an ancestor, not self.
func findClaudeAncestor(pid int) int {
	table, err := procSnapshot()
	if err != nil {
		return 0
	}
	found := 0
	first := true
	walkAncestry(table, pid, func(p int, e procEntry) {
		if found != 0 {
			return
		}
		if first { // skip self
			first = false
			return
		}
		if isClaudeComm(e.comm) {
			found = p
		}
	})
	return found
}

// isClaudeComm reports whether a `ps` comm string names the Claude Code
// binary. `ps -o comm` reports either a bare name or an absolute path
// depending on how the process was exec'd, so we match on the basename.
func isClaudeComm(comm string) bool {
	return filepath.Base(strings.TrimSpace(comm)) == "claude"
}
