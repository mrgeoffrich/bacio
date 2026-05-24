package cli

import (
	"context"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/git"
	"github.com/mrgeoffrich/bacio/internal/model"
)

// TestResolveActiveDispatchID covers BACI-132's hook helper: it
// picks the newest non-cancelled dispatch targeting (session,
// issue), returns nil when none matches, and ignores dispatches
// targeting other issues. Mirrors boardcards.pickActiveDispatch.
func TestResolveActiveDispatchID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	ctx := context.Background()
	c, err := client.Open(ctx, client.Options{DBPath: ":memory:", Actor: "tester"})
	if err != nil {
		t.Fatalf("open client: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	repoRoot := t.TempDir()
	repo, _, err := c.EnsureRepo(ctx, &git.Info{Root: repoRoot, Name: "proj"})
	if err != nil {
		t.Fatalf("ensure repo: %v", err)
	}
	iss, err := c.CreateIssue(ctx, repo, inputs.IssueAddInput{Title: "Test"}, false)
	if err != nil {
		t.Fatalf("create issue: %v", err)
	}
	other, err := c.CreateIssue(ctx, repo, inputs.IssueAddInput{Title: "Other"}, false)
	if err != nil {
		t.Fatalf("create other issue: %v", err)
	}

	sess, err := c.RegisterAgent(ctx, repo, inputs.AgentRegisterInput{
		SessionID: "f4cef4ce-f4ce-f4ce-f4ce-f4cef4cef4ce",
		Actor:     "tester",
	}, false)
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	t.Run("no dispatch returns nil", func(t *testing.T) {
		got := resolveActiveDispatchID(ctx, c, sess.SessionID, iss.Key)
		if got != nil {
			t.Fatalf("expected nil, got %v", got)
		}
	})

	d1, err := c.CreateDispatch(ctx, repo, inputs.AgentDispatchInput{
		IssueKey:      iss.Key,
		TargetSession: sess.SessionID,
		Mode:          "plan",
		Message:       "stub",
	}, false)
	if err != nil {
		t.Fatalf("create d1: %v", err)
	}

	t.Run("single non-cancelled dispatch wins", func(t *testing.T) {
		got := resolveActiveDispatchID(ctx, c, sess.SessionID, iss.Key)
		if got == nil || *got != d1.ID {
			t.Fatalf("got %v, want %d", got, d1.ID)
		}
	})

	// Cancel the only matching dispatch — the helper now returns nil.
	if _, err := c.CancelDispatch(ctx, inputs.AgentCancelInput{ID: d1.ID}, false); err != nil {
		t.Fatalf("cancel d1: %v", err)
	}

	t.Run("cancelled dispatches are ignored", func(t *testing.T) {
		got := resolveActiveDispatchID(ctx, c, sess.SessionID, iss.Key)
		if got != nil {
			t.Fatalf("expected nil after cancel, got %v", got)
		}
	})

	d2, err := c.CreateDispatch(ctx, repo, inputs.AgentDispatchInput{
		IssueKey:      iss.Key,
		TargetSession: sess.SessionID,
		Mode:          "implement",
		Message:       "stub",
	}, false)
	if err != nil {
		t.Fatalf("create d2: %v", err)
	}
	d3, err := c.CreateDispatch(ctx, repo, inputs.AgentDispatchInput{
		IssueKey:      iss.Key,
		TargetSession: sess.SessionID,
		Mode:          "review",
		Message:       "stub",
	}, false)
	if err != nil {
		t.Fatalf("create d3: %v", err)
	}

	t.Run("newest non-cancelled wins", func(t *testing.T) {
		got := resolveActiveDispatchID(ctx, c, sess.SessionID, iss.Key)
		if got == nil || *got != d3.ID {
			t.Fatalf("got %v, want d3.ID=%d (d2.ID=%d)", got, d3.ID, d2.ID)
		}
	})

	t.Run("dispatch on a different issue is ignored", func(t *testing.T) {
		got := resolveActiveDispatchID(ctx, c, sess.SessionID, other.Key)
		if got != nil {
			t.Fatalf("expected nil for unrelated issue, got %v", got)
		}
	})

	t.Run("empty session id returns nil", func(t *testing.T) {
		got := resolveActiveDispatchID(ctx, c, "", iss.Key)
		if got != nil {
			t.Fatalf("expected nil for empty session_id, got %v", got)
		}
	})

	t.Run("empty issue key returns nil", func(t *testing.T) {
		got := resolveActiveDispatchID(ctx, c, sess.SessionID, "")
		if got != nil {
			t.Fatalf("expected nil for empty issue_key, got %v", got)
		}
	})

	// Suppress unused warning — model is imported transitively.
	_ = model.DispatchPending
}
