package cli

import (
	"context"
	"os"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/git"
)

// TestChannelRegisterReusesAgentsJSONSlug pins the /clear-survives-rename
// fix (BACI-44): when the claude_pid this channel serves already has an
// entry in .bacio/agents.json, channelSource.Register must reuse that
// slug rather than letting CompleteRegistration mint a fresh one. Before
// the fix, /clear would correctly end the previous session but the new
// session for the same claude_pid was registered under a brand-new
// identity, orphaning the original slug.
func TestChannelRegisterReusesAgentsJSONSlug(t *testing.T) {
	const existingSlug = "lively-kestrel@claude.testhost"
	const host = "testhost"
	// BACI-100: register requires a structurally valid UUID session_id.
	const newSessionID = "5f8c2a10-3b4d-4e6f-8a9b-0c1d2e3f4a5b"
	// agents.json save() calls pruneDead() which drops entries whose pid
	// isn't in the live process table. Use this test process's own pid
	// so the seeded entry survives the save.
	claudePID := int64(os.Getpid())

	repoRoot := t.TempDir()

	ctx := context.Background()
	c, err := client.Open(ctx, client.Options{DBPath: ":memory:", Actor: "tester"})
	if err != nil {
		t.Fatalf("open client: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	repo, _, err := c.EnsureRepo(ctx, &git.Info{Root: repoRoot, Name: "testrepo"})
	if err != nil {
		t.Fatalf("ensure repo: %v", err)
	}

	// Seed agents.json the way a prior CompleteRegistration would have:
	// this claude_pid is already known to be `existingSlug`. The fix is
	// to make Register honour this — without it, the new register call
	// mints a fresh slug and clobbers the file.
	if err := recordAgentSession(repoRoot, int(claudePID), host, existingSlug, "pre-clear-session-id"); err != nil {
		t.Fatalf("seed agents.json: %v", err)
	}

	src := &channelSource{
		c:          c,
		repo:       repo,
		repoRoot:   repoRoot,
		host:       host,
		claudePID:  claudePID,
		channelPID: int64(99999),
		pushed:     map[int64]bool{},
	}

	// New post-/clear session id, just like Claude Code would mint.
	if err := src.Register(ctx, newSessionID, "claude-opus-4-7"); err != nil {
		t.Fatalf("Register: %v", err)
	}

	view, err := c.ShowAgentSession(ctx, newSessionID)
	if err != nil {
		t.Fatalf("show registered session: %v", err)
	}
	sess := view.Session
	if sess.AgentName != existingSlug {
		t.Fatalf("post-/clear session adopted wrong identity: got %q, want %q (a fresh mint means the agents.json hint was ignored — BACI-44 regression)", sess.AgentName, existingSlug)
	}
	if sess.RegisteredAt == nil {
		t.Fatalf("expected registered_at to be set after Register; got NULL")
	}
}

// TestChannelRegisterMintsWhenNoHint covers the orthogonal case: a
// claude_pid with no agents.json entry (fresh process, first register
// ever) still mints a brand-new identity. Without this guard, the
// BACI-44 fix could regress to "Register requires a pre-existing hint".
func TestChannelRegisterMintsWhenNoHint(t *testing.T) {
	const host = "testhost"
	// BACI-100: register requires a structurally valid UUID session_id.
	const newSessionID = "6a9d3b21-4c5e-4f70-9bac-1d2e3f4a5b6c"
	// Live pid for symmetry with the sibling test, though no agents.json
	// entry is seeded here.
	claudePID := int64(os.Getpid())

	repoRoot := t.TempDir()

	ctx := context.Background()
	c, err := client.Open(ctx, client.Options{DBPath: ":memory:", Actor: "tester"})
	if err != nil {
		t.Fatalf("open client: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	repo, _, err := c.EnsureRepo(ctx, &git.Info{Root: repoRoot, Name: "testrepo"})
	if err != nil {
		t.Fatalf("ensure repo: %v", err)
	}

	// No agents.json seed — recordAgentSession is deliberately NOT called.

	src := &channelSource{
		c:          c,
		repo:       repo,
		repoRoot:   repoRoot,
		host:       host,
		claudePID:  claudePID,
		channelPID: int64(99999),
		pushed:     map[int64]bool{},
	}

	if err := src.Register(ctx, newSessionID, "claude-opus-4-7"); err != nil {
		t.Fatalf("Register: %v", err)
	}

	view, err := c.ShowAgentSession(ctx, newSessionID)
	if err != nil {
		t.Fatalf("show registered session: %v", err)
	}
	sess := view.Session
	if sess.AgentName == "" {
		t.Fatalf("expected a freshly-minted agent name; got empty (Register failed to mint without a hint)")
	}
}
