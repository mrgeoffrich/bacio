package cli

import (
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
var bacioHookEvents = []struct{ Event, Subcommand string }{
	{"SessionStart", "session-start"},
	{"UserPromptSubmit", "user-prompt-submit"},
	{"Stop", "stop"},
	{"SessionEnd", "session-end"},
}

// bacioHookMarker identifies hook groups bacio owns, so re-running
// install-hooks replaces them in place rather than stacking duplicates.
const bacioHookMarker = "bacio hook "

func newInstallHooksCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install-hooks",
		Short: "Install bacio's Claude Code hooks into the current repo",
		Long: `Merge bacio's agent-supervision hooks into <repo-root>/.claude/settings.json.

bacio registers four command hooks so a Claude Code session keeps the
local agent registry in sync without the agent calling 'bacio agent ...'
by hand:

    SessionStart      register the session; inject assigned issues + claims
    UserPromptSubmit  heartbeat; nudge on open claims
    Stop              heartbeat
    SessionEnd        end the session; auto-release its claims

The merge is non-destructive: existing hooks for other events -- and
any non-bacio hooks on these four events -- are preserved. Re-running
replaces bacio's own hook groups in place so command updates land.

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
			if err := mergeBacioHooks(path); err != nil {
				return err
			}
			return ok("installed bacio hooks (%d events) into %s", len(bacioHookEvents), path)
		},
	}
}

// mergeBacioHooks reads .claude/settings.json (treating an absent file
// as empty), merges bacio's hook groups into the "hooks" object, and
// writes it back. Non-bacio content is preserved; bacio's own groups
// are replaced in place so the merge is idempotent.
func mergeBacioHooks(path string) error {
	top := map[string]json.RawMessage{}
	switch data, err := os.ReadFile(path); {
	case err == nil:
		if len(strings.TrimSpace(string(data))) > 0 {
			if err := json.Unmarshal(data, &top); err != nil {
				return fmt.Errorf("parse %s: %w", path, err)
			}
		}
	case errors.Is(err, fs.ErrNotExist):
		// absent file -> start from empty settings
	default:
		return err
	}

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
		grp, err := json.Marshal(bacioHookGroup(ev.Subcommand))
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
// command hook invoking `bacio hook <subcommand>`. No matcher -- these
// events either don't support matchers or bacio wants every occurrence.
func bacioHookGroup(subcommand string) map[string]any {
	return map[string]any{
		"hooks": []map[string]any{
			{"type": "command", "command": "bacio hook " + subcommand},
		},
	}
}
