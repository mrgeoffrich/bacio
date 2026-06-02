package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/spf13/cobra"
)

// proxyServiceUnit is the rendered OS-supervisor unit for `bacio proxy
// serve` plus the metadata the install path needs to write and load it.
type proxyServiceUnit struct {
	// Path is the absolute on-disk location the unit is written to.
	Path string
	// Body is the rendered unit file (launchd plist / systemd unit).
	Body string
	// loadArgs / loadDesc describe the loader invocation the apply step
	// runs after writing the file (launchctl load / systemctl --user).
	loadArgs []string
	loadDesc string
}

// newProxyInstallServiceCmd is `bacio proxy install-service` (BACI-344) —
// the documented keep-alive for the standalone reverse proxy. It renders a
// per-user OS-supervisor unit that runs `bacio proxy serve`, writes it, and
// registers it (launchd on macOS, a systemd user unit on Linux) so the proxy
// survives logout / restart and is the process the user essentially never
// touches. A harness-integration shim like `bacio proxy serve` itself — no
// --json / schema surface; --dry-run prints the rendered unit without writing.
func newProxyInstallServiceCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "install-service",
		Short: "Install an OS-supervisor unit that keeps `bacio proxy serve` running",
		Long: `Render and register a per-user OS-supervisor unit that runs
'bacio proxy serve', so the standalone reverse proxy survives logout / restart
and is the process you essentially never touch.

  macOS:  a launchd agent at ~/Library/LaunchAgents/io.bacio.proxy.plist,
          loaded with 'launchctl load'.
  Linux:  a systemd user unit at ~/.config/systemd/user/bacio-proxy.service,
          enabled + started with 'systemctl --user enable --now'.

The unit invokes the absolute path of the running bacio binary, so it keeps
working regardless of the PATH the supervisor spawns with. Windows is not
supported (bacio proxy serve runs there fine — only this install automation
is macOS/Linux); it errors clearly.

  --dry-run prints the rendered unit and its target path without writing
  or loading anything.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			exe, err := os.Executable()
			if err != nil {
				return fmt.Errorf("resolve bacio binary path: %w", err)
			}
			home, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("resolve home dir: %w", err)
			}
			unit, err := renderProxyServiceUnit(runtime.GOOS, exe, home)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			if dryRun {
				fmt.Fprintf(out, "# would write %s\n\n%s", unit.Path, unit.Body)
				fmt.Fprintf(out, "\n# would load it with: %s\n", unit.loadDesc)
				return nil
			}

			if err := os.MkdirAll(filepath.Dir(unit.Path), 0o755); err != nil {
				return fmt.Errorf("create service dir: %w", err)
			}
			if err := os.WriteFile(unit.Path, []byte(unit.Body), 0o644); err != nil {
				return fmt.Errorf("write service unit: %w", err)
			}
			fmt.Fprintf(out, "wrote %s\n", unit.Path)
			if len(unit.loadArgs) > 0 {
				loadCmd := exec.Command(unit.loadArgs[0], unit.loadArgs[1:]...)
				loadCmd.Stdout = out
				loadCmd.Stderr = cmd.ErrOrStderr()
				if err := loadCmd.Run(); err != nil {
					// The unit is on disk; a load failure is recoverable by
					// hand, so report it without unwinding the write.
					return fmt.Errorf("load service unit (%s): %w — the unit file is written; load it manually", unit.loadDesc, err)
				}
				fmt.Fprintf(out, "loaded: %s\n", unit.loadDesc)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the rendered unit + target path without writing or loading it")
	return cmd
}

// renderProxyServiceUnit builds the OS-supervisor unit for `bacio proxy
// serve` on the given GOOS, using exe (the absolute bacio binary path) and
// home (the user's home dir). Split out from the command so the per-GOOS
// rendering is testable without writing to disk or shelling out. An
// unsupported GOOS returns a clear error.
func renderProxyServiceUnit(goos, exe, home string) (proxyServiceUnit, error) {
	switch goos {
	case "darwin":
		path := filepath.Join(home, "Library", "LaunchAgents", "io.bacio.proxy.plist")
		body := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>io.bacio.proxy</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>proxy</string>
		<string>serve</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
</dict>
</plist>
`, exe)
		return proxyServiceUnit{
			Path:     path,
			Body:     body,
			loadArgs: []string{"launchctl", "load", path},
			loadDesc: "launchctl load " + path,
		}, nil
	case "linux", "freebsd", "openbsd", "netbsd":
		path := filepath.Join(home, ".config", "systemd", "user", "bacio-proxy.service")
		body := fmt.Sprintf(`[Unit]
Description=bacio reverse-proxy listener
After=network.target

[Service]
ExecStart=%s proxy serve
Restart=on-failure

[Install]
WantedBy=default.target
`, exe)
		return proxyServiceUnit{
			Path:     path,
			Body:     body,
			loadArgs: []string{"systemctl", "--user", "enable", "--now", "bacio-proxy.service"},
			loadDesc: "systemctl --user enable --now bacio-proxy.service",
		}, nil
	default:
		return proxyServiceUnit{}, fmt.Errorf("`bacio proxy install-service` is not supported on %s — `bacio proxy serve` runs there, but the keep-alive automation is macOS/Linux only; run it under your own supervisor", goos)
	}
}
