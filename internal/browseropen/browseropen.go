// Package browseropen launches the user's OS-default browser at a given
// URL. The helper is intentionally tiny: bacio prizes its pure-Go /
// no-CGO / no-third-party-deps footprint, so the ~25-line
// implementation here replaces the equivalent github.com/pkg/browser
// dependency. Best-effort by design — callers should log any returned
// error and keep going; the browser launch is a UX nicety, not a
// correctness requirement.
package browseropen

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
)

// runner is the seam tests stub to capture the would-be exec call
// without actually spawning anything. The default implementation
// starts the process (and crucially does NOT Wait, so the browser
// outlives the bacio shim).
var runner = func(c *exec.Cmd) error { return c.Start() }

// Open attempts to open url in the OS-default browser. The context
// bounds the launch shell-out itself (kills a hung 'open' / 'xdg-open');
// the spawned browser process is detached and is not killed when the
// context cancels.
//
// Returns an error the caller can log; never panics. An unsupported
// GOOS surfaces as an error rather than a panic so headless / niche
// platforms degrade gracefully.
func Open(ctx context.Context, url string) error {
	cmd, args, err := commandFor(url)
	if err != nil {
		return err
	}
	// Use CommandContext so a hung launcher is killable, but do NOT
	// Wait — the browser process should outlive the shim.
	c := exec.CommandContext(ctx, cmd, args...)
	return runner(c)
}

// commandFor returns the (binary, args) pair the current OS uses to
// open a URL in the default browser. Split out so the per-GOOS
// branching is testable without actually invoking exec.
func commandFor(url string) (string, []string, error) {
	switch runtime.GOOS {
	case "darwin":
		return "open", []string{url}, nil
	case "windows":
		// rundll32 is more reliable than `cmd /c start` (no quoting
		// traps when the URL contains '&' or spaces).
		return "rundll32", []string{"url.dll,FileProtocolHandler", url}, nil
	case "linux", "freebsd", "openbsd", "netbsd":
		return "xdg-open", []string{url}, nil
	default:
		return "", nil, fmt.Errorf("unsupported GOOS %q", runtime.GOOS)
	}
}
