package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/mrgeoffrich/bacio/internal/wtenv"
)

// mcpServerName is the key bacio uses under "mcpServers" in .mcp.json,
// and the name passed to `claude --dangerously-load-development-channels
// server:<name>`.
const mcpServerName = "bacio"

// The reusable channel-planning helpers below are consumed by
// `bacio install-agent` (the consolidated installer, BACI-79) and the
// channel-helper unit tests. There is no standalone `install-channel`
// command any more.

// printWorktreeManifestHint surfaces the per-worktree manifest's
// existence (or absence) on stderr ahead of the activation banner.
// Tells the user which DB / API port the channel + hooks will pick up
// when they next launch Claude here. BACI-63: the channel resolves
// from cwd at runtime, so no env baking is needed in .mcp.json — but
// users still benefit from seeing what bacio thinks the right manifest
// is.
func printWorktreeManifestHint(w io.Writer, root string) {
	manifest := filepath.Join(root, wtenv.DefaultManifestFilename)
	if _, err := os.Stat(manifest); err == nil {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Worktree manifest:")
		fmt.Fprintf(w, "  %s\n", manifest)
		fmt.Fprintln(w, "  The channel + hooks resolve this automatically from cwd — no")
		fmt.Fprintln(w, "  env baking is required in .mcp.json.")
		return
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Worktree manifest:")
	fmt.Fprintln(w, "  (none — this worktree will share ~/.bacio/db.sqlite + 127.0.0.1:5320")
	fmt.Fprintln(w, "   with every other manifest-free bacio instance on this machine. Run")
	fmt.Fprintln(w, "   `bacio worktree init` to give this worktree its own DB + port.)")
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

