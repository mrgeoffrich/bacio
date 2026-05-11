package cli

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestInRemoteMode_EnvVar covers the simple env-var fallback so the helper
// every local-only verb gates on is regression-protected.
func TestInRemoteMode_EnvVar(t *testing.T) {
	// Save and clear opts so the test is order-independent.
	saved := opts
	opts.remote = ""
	t.Cleanup(func() { opts = saved })

	t.Setenv("BACIO_REMOTE", "")
	if inRemoteMode() {
		t.Fatalf("inRemoteMode should be false with no flag and empty env")
	}
	t.Setenv("BACIO_REMOTE", "http://example.invalid")
	if !inRemoteMode() {
		t.Fatalf("inRemoteMode should be true when BACIO_REMOTE is set")
	}
}

// TestInRemoteMode_Flag confirms the --remote flag also switches the helper.
func TestInRemoteMode_Flag(t *testing.T) {
	saved := opts
	t.Cleanup(func() { opts = saved })
	t.Setenv("BACIO_REMOTE", "")

	opts.remote = "http://example.invalid"
	if !inRemoteMode() {
		t.Fatalf("inRemoteMode should be true when --remote is set")
	}
}

// TestLocalOnlyVerbsRejectRemote drives the local-only cobra commands
// (`init`, `install-skill`, `tui`) with BACIO_REMOTE set and asserts each
// returns an error mentioning the remote-mode contract. This keeps the
// "this verb writes to the local filesystem / DB" gate from silently
// regressing if a future refactor moves the check around.
func TestLocalOnlyVerbsRejectRemote(t *testing.T) {
	saved := opts
	t.Cleanup(func() { opts = saved })
	opts.remote = ""
	t.Setenv("BACIO_REMOTE", "http://example.invalid")

	cases := []struct {
		name string
		cmd  *cobra.Command
	}{
		{name: "init", cmd: newInitCmd()},
		{name: "install-skill", cmd: newInstallSkillCmd()},
		{name: "tui", cmd: newTUICmd()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.cmd.RunE == nil {
				t.Fatalf("%s: command has no RunE", tc.name)
			}
			err := tc.cmd.RunE(tc.cmd, nil)
			if err == nil {
				t.Fatalf("%s: expected error in remote mode, got nil", tc.name)
			}
			msg := err.Error()
			if !strings.Contains(strings.ToLower(msg), "remote mode") {
				t.Fatalf("%s: error %q does not mention remote mode", tc.name, msg)
			}
		})
	}
}

// TestDocFromPathRejectsRemote covers the inline `--from-path` gate inside
// `bacio doc add` and `bacio doc upsert` — both paths short-circuit when the
// flag is set in remote mode because the API can't read the client's
// filesystem. We drive the doc subcommand via cobra's Execute so the flag
// parser actually fires.
func TestDocFromPathRejectsRemote(t *testing.T) {
	saved := opts
	t.Cleanup(func() { opts = saved })
	opts.remote = ""
	t.Setenv("BACIO_REMOTE", "http://example.invalid")

	for _, verb := range []string{"add", "upsert"} {
		t.Run(verb, func(t *testing.T) {
			docCmd := newDocCmd()
			docCmd.SetArgs([]string{verb, "--from-path", "/dev/null"})
			docCmd.SilenceUsage = true
			docCmd.SilenceErrors = true
			err := docCmd.Execute()
			if err == nil {
				t.Fatalf("expected --from-path to be rejected in remote mode")
			}
			if !strings.Contains(err.Error(), "remote mode") {
				t.Fatalf("error %q does not mention remote mode", err.Error())
			}
		})
	}
}

// TestDocExportRejectsRemote covers the export verb's inline gate.
func TestDocExportRejectsRemote(t *testing.T) {
	saved := opts
	t.Cleanup(func() { opts = saved })
	opts.remote = ""
	t.Setenv("BACIO_REMOTE", "http://example.invalid")

	docCmd := newDocCmd()
	docCmd.SetArgs([]string{"export", "design.md", "--to-path"})
	docCmd.SilenceUsage = true
	docCmd.SilenceErrors = true
	err := docCmd.Execute()
	if err == nil {
		t.Fatalf("expected bacio doc export to be rejected in remote mode")
	}
	if !strings.Contains(err.Error(), "remote mode") {
		t.Fatalf("error %q does not mention remote mode", err.Error())
	}
}
