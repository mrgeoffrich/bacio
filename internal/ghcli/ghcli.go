// Package ghcli wraps the small slice of the GitHub `gh` CLI that
// `bacio pr create` (BACI-163) needs: list PRs by label, idempotently
// create/update a label, and open a PR. Keeping the shell-out behind a
// small interface gives us a single seam to mock in tests and a single
// place to translate the assorted `gh` failure modes into typed errors.
//
// We deliberately shell out to `gh` rather than depend on a third-party
// GitHub-API library: bacio prizes its pure-Go / no-CGO / no-third-party
// footprint, and `gh` is already the auth-and-context-aware client most
// bacio users already have on PATH.
package ghcli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// PRSummary mirrors the `gh pr list --json number,state,url` row shape.
// State values are the upstream `gh` strings: "OPEN", "CLOSED", "MERGED".
type PRSummary struct {
	Number int    `json:"number"`
	State  string `json:"state"`
	URL    string `json:"url"`
}

// PRState constants for callers that prefer named comparisons over raw
// string literals. The values match `gh`'s casing exactly so a `==`
// comparison against the JSON-decoded State field works.
const (
	StateOpen   = "OPEN"
	StateClosed = "CLOSED"
	StateMerged = "MERGED"
)

// Client is the slice of `gh` functionality `bacio pr create` needs.
// Tests inject a fake to drive the wrapper's decision matrix without
// touching the network.
type Client interface {
	// ListPRsByLabel returns every PR carrying the given label, regardless
	// of state — the caller filters by State. An unset / missing label
	// returns an empty slice (not an error).
	ListPRsByLabel(ctx context.Context, label string) ([]PRSummary, error)

	// CreateLabelIdempotent creates the label if it doesn't exist, or
	// updates its colour/description if it does. Backed by
	// `gh label create --force`, which is exactly this semantic.
	CreateLabelIdempotent(ctx context.Context, name, color string) error

	// CreatePR opens a new PR carrying the given label, forwarding extra
	// args verbatim to `gh pr create`. Returns the new PR's URL (the last
	// https:// line of `gh pr create`'s stdout — `gh` prints the URL).
	CreatePR(ctx context.Context, label string, extra []string) (url string, err error)
}

// ErrGHMissing is returned when `gh` isn't on PATH.
var ErrGHMissing = errors.New("the gh CLI is not on PATH; install it from https://cli.github.com and re-run")

// ErrGHUnauthenticated is returned when `gh` is on PATH but no auth
// token is configured for the host this repo targets. The wrapper
// detects this via the `gh auth status`-style error text on stderr
// rather than a separate probe call — fewer round trips.
var ErrGHUnauthenticated = errors.New("gh is installed but not authenticated; run `gh auth login` and re-run")

// runner is the seam tests override to capture exec calls. The default
// implementation runs `gh` with the supplied args and returns the
// (stdout, stderr, error) tuple. Keeping the runner separate from the
// methods means the JSON parsing logic stays independently testable.
type runner func(ctx context.Context, args ...string) (stdout, stderr string, err error)

// Exec is the production Client backed by an actual `gh` binary on PATH.
type Exec struct{ run runner }

// NewExec returns a Client that shells out to `gh`.
func NewExec() Client {
	return &Exec{run: defaultRunner}
}

// newExecWithRunner is the test seam: a Client with a stubbed runner.
// Kept package-private so external callers always go through NewExec.
func newExecWithRunner(r runner) *Exec { return &Exec{run: r} }

// defaultRunner shells out to `gh` and translates the two
// authentication / missing-binary failure modes into typed errors.
func defaultRunner(ctx context.Context, args ...string) (string, string, error) {
	cmd := exec.CommandContext(ctx, "gh", args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		// exec.LookPath-style miss surfaces here as *exec.Error.
		if errors.Is(err, exec.ErrNotFound) {
			return "", "", ErrGHMissing
		}
		// On macOS / Linux a missing binary surfaces as ENOENT, which
		// shows up here as an *exec.Error wrapping a *fs.PathError. The
		// errors.Is above usually catches it; the string check is a
		// belt-and-braces for older Go versions.
		if strings.Contains(err.Error(), "executable file not found") {
			return "", "", ErrGHMissing
		}
		errOut := stderr.String()
		if isUnauthenticated(errOut) {
			return stdout.String(), errOut, ErrGHUnauthenticated
		}
		return stdout.String(), errOut, fmt.Errorf("gh %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errOut))
	}
	return stdout.String(), stderr.String(), nil
}

// isUnauthenticated heuristically classifies a `gh` stderr blob as a
// missing-auth failure. `gh`'s exact wording varies between versions
// but the substrings below have been stable for years.
func isUnauthenticated(stderr string) bool {
	low := strings.ToLower(stderr)
	switch {
	case strings.Contains(low, "not logged into"):
		return true
	case strings.Contains(low, "authentication required"):
		return true
	case strings.Contains(low, "gh auth login"):
		return true
	}
	return false
}

// ListPRsByLabel runs `gh pr list --label <label> --state all --json
// number,state,url` and parses the JSON output. An empty result is a
// nil-or-empty slice, not an error.
func (e *Exec) ListPRsByLabel(ctx context.Context, label string) ([]PRSummary, error) {
	if strings.TrimSpace(label) == "" {
		return nil, fmt.Errorf("ghcli: label is required")
	}
	stdout, _, err := e.run(ctx, "pr", "list", "--label", label, "--state", "all", "--json", "number,state,url")
	if err != nil {
		return nil, err
	}
	stdout = strings.TrimSpace(stdout)
	if stdout == "" {
		return nil, nil
	}
	var rows []PRSummary
	if err := json.Unmarshal([]byte(stdout), &rows); err != nil {
		return nil, fmt.Errorf("ghcli: parse pr list JSON: %w (stdout=%q)", err, stdout)
	}
	return rows, nil
}

// CreateLabelIdempotent runs `gh label create <name> --color <color>
// --force`. `--force` makes the call a no-op (colour update at most)
// when the label already exists.
func (e *Exec) CreateLabelIdempotent(ctx context.Context, name, color string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("ghcli: label name is required")
	}
	if strings.TrimSpace(color) == "" {
		return fmt.Errorf("ghcli: label color is required")
	}
	_, _, err := e.run(ctx, "label", "create", name, "--color", color, "--force")
	return err
}

// CreatePR runs `gh pr create --label <label> <extra...>` and returns
// the new PR's URL by extracting the last HTTPS line of stdout. `gh
// pr create` prints the PR URL on the final stdout line on success.
func (e *Exec) CreatePR(ctx context.Context, label string, extra []string) (string, error) {
	if strings.TrimSpace(label) == "" {
		return "", fmt.Errorf("ghcli: label is required")
	}
	args := append([]string{"pr", "create", "--label", label}, extra...)
	stdout, _, err := e.run(ctx, args...)
	if err != nil {
		return "", err
	}
	url := extractPRURL(stdout)
	if url == "" {
		return "", fmt.Errorf("ghcli: could not find PR URL in `gh pr create` output: %q", strings.TrimSpace(stdout))
	}
	return url, nil
}

// extractPRURL pulls the last "https://..." line out of `gh pr create`
// stdout. `gh` prints assorted status lines first ("Creating draft PR
// for…", "remote: …") and the PR URL on the final line.
func extractPRURL(stdout string) string {
	var last string
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "https://") {
			last = line
		}
	}
	return last
}
