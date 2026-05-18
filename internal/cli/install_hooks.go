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

	"github.com/spf13/cobra"

	"github.com/mrgeoffrich/bacio/internal/git"
)

// bacioHookEvents maps each Claude Code hook event bacio handles to the
// `bacio hook` subcommand that services it. install-hooks writes one
// command hook per event into the repo's .claude/settings.json.
//
// Matcher is non-empty only for PostToolUse — the four event-typed
// hooks don't support matchers, but PostToolUse needs one to scope
// bacio's mirror to the agent's task-list tools (firing on every tool
// call would be a lot of stderr noise for a benign no-op). The matcher
// uses pipe-alternation (Claude Code's matcher syntax supports
// `Foo|Bar` for a literal multi-tool match) so one hook group
// services both TaskCreate (insert a planned task) and TaskUpdate
// (flip its status). Source of truth for the literal lives next to
// the hook handler so the two can't drift — see internal/cli/hook.go.
var bacioHookEvents = []struct{ Event, Subcommand, Matcher string }{
	{"SessionStart", "session-start", ""},
	{"UserPromptSubmit", "user-prompt-submit", ""},
	{"Stop", "stop", ""},
	{"SessionEnd", "session-end", ""},
	{"PostToolUse", "post-tool-use", postToolUseMatcher},
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

func newInstallHooksCmd() *cobra.Command {
	var assumeYes bool
	cmd := &cobra.Command{
		Use:   "install-hooks",
		Short: "Install bacio's Claude Code hooks into the current repo",
		Long: `Merge bacio's agent-supervision hooks into <repo-root>/.claude/settings.json.

bacio registers a small set of command hooks so a Claude Code session
keeps the local agent registry in sync without the agent calling
'bacio agent ...' by hand:

    SessionStart                                 register the session; inject assigned issues + claims
    UserPromptSubmit                             heartbeat; nudge on open claims
    Stop                                         heartbeat
    SessionEnd                                   end the session; auto-release its claims
    PostToolUse (matcher: TaskCreate|TaskUpdate) mirror the agent's task list into bacio

The merge is non-destructive: existing hooks for other events -- and
any non-bacio hooks on these four events -- are preserved. Re-running
replaces bacio's own hook groups in place so command updates land.

install-hooks prints the planned changes and asks for confirmation
before writing. Pass --yes (-y) to skip the prompt and accept
automatically -- needed when running non-interactively.

Activation: the hooks above are inert unless BACIO_AGENT_MODE=1 is set
in the environment of the Claude session that loads them. See the
post-install output for the recommended launch incantation.

Note: top-level keys in settings.json may be reordered, since the file
is round-tripped through a JSON decode. Hook behaviour is unchanged.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if inRemoteMode() {
				return fmt.Errorf("bacio install-hooks: not supported in remote mode (writes the settings file to the local repo); run this verb against the local DB instead")
			}
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			info, err := git.Detect(cwd)
			if err != nil {
				return err
			}
			path := filepath.Join(info.Root, ".claude", "settings.json")

			top, changes, err := planBacioHooks(path)
			if err != nil {
				return err
			}

			if !assumeYes {
				printHookPlan(path, changes)
				confirmed, err := confirmPrompt("Proceed?")
				if err != nil {
					return err
				}
				if !confirmed {
					fmt.Fprintln(os.Stderr, "aborted — no changes made")
					return nil
				}
			}

			if err := applyBacioHooks(path, top); err != nil {
				return err
			}
			if err := reportHookChanges(path, changes); err != nil {
				return err
			}
			printActivationBanner(os.Stderr)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&assumeYes, "yes", "y", false, "skip the confirmation prompt and accept the changes")
	return cmd
}

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
func applyBacioHooks(path string, top map[string]json.RawMessage) error {
	hooks := map[string]json.RawMessage{}
	if raw, ok := top["hooks"]; ok {
		if err := json.Unmarshal(raw, &hooks); err != nil {
			return fmt.Errorf("parse %s: \"hooks\" is not an object: %w", path, err)
		}
	}

	for _, ev := range bacioHookEvents {
		var groups []json.RawMessage
		if raw, ok := hooks[ev.Event]; ok {
			if err := json.Unmarshal(raw, &groups); err != nil {
				return fmt.Errorf("parse %s: \"hooks.%s\" is not an array: %w", path, ev.Event, err)
			}
		}
		var kept []json.RawMessage
		for _, g := range groups {
			if !bytes.Contains(g, []byte(bacioHookMarker)) {
				kept = append(kept, g)
			}
		}
		grp, err := json.Marshal(bacioHookGroup(ev.Subcommand, ev.Matcher))
		if err != nil {
			return err
		}
		kept = append(kept, grp)
		merged, err := json.Marshal(kept)
		if err != nil {
			return err
		}
		hooks[ev.Event] = merged
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

// printHookPlan writes the planned changes to stderr, ahead of the
// confirmation prompt.
func printHookPlan(path string, changes []hookChange) {
	fmt.Fprintf(os.Stderr, "bacio install-hooks will update %s:\n", path)
	for _, c := range changes {
		fmt.Fprintf(os.Stderr, "  %-7s %-17s → bacio hook %s\n", c.Action, c.Event, c.Subcommand)
	}
	fmt.Fprintln(os.Stderr, "Existing hooks for other events are left untouched.")
}

// reportHookChanges emits the post-write summary on stdout (via ok(), so
// it round-trips to JSON like every other command's success output).
func reportHookChanges(path string, changes []hookChange) error {
	var b strings.Builder
	fmt.Fprintf(&b, "installed bacio hooks into %s", path)
	for _, c := range changes {
		fmt.Fprintf(&b, "\n  %-7s %-17s → bacio hook %s", c.Action, c.Event, c.Subcommand)
	}
	return ok("%s", b.String())
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
