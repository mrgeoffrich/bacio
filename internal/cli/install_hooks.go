package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// bacioHookEvents maps each Claude Code hook event bacio handles to the
// `bacio hook` subcommand that services it. install-hooks writes one
// command hook per event into the repo's .claude/settings.json.
//
// Matcher is non-empty for the tool-call hooks — the four event-typed
// hooks don't support matchers. PostToolUse carries three rows: the
// task-list mirror scoped to `TaskCreate|TaskUpdate`, the BACI-147
// terminal-title hook scoped to `mcp__bacio__register`, and the
// BACI-159 supervisor heartbeat with an empty matcher so it fires
// on every supervisor tool call (the supervisor has no other
// liveness signal during a long Task-spawned subagent run).
// PreToolUse scopes the worktree- and sqlite3-confinement guards to
// `Write|Edit|Bash`. All matchers use Claude Code's pipe-alternation
// syntax (`Foo|Bar` for a literal multi-tool match). Source of truth
// for each literal lives next to its hook handler so the two can't
// drift — see internal/cli/hook.go.
//
// Multiple rows can share an Event (PostToolUse has three since
// BACI-159). applyBacioHooks groups by Event before rewriting the
// settings array so subsequent iterations over an Event don't clobber
// the group an earlier iteration just wrote.
var bacioHookEvents = []struct{ Event, Subcommand, Matcher string }{
	{"SessionStart", "session-start", ""},
	{"UserPromptSubmit", "user-prompt-submit", ""},
	{"Stop", "stop", ""},
	{"SessionEnd", "session-end", ""},
	{"PostToolUse", "post-tool-use", postToolUseMatcher},
	{"PostToolUse", "set-title", setTitleMatcher},
	{"PostToolUse", "post-tool-use-heartbeat", ""},
	{"PreToolUse", "pre-tool-use", preToolUseMatcher},
}

// bacioHookMarker identifies hook groups bacio owns, so re-running
// install-hooks replaces them in place rather than stacking duplicates.
const bacioHookMarker = "bacio hook "

// hookChange describes what install-hooks will do for one event:
// "add" (no bacio hook there yet) or "update" (bacio already owns a
// group that'll be replaced in place).
type hookChange struct {
	Event      string
	Subcommand string
	Action     string
}

// The reusable hook-planning helpers below are consumed by
// `bacio install-agent` (the consolidated installer, BACI-79) and the
// hook-helper unit tests. There is no standalone `install-hooks`
// command any more.

// planBacioHooks reads .claude/settings.json (treating an absent file as
// empty) and reports what applyBacioHooks would do — without writing.
// Returns the parsed top-level object so the apply step doesn't re-read.
func planBacioHooks(path string) (map[string]json.RawMessage, []hookChange, error) {
	top := map[string]json.RawMessage{}
	switch data, err := os.ReadFile(path); {
	case err == nil:
		if len(strings.TrimSpace(string(data))) > 0 {
			if err := json.Unmarshal(data, &top); err != nil {
				return nil, nil, fmt.Errorf("parse %s: %w", path, err)
			}
		}
	case errors.Is(err, fs.ErrNotExist):
		// absent file -> start from empty settings
	default:
		return nil, nil, err
	}

	hooks := map[string]json.RawMessage{}
	if raw, ok := top["hooks"]; ok {
		if err := json.Unmarshal(raw, &hooks); err != nil {
			return nil, nil, fmt.Errorf("parse %s: \"hooks\" is not an object: %w", path, err)
		}
	}

	changes := make([]hookChange, 0, len(bacioHookEvents))
	for _, ev := range bacioHookEvents {
		action := "add"
		if raw, ok := hooks[ev.Event]; ok && bytes.Contains(raw, []byte(bacioHookMarker)) {
			action = "update"
		}
		changes = append(changes, hookChange{Event: ev.Event, Subcommand: ev.Subcommand, Action: action})
	}
	return top, changes, nil
}

// applyBacioHooks merges bacio's hook groups into the (already-parsed)
// top-level settings object and writes it back. Non-bacio content is
// preserved; bacio's own groups are replaced in place so the merge is
// idempotent.
//
// bacioHookEvents may carry multiple rows for one Event (BACI-147 added
// a second PostToolUse row). The rewrite groups bacio-owned rows by
// Event and applies them in a single pass per event — strip every
// bacio-owned group from the existing array once, then append every
// grouped bacio entry — so a second iteration over the same event
// can't clobber what the first one wrote.
func applyBacioHooks(path string, top map[string]json.RawMessage) error {
	hooks := map[string]json.RawMessage{}
	if raw, ok := top["hooks"]; ok {
		if err := json.Unmarshal(raw, &hooks); err != nil {
			return fmt.Errorf("parse %s: \"hooks\" is not an object: %w", path, err)
		}
	}

	// Group bacio-owned rows by Event and keep the events in their
	// first-seen order so the on-disk JSON ordering stays deterministic
	// across runs (matches the iteration order of bacioHookEvents).
	type pendingRow struct{ Subcommand, Matcher string }
	pending := map[string][]pendingRow{}
	var eventOrder []string
	for _, ev := range bacioHookEvents {
		if _, seen := pending[ev.Event]; !seen {
			eventOrder = append(eventOrder, ev.Event)
		}
		pending[ev.Event] = append(pending[ev.Event], pendingRow{Subcommand: ev.Subcommand, Matcher: ev.Matcher})
	}

	for _, event := range eventOrder {
		var groups []json.RawMessage
		if raw, ok := hooks[event]; ok {
			if err := json.Unmarshal(raw, &groups); err != nil {
				return fmt.Errorf("parse %s: \"hooks.%s\" is not an array: %w", path, event, err)
			}
		}
		var kept []json.RawMessage
		for _, g := range groups {
			if !bytes.Contains(g, []byte(bacioHookMarker)) {
				kept = append(kept, g)
			}
		}
		for _, row := range pending[event] {
			grp, err := json.Marshal(bacioHookGroup(row.Subcommand, row.Matcher))
			if err != nil {
				return err
			}
			kept = append(kept, grp)
		}
		merged, err := json.Marshal(kept)
		if err != nil {
			return err
		}
		hooks[event] = merged
	}

	hooksRaw, err := json.Marshal(hooks)
	if err != nil {
		return err
	}
	top["hooks"] = hooksRaw

	out, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create .claude dir: %w", err)
	}
	return os.WriteFile(path, out, 0o644)
}

// bacioHookGroup builds the hook-group object for one event: a single
// command hook invoking `bacio hook <subcommand>`. matcher is included
// only when non-empty — the four event-typed hooks (SessionStart,
// UserPromptSubmit, Stop, SessionEnd) don't support matchers, but
// PostToolUse needs one to scope bacio's mirror to the agent's task
// list tools (TaskCreate|TaskUpdate) and dodge stderr noise on every
// other tool call.
func bacioHookGroup(subcommand, matcher string) map[string]any {
	g := map[string]any{
		"hooks": []map[string]any{
			{"type": "command", "command": "bacio hook " + subcommand},
		},
	}
	if matcher != "" {
		g["matcher"] = matcher
	}
	return g
}

// confirmPrompt asks a yes/no question on stderr and reads the answer
// from stdin. Returns (true, nil) for y/yes; (false, nil) for anything
// else; (false, err) when stdin can't be read at all (EOF — e.g. a
// non-tty pipe), so the caller can point the user at --yes.
func confirmPrompt(question string) (bool, error) {
	fmt.Fprintf(os.Stderr, "%s [y/N] ", question)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return false, fmt.Errorf("cannot read confirmation from stdin — re-run with --yes to accept non-interactively")
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}
