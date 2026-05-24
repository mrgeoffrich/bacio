package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPathWithin pins the boundary-safe containment check. The
// sibling-prefix case is the one a plain strings.HasPrefix gets wrong —
// `…/agent-abc-evil` is NOT within `…/agent-abc`.
func TestPathWithin(t *testing.T) {
	sep := string(os.PathSeparator)
	root := sep + filepath.Join("repo", ".claude", "worktrees", "agent-abc")
	cases := []struct {
		name   string
		root   string
		target string
		want   bool
	}{
		{"under-root", root, filepath.Join(root, "internal", "cli", "hook.go"), true},
		{"equal-to-root", root, root, true},
		{"sibling-prefix", root, root + "-evil" + sep + "file.go", false},
		{"parent-dir", root, sep + filepath.Join("repo", "internal", "sync", "engine.go"), false},
		{"unrelated", root, sep + filepath.Join("other", "place", "x.go"), false},
		{"empty-root", "", filepath.Join(root, "x.go"), false},
		{"empty-target", root, "", false},
		{"trailing-slash-root", root + sep, filepath.Join(root, "x.go"), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := pathWithin(c.root, c.target); got != c.want {
				t.Fatalf("pathWithin(%q, %q) = %v, want %v", c.root, c.target, got, c.want)
			}
		})
	}
}

// TestDecidePreToolUseInsideWorktree: an edit to a file under the
// resolved linked-worktree root is allowed.
func TestDecidePreToolUseInsideWorktree(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "internal", "cli", "hook.go")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	in := &preToolUseInput{ToolName: "Edit", CWD: root}
	in.ToolInput.FilePath = target

	d := decidePreToolUse(in, func(cwd string) (string, bool) { return root, true })
	if !d.allow {
		t.Fatalf("expected allow for in-worktree edit, got deny: %s", d.reason)
	}
}

// TestDecidePreToolUseOutsideWorktree: an edit addressing a parent-repo
// absolute path is denied, and the reason names the linked-worktree
// root — the BACI-116 regression case, reworded in BACI-129.
func TestDecidePreToolUseOutsideWorktree(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, ".claude", "worktrees", "agent-abc")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// The escape: a file_path under the parent checkout, while the
	// worker stands inside the dispatch worktree.
	escape := filepath.Join(parent, "internal", "sync", "engine.go")
	if err := os.MkdirAll(filepath.Dir(escape), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	in := &preToolUseInput{ToolName: "Write", CWD: root}
	in.ToolInput.FilePath = escape

	d := decidePreToolUse(in, func(cwd string) (string, bool) { return root, true })
	if d.allow {
		t.Fatalf("expected deny for parent-repo edit, got allow")
	}
	resolvedRoot := evalSymlinksLenient(root)
	if !strings.Contains(d.reason, resolvedRoot) {
		t.Fatalf("deny reason should name the worktree root %q; got: %s", resolvedRoot, d.reason)
	}
	if !strings.Contains(d.reason, "linked git worktree") {
		t.Fatalf("deny reason should mention 'linked git worktree' wording; got: %s", d.reason)
	}
}

// TestDecidePreToolUseSiblingWorktree: an edit into a sibling worktree
// whose name shares a prefix with the confined root is denied — the
// boundary-safety case at the decision level.
func TestDecidePreToolUseSiblingWorktree(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "agent-abc")
	sibling := filepath.Join(parent, "agent-abc-evil")
	for _, d := range []string{root, sibling} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	in := &preToolUseInput{ToolName: "Edit", CWD: root}
	in.ToolInput.FilePath = filepath.Join(sibling, "file.go")

	d := decidePreToolUse(in, func(cwd string) (string, bool) { return root, true })
	if d.allow {
		t.Fatalf("expected deny for sibling-worktree edit, got allow")
	}
}

// TestDecidePreToolUseNoGitRepo: cwd is not inside any git repository
// (resolver returns "", false) — confinement does not engage, the
// edit is allowed wherever it points. BACI-129 dropped the
// no-manifest carve-out: the carve-out is now "no git repo" instead.
func TestDecidePreToolUseNoGitRepo(t *testing.T) {
	in := &preToolUseInput{ToolName: "Write", CWD: t.TempDir()}
	in.ToolInput.FilePath = "/anywhere/at/all.go"

	d := decidePreToolUse(in, func(cwd string) (string, bool) { return "", false })
	if !d.allow {
		t.Fatalf("expected allow when cwd is outside any git repo, got deny: %s", d.reason)
	}
}

