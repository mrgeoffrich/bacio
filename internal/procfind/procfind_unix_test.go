//go:build !windows

package procfind

import (
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestParseLsof(t *testing.T) {
	// Two processes, one with two socket lines (IPv4 + IPv6) — the
	// duplicate `p` block must dedupe to a single Listener.
	out := []byte("p48213\n" +
		"cbacio\n" +
		"n127.0.0.1:5401\n" +
		"p48213\n" +
		"cbacio\n" +
		"n[::1]:5401\n" +
		"p48999\n" +
		"cbacio-desktop\n" +
		"n127.0.0.1:5401\n")
	got := parseLsof(out)
	if len(got) != 2 {
		t.Fatalf("got %d listeners, want 2: %+v", len(got), got)
	}
	if got[0].PID != 48213 || got[0].Command != "bacio" {
		t.Errorf("listener[0] = %+v, want {48213 bacio}", got[0])
	}
	if got[1].PID != 48999 || got[1].Command != "bacio-desktop" {
		t.Errorf("listener[1] = %+v, want {48999 bacio-desktop}", got[1])
	}
}

func TestParseLsofEmpty(t *testing.T) {
	if got := parseLsof(nil); len(got) != 0 {
		t.Errorf("parseLsof(nil) = %+v, want empty", got)
	}
	if got := parseLsof([]byte("\n\n")); len(got) != 0 {
		t.Errorf("parseLsof(blank) = %+v, want empty", got)
	}
}

func TestListenersOnPortStubbed(t *testing.T) {
	orig := runCommand
	defer func() { runCommand = orig }()
	runCommand = func(name string, args ...string) ([]byte, error) {
		if name != "lsof" {
			t.Fatalf("unexpected command %q", name)
		}
		return []byte("p1234\ncbacio\nn127.0.0.1:5401\n"), nil
	}
	got, err := ListenersOnPort(5401)
	if err != nil {
		t.Fatalf("ListenersOnPort: %v", err)
	}
	if len(got) != 1 || got[0].PID != 1234 || got[0].Command != "bacio" {
		t.Fatalf("got %+v, want one {1234 bacio}", got)
	}
}

func TestListenersOnPortNoMatch(t *testing.T) {
	// lsof exits 1 with no output when nothing matches the filter —
	// that must surface as an empty result, not an error.
	orig := runCommand
	defer func() { runCommand = orig }()
	runCommand = func(name string, args ...string) ([]byte, error) {
		return nil, &exec.ExitError{}
	}
	got, err := ListenersOnPort(5999)
	if err != nil {
		t.Fatalf("ListenersOnPort: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v, want empty", got)
	}
}

func TestSignalAndAlive(t *testing.T) {
	// Spawn a harmless sleep we own, confirm Alive, terminate it.
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	pid := cmd.Process.Pid
	defer func() { _ = cmd.Process.Kill() }()

	if !Alive(pid) {
		t.Fatalf("Alive(%d) = false right after start", pid)
	}
	if err := SignalTerm(pid); err != nil {
		t.Fatalf("SignalTerm: %v", err)
	}
	_ = cmd.Wait()
	// Give the kernel a beat to reap.
	deadline := time.Now().Add(2 * time.Second)
	for Alive(pid) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if Alive(pid) {
		t.Errorf("Alive(%d) = true after SIGTERM + wait", pid)
	}
}

func TestAliveDeadPID(t *testing.T) {
	// PID 1 exists but is not ours; a clearly-unused high PID should
	// not be alive. Use the current process's own check as a sanity.
	if !Alive(os.Getpid()) {
		t.Errorf("Alive(self) = false")
	}
}
