package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// TestRenderProxyServiceUnit_Darwin pins the launchd plist: it invokes the
// absolute bacio binary with `proxy serve`, lands at the canonical
// LaunchAgents path, and carries the RunAtLoad / KeepAlive keys that make
// it a never-touch keep-alive.
func TestRenderProxyServiceUnit_Darwin(t *testing.T) {
	const exe = "/opt/bacio/bin/bacio"
	home := "/Users/test"
	unit, err := renderProxyServiceUnit("darwin", exe, home)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	wantPath := filepath.Join(home, "Library", "LaunchAgents", "io.bacio.proxy.plist")
	if unit.Path != wantPath {
		t.Errorf("path = %q, want %q", unit.Path, wantPath)
	}
	for _, must := range []string{exe, "<string>proxy</string>", "<string>serve</string>", "io.bacio.proxy", "RunAtLoad", "KeepAlive"} {
		if !strings.Contains(unit.Body, must) {
			t.Errorf("plist body missing %q:\n%s", must, unit.Body)
		}
	}
	if unit.loadDesc != "launchctl load "+wantPath {
		t.Errorf("loadDesc = %q", unit.loadDesc)
	}
}

// TestRenderProxyServiceUnit_Linux pins the systemd user unit: it ExecStarts
// the absolute bacio binary with `proxy serve`, lands at the canonical
// per-user unit path, and restarts on failure.
func TestRenderProxyServiceUnit_Linux(t *testing.T) {
	const exe = "/usr/local/bin/bacio"
	home := "/home/test"
	unit, err := renderProxyServiceUnit("linux", exe, home)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	wantPath := filepath.Join(home, ".config", "systemd", "user", "bacio-proxy.service")
	if unit.Path != wantPath {
		t.Errorf("path = %q, want %q", unit.Path, wantPath)
	}
	for _, must := range []string{"ExecStart=" + exe + " proxy serve", "Restart=on-failure", "WantedBy=default.target"} {
		if !strings.Contains(unit.Body, must) {
			t.Errorf("unit body missing %q:\n%s", must, unit.Body)
		}
	}
}

// TestRenderProxyServiceUnit_UnsupportedGOOS asserts a clear error on a GOOS
// with no keep-alive automation (Windows) rather than a half-written unit.
func TestRenderProxyServiceUnit_UnsupportedGOOS(t *testing.T) {
	_, err := renderProxyServiceUnit("windows", "C:/bacio.exe", "C:/Users/test")
	if err == nil {
		t.Fatal("expected an error for windows, got nil")
	}
	if !strings.Contains(err.Error(), "windows") {
		t.Errorf("error %q should name the unsupported GOOS", err)
	}
}

// TestProxyInstallServiceCmd_DryRunRendersUnit asserts --dry-run prints the
// rendered unit + target path and writes nothing to disk. The rendering is
// the current host's GOOS; we assert the projection mentions the binary and
// "would write" without touching the real LaunchAgents / systemd path.
func TestProxyInstallServiceCmd_DryRunRendersUnit(t *testing.T) {
	cmd := newProxyInstallServiceCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--dry-run"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "would write") {
		t.Errorf("dry-run output missing the 'would write' projection:\n%s", got)
	}
	// The invocation renders differently per GOOS — `<string>serve</string>`
	// in a launchd plist, `proxy serve` in a systemd ExecStart — so assert
	// the `serve` token rather than a fixed substring.
	if !strings.Contains(got, "serve") {
		t.Errorf("dry-run output should reference the `proxy serve` invocation:\n%s", got)
	}
	if !strings.Contains(got, "would load it with") {
		t.Errorf("dry-run output missing the loader projection:\n%s", got)
	}
}

// TestProxyInstallServiceCmd_RegisteredUnderProxy guards the wiring.
func TestProxyInstallServiceCmd_RegisteredUnderProxy(t *testing.T) {
	parent := newProxyCmd()
	sub, _, err := parent.Find([]string{"install-service"})
	if err != nil {
		t.Fatalf("find proxy install-service: %v", err)
	}
	if sub == nil || sub.Name() != "install-service" {
		t.Fatalf("proxy install-service not registered under `bacio proxy`; got %v", sub)
	}
}
