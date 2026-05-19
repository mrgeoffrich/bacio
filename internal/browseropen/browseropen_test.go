package browseropen

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

func TestCommandFor(t *testing.T) {
	// commandFor branches on runtime.GOOS at call time. Cover the
	// branch matching the test runner's GOOS explicitly; the unsupported
	// fallback is exercised in a separate test via a small refactor
	// avoidance — we trust the switch reads correctly otherwise.
	url := "http://127.0.0.1:5320/ui/"
	bin, args, err := commandFor(url)
	switch runtime.GOOS {
	case "darwin":
		if err != nil {
			t.Fatalf("darwin: unexpected error: %v", err)
		}
		if bin != "open" {
			t.Fatalf("darwin: want bin=open, got %q", bin)
		}
		if len(args) != 1 || args[0] != url {
			t.Fatalf("darwin: want args=[url], got %v", args)
		}
	case "windows":
		if err != nil {
			t.Fatalf("windows: unexpected error: %v", err)
		}
		if bin != "rundll32" {
			t.Fatalf("windows: want bin=rundll32, got %q", bin)
		}
		if len(args) != 2 || args[0] != "url.dll,FileProtocolHandler" || args[1] != url {
			t.Fatalf("windows: want args=[url.dll,FileProtocolHandler url], got %v", args)
		}
	case "linux", "freebsd", "openbsd", "netbsd":
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", runtime.GOOS, err)
		}
		if bin != "xdg-open" {
			t.Fatalf("%s: want bin=xdg-open, got %q", runtime.GOOS, bin)
		}
		if len(args) != 1 || args[0] != url {
			t.Fatalf("%s: want args=[url], got %v", runtime.GOOS, args)
		}
	default:
		// The fallback path returns an error; just assert that shape.
		if err == nil {
			t.Fatalf("expected error on unsupported GOOS %q", runtime.GOOS)
		}
	}
}

// TestOpen_RunnerInvoked confirms the seam runner receives a fully
// composed *exec.Cmd whose Path matches the GOOS-specific launcher.
// The default runner is swapped out so no browser is actually spawned.
func TestOpen_RunnerInvoked(t *testing.T) {
	prev := runner
	t.Cleanup(func() { runner = prev })

	var captured *exec.Cmd
	runner = func(c *exec.Cmd) error {
		captured = c
		return nil
	}

	url := "http://127.0.0.1:5411/ui/"
	if err := Open(context.Background(), url); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if captured == nil {
		t.Fatalf("runner not invoked")
	}

	// Path's basename should match the per-GOOS launcher.
	wantBin, _, _ := commandFor(url)
	if wantBin == "" {
		t.Skipf("commandFor returns no launcher on GOOS=%s; nothing more to assert", runtime.GOOS)
	}
	// exec.Command resolves the binary via LookPath, so captured.Path
	// may be an absolute path. Compare the trailing segment.
	if !strings.HasSuffix(captured.Path, wantBin) && captured.Path != wantBin {
		t.Fatalf("captured.Path: want suffix=%q, got %q", wantBin, captured.Path)
	}

	// Last argv element is always the URL (it's the only positional
	// across all three launchers).
	if got := captured.Args[len(captured.Args)-1]; got != url {
		t.Fatalf("captured.Args last: want %q, got %q", url, got)
	}
}

// TestOpen_RunnerErrorBubbles confirms a launch failure surfaces to
// the caller verbatim instead of being swallowed.
func TestOpen_RunnerErrorBubbles(t *testing.T) {
	prev := runner
	t.Cleanup(func() { runner = prev })

	want := errors.New("simulated launcher failure")
	runner = func(*exec.Cmd) error { return want }

	got := Open(context.Background(), "http://example.invalid/")
	if !errors.Is(got, want) {
		t.Fatalf("Open: want %v, got %v", want, got)
	}
}
