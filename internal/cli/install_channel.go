package cli

import (
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

// mcpServerName is the key bacio uses under "mcpServers" in .mcp.json,
// and the name passed to `claude --dangerously-load-development-channels
// server:<name>`.
const mcpServerName = "bacio"

func newInstallChannelCmd() *cobra.Command {
	var assumeYes bool
	cmd := &cobra.Command{
		Use:   "install-channel",
		Short: "Register the bacio channel MCP server in the current repo's .mcp.json",
		Long: `Add a "bacio" entry to <repo-root>/.mcp.json so Claude Code can spawn
'bacio channel' -- the MCP server that pushes queued dispatches into a
running session live (see 'bacio channel --help').

The merge is non-destructive: other mcpServers entries and other
top-level keys in .mcp.json are preserved. Re-running refreshes bacio's
entry in place.

install-channel prints the planned change and asks for confirmation
before writing. Pass --yes (-y) to skip the prompt.

Activation: the channel (and bacio's hooks) are inert unless
BACIO_AGENT_MODE=1 is set in the environment of the Claude session
that loads them. See the post-install output for the recommended
launch incantation. The channel rides the regular .mcp.json MCP
transport — Claude Code loads it automatically once the entry is
written, no extra flags required.

Optional: bacio can also be loaded as a Claude Code experimental
"native" channel via --dangerously-load-development-channels
server:bacio (Claude Code v2.1.80+). That path is independent of the
default MCP transport above and is not required.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if inRemoteMode() {
				return fmt.Errorf("bacio install-channel: not supported in remote mode (writes the .mcp.json file to the local repo); run this verb against the local DB instead")
			}
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			info, err := git.Detect(cwd)
			if err != nil {
				return err
			}
			path := filepath.Join(info.Root, ".mcp.json")
			command := bacioBinaryPath()

			top, action, err := planBacioChannel(path)
			if err != nil {
				return err
			}

			if !assumeYes {
				fmt.Fprintf(os.Stderr, "bacio install-channel will %s the %q MCP server in %s:\n", action, mcpServerName, path)
				fmt.Fprintf(os.Stderr, "  command: %s channel\n", command)
				confirmed, err := confirmPrompt("Proceed?")
				if err != nil {
					return err
				}
				if !confirmed {
					fmt.Fprintln(os.Stderr, "aborted — no changes made")
					return nil
				}
			}

			if err := applyBacioChannel(path, top, command); err != nil {
				return err
			}
			if err := reportChannelInstall(path, action); err != nil {
				return err
			}
			printActivationBanner(os.Stderr)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&assumeYes, "yes", "y", false, "skip the confirmation prompt and accept the change")
	return cmd
}

// bacioBinaryPath returns the absolute path of the running bacio binary
// so the .mcp.json entry resolves regardless of the PATH Claude Code's
// MCP subsystem spawns with. Falls back to "bacio" (a PATH lookup) when
// the path can't be determined.
func bacioBinaryPath() string {
	if exe, err := os.Executable(); err == nil {
		return exe
	}
	return "bacio"
}

// planBacioChannel reads .mcp.json (an absent file counts as empty) and
// reports whether bacio's entry would be added or updated, returning the
// parsed top-level object so the apply step doesn't re-read.
func planBacioChannel(path string) (map[string]json.RawMessage, string, error) {
	top := map[string]json.RawMessage{}
	switch data, err := os.ReadFile(path); {
	case err == nil:
		if len(strings.TrimSpace(string(data))) > 0 {
			if err := json.Unmarshal(data, &top); err != nil {
				return nil, "", fmt.Errorf("parse %s: %w", path, err)
			}
		}
	case errors.Is(err, fs.ErrNotExist):
		// absent file -> start from empty config
	default:
		return nil, "", err
	}

	action := "add"
	if raw, ok := top["mcpServers"]; ok {
		servers := map[string]json.RawMessage{}
		if err := json.Unmarshal(raw, &servers); err != nil {
			return nil, "", fmt.Errorf("parse %s: \"mcpServers\" is not an object: %w", path, err)
		}
		if _, exists := servers[mcpServerName]; exists {
			action = "update"
		}
	}
	return top, action, nil
}

// applyBacioChannel merges bacio's server entry into the (already-parsed)
// top-level .mcp.json object and writes it back. Other servers and other
// top-level keys are preserved.
func applyBacioChannel(path string, top map[string]json.RawMessage, command string) error {
	servers := map[string]json.RawMessage{}
	if raw, ok := top["mcpServers"]; ok {
		if err := json.Unmarshal(raw, &servers); err != nil {
			return fmt.Errorf("parse %s: \"mcpServers\" is not an object: %w", path, err)
		}
	}

	entry, err := json.Marshal(map[string]any{
		"command": command,
		"args":    []string{"channel"},
	})
	if err != nil {
		return err
	}
	servers[mcpServerName] = entry

	serversRaw, err := json.Marshal(servers)
	if err != nil {
		return err
	}
	top["mcpServers"] = serversRaw

	out, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	return os.WriteFile(path, out, 0o644)
}

// reportChannelInstall emits the success summary on stdout via ok() so
// it round-trips to JSON like every other command's success output. The
// activation guidance (which flag/env var to launch with) is intentionally
// kept out of this payload and printed separately to stderr by
// printActivationBanner — folding paragraphs of human guidance into
// the structured success body would clutter machine consumers' parse
// path. The prior message also led users to launch with
// --dangerously-load-development-channels, which is for Claude Code's
// experimental native-channels feature; bacio actually rides the
// regular .mcp.json MCP transport, so that hint was misleading
// (BACI-48).
func reportChannelInstall(path, action string) error {
	done := "added"
	if action == "update" {
		done = "updated"
	}
	return ok("%s the %q MCP server in %s", done, mcpServerName, path)
}