// TestDecidePreToolUsePrimaryWorktreeDenied: cwd is inside the *primary*
// worktree of a git repo (resolver returns root, linked=false) — the
// BACI-129 case. Every Write/Edit is denied regardless of where
// file_path points, and the deny reason names the primary root so the
// model is told to move to a linked worktree.
func TestDecidePreToolUsePrimaryWorktreeDenied(t *testing.T) {
	primary := t.TempDir()
	target := filepath.Join(primary, "internal", "cli", "hook.go")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	in := &preToolUseInput{ToolName: "Edit", CWD: primary}
	in.ToolInput.FilePath = target

	d := decidePreToolUse(in, func(cwd string) (string, bool) { return primary, false })
	if d.allow {
		t.Fatalf("expected deny for primary-worktree edit, got allow")
	}
	resolvedPrimary := evalSymlinksLenient(primary)
	if !strings.Contains(d.reason, resolvedPrimary) {
		t.Fatalf("deny reason should name the primary root %q; got: %s", resolvedPrimary, d.reason)
	}
	if !strings.Contains(d.reason, "primary worktree") {
		t.Fatalf("deny reason should mention 'primary worktree' wording; got: %s", d.reason)
	}
}

// TestDecidePreToolUseLinkedWorktreeAllowed: cwd is inside a linked
// worktree (resolver returns root, linked=true) and the edit lands
// under that root — allowed. This is the dispatched-worker happy path.
func TestDecidePreToolUseLinkedWorktreeAllowed(t *testing.T) {
	linked := t.TempDir()
	target := filepath.Join(linked, "internal", "cli", "hook.go")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	in := &preToolUseInput{ToolName: "Edit", CWD: linked}
	in.ToolInput.FilePath = target

	d := decidePreToolUse(in, func(cwd string) (string, bool) { return linked, true })
	if !d.allow {
		t.Fatalf("expected allow for linked-worktree edit, got deny: %s", d.reason)
	}
}

// TestDecidePreToolUseNonWriteTool: a tool other than Write/Edit is
// always allowed — the matcher should never deliver one, but the guard
// defends against a matcher widening.
func TestDecidePreToolUseNonWriteTool(t *testing.T) {
	in := &preToolUseInput{ToolName: "Bash", CWD: t.TempDir()}
	in.ToolInput.FilePath = "/anywhere/at/all.go"

	d := decidePreToolUse(in, func(cwd string) (string, bool) {
		t.Fatalf("resolver must not be consulted for a non-Write/Edit tool")
		return "", false
	})
	if !d.allow {
		t.Fatalf("expected allow for non-Write/Edit tool, got deny")
	}
}

// TestDecidePreToolUseEmptyFilePath: a Write/Edit with no file_path is
// allowed — the guard has nothing to confine and must not deny.
func TestDecidePreToolUseEmptyFilePath(t *testing.T) {
	in := &preToolUseInput{ToolName: "Edit", CWD: t.TempDir()}
	in.ToolInput.FilePath = "  "

	d := decidePreToolUse(in, func(cwd string) (string, bool) {
		t.Fatalf("resolver must not be consulted when file_path is empty")
		return "", false
	})
	if !d.allow {
		t.Fatalf("expected allow for empty file_path, got deny")
	}
}

// TestDecidePreToolUseSymlinkEscape: a symlink inside the worktree
// pointing at the parent checkout cannot be used to slip the guard.
func TestDecidePreToolUseSymlinkEscape(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "agent-abc")
	outside := filepath.Join(parent, "main-checkout")
	for _, d := range []string{root, outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	// A symlink inside the worktree that resolves to the parent.
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	in := &preToolUseInput{ToolName: "Write", CWD: root}
	in.ToolInput.FilePath = filepath.Join(link, "engine.go")

	d := decidePreToolUse(in, func(cwd string) (string, bool) { return root, true })
	if d.allow {
		t.Fatalf("expected deny for symlink-escape edit, got allow")
	}
}

// TestDecidePreToolUseRelativeFilePath: a relative file_path is
// resolved against cwd before the containment check.
func TestDecidePreToolUseRelativeFilePath(t *testing.T) {
	root := t.TempDir()
	in := &preToolUseInput{ToolName: "Edit", CWD: root}
	in.ToolInput.FilePath = filepath.Join("internal", "cli", "hook.go")

	d := decidePreToolUse(in, func(cwd string) (string, bool) { return root, true })
	if !d.allow {
		t.Fatalf("expected allow for relative path under cwd, got deny: %s", d.reason)
	}
}

// TestPreToolUseGatedByAgentMode: when BACIO_AGENT_MODE is unset the
// hook skips entirely — an interactive session is never confined.
func TestPreToolUseGatedByAgentMode(t *testing.T) {
	t.Setenv("BACIO_AGENT_MODE", "scratch")
	if err := os.Unsetenv("BACIO_AGENT_MODE"); err != nil {
		t.Fatalf("unset: %v", err)
	}
	cmd := hookPreToolUseCmd()
	out := captureStderr(t, func() {
		if err := cmd.RunE(cmd, nil); err != nil {
			t.Fatalf("RunE: %v", err)
		}
	})
	if !strings.Contains(out, "pre-tool-use") || !strings.Contains(out, "skipping") {
		t.Fatalf("expected agent-mode skip notice on stderr, got: %q", out)
	}
}
