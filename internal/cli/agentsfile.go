package cli

import (
	"encoding/json"
	"errors"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/mrgeoffrich/bacio/internal/git"
)

// agentEntry is one `claude` process's worth of identity state in
// .bacio/agents.json: which agent identity that process runs as, and
// the session ids it has gone through (a new one per /clear).
type agentEntry struct {
	Name      string    `json:"name"`
	Host      string    `json:"host,omitempty"`
	Sessions  []string  `json:"sessions,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// agentsFile is .bacio/agents.json — a flat map from claude_pid (as a
// string, because JSON object keys are strings) to the identity that
// `claude` process is running as. It's the write-side "which identity
// for this process" anchor that lets multiple agents share one repo;
// the live session registry proper is the agent_sessions table.
//
// Concurrency is deliberately best-effort: no locking, just an atomic
// temp-file + rename per write and a handful of backed-off retries. The
// rename means a write can never *tear* the file — a reader always sees
// a complete document. But a write is a whole-file replace built from a
// possibly-stale read, so two writers racing for *different* claude_pids
// can lose one another's entry: the loser's entry simply reappears on
// that agent's next hook fire (session-start / every prompt / stop), so
// it's self-healing within one hook tick. This tradeoff is intentional;
// a lock would be the fix if it ever starts to matter.
type agentsFile map[string]agentEntry

const agentsFileName = "agents.json"

func agentsFilePath(root string) string {
	return filepath.Join(root, ".bacio", agentsFileName)
}

// loadAgentsFile reads .bacio/agents.json. A missing file is not an
// error — it yields an empty map (the first session in the repo). A
// corrupt file is also treated as empty rather than wedging identity
// resolution forever; the next write overwrites it.
func loadAgentsFile(root string) (agentsFile, error) {
	b, err := os.ReadFile(agentsFilePath(root))
	if errors.Is(err, os.ErrNotExist) {
		return agentsFile{}, nil
	}
	if err != nil {
		return nil, err
	}
	af := agentsFile{}
	if len(b) == 0 {
		return af, nil
	}
	if err := json.Unmarshal(b, &af); err != nil {
		return agentsFile{}, nil
	}
	return af, nil
}

// save writes the map back atomically (temp file + rename) and ensures
// .bacio/.gitignore exists. It first prunes entries whose claude_pid is
// no longer a live process — GC keyed on actual liveness, not a timer.
func (af agentsFile) save(root string) error {
	af.pruneDead()
	dir := filepath.Join(root, ".bacio")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	gitignore := filepath.Join(dir, ".gitignore")
	if _, err := os.Stat(gitignore); errors.Is(err, os.ErrNotExist) {
		_ = os.WriteFile(gitignore, []byte("*\n"), 0o644) // best-effort
	}
	b, err := json.MarshalIndent(af, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp, err := os.CreateTemp(dir, agentsFileName+".*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, agentsFilePath(root))
}

// pruneDead drops entries whose claude_pid is no longer a live process.
// A `ps` failure leaves the map untouched — keeping a stale entry beats
// nuking every identity on a transient hiccup.
func (af agentsFile) pruneDead() {
	table, err := procSnapshot()
	if err != nil {
		return
	}
	for pidStr := range af {
		pid, err := strconv.Atoi(pidStr)
		if err != nil {
			delete(af, pidStr) // unparseable key — junk
			continue
		}
		if _, alive := table[pid]; !alive {
			delete(af, pidStr)
		}
	}
}

// readAgentIdentity returns the agent slug recorded for claudePID, or
// "" when there's no entry (or no file). The read-side resolver the
// channel hits every poll tick and the hooks consult on session start.
//
// There's no liveness check on the entry: callers pass their own
// resolved claude_pid (an ancestor of the calling process, hence
// alive), so the only stale-read hazard is a recycled pid whose
// previous `claude` died. That self-corrects — pruneDead clears dead
// pids on the next write, the new process's session-start hook
// overwrites the key, and the channel re-reads every tick so it picks
// the correction up within one poll.
func readAgentIdentity(root string, claudePID int) string {
	if root == "" || claudePID == 0 {
		return ""
	}
	af, err := loadAgentsFile(root)
	if err != nil {
		return ""
	}
	if e, ok := af[strconv.Itoa(claudePID)]; ok {
		return e.Name
	}
	return ""
}

// resolveProcessIdentity figures out, once per process, which agent
// this `bacio` invocation is running as: walk up to the `claude`
// process, find the repo, look up that claude_pid in .bacio/agents.json.
// It's memoised — the process tree and the file are stable across a
// short-lived CLI invocation — and it's the read-side counterpart to
// the hooks' write-side recordAgentSession. Empty results (no claude
// ancestor, not a git repo, no agents.json entry) just mean "not
// running under a recorded agent"; callers fall back accordingly.
var (
	procIdentityOnce sync.Once
	procIdentityName string
	procIdentitySess string
)

func resolveProcessIdentity() {
	procIdentityOnce.Do(func() {
		cwd, err := os.Getwd()
		if err != nil {
			return
		}
		info, err := git.Detect(cwd)
		if err != nil {
			return
		}
		claudePID := findClaudeAncestor(os.Getpid())
		if claudePID == 0 {
			return
		}
		af, err := loadAgentsFile(info.Root)
		if err != nil {
			return
		}
		e, ok := af[strconv.Itoa(claudePID)]
		if !ok {
			return
		}
		procIdentityName = e.Name
		if n := len(e.Sessions); n > 0 {
			procIdentitySess = e.Sessions[n-1] // newest session for this process
		}
	})
}

// agentIdentityForProcess returns the agent slug this `bacio` process is
// running as (per .bacio/agents.json), or "" when it isn't running
// under a recorded agent.
func agentIdentityForProcess() string {
	resolveProcessIdentity()
	return procIdentityName
}

// sessionIDForProcess returns the newest session id recorded for this
// process's `claude` pid in .bacio/agents.json, or "" when none.
func sessionIDForProcess() string {
	resolveProcessIdentity()
	return procIdentitySess
}

// recordAgentSession ensures .bacio/agents.json has an entry for
// claudePID under `name`, with sessionID in its session list and
// updated_at bumped. Read-modify-write retried a few times; see the
// agentsFile doc comment on why that's enough.
func recordAgentSession(root string, claudePID int, host, name, sessionID string) error {
	if root == "" || claudePID == 0 || name == "" {
		return nil
	}
	key := strconv.Itoa(claudePID)
	const maxAttempts = 5
	var lastErr error
	for attempt := range maxAttempts {
		if attempt > 0 {
			// Best-effort jittered backoff — no locking by design (see
			// the agentsFile doc comment), so spacing concurrent writers
			// out is all we can do to cut down on clobbers.
			time.Sleep(time.Duration(attempt)*8*time.Millisecond + time.Duration(rand.IntN(8))*time.Millisecond)
		}
		af, err := loadAgentsFile(root)
		if err != nil {
			lastErr = err
			continue
		}
		e := af[key]
		e.Name = name
		if host != "" {
			e.Host = host
		}
		if sessionID != "" && !slices.Contains(e.Sessions, sessionID) {
			e.Sessions = append(e.Sessions, sessionID)
		}
		e.UpdatedAt = time.Now().UTC()
		af[key] = e
		if err := af.save(root); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	return lastErr
}
