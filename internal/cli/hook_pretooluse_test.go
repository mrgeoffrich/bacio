package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestParseClaimIssue pins the worker-claim parser the agent-id→dispatch
// binding keys off: it extracts the issue from a `bacio agent claim <ISSUE>`
// Bash command and fail-closes on everything else.
func TestParseClaimIssue(t *testing.T) {
	cases := []struct {
		name    string
		tool    string
		command string
		wantKey string
		wantOK  bool
	}{
		{"plain claim", "Bash", "bacio agent claim BACI-12 --prompt implement", "BACI-12", true},
		{"absolute binary", "Bash", "/usr/local/bin/bacio agent claim ZZZZ-9", "ZZZZ-9", true},
		{"workspace binary", "Bash", ".bin/bacio-agent-slug agent claim PROX-3 --env x", "PROX-3", true},
		// The brief writes `claim <ISSUE> --prompt <mode>` (issue first). A flag
		// value before the issue can't be told from a positional, so fail-close.
		{"flag value before issue fails closed", "Bash", "bacio agent claim --prompt p MINI-7", "", false},
		{"not a claim verb", "Bash", "bacio agent release BACI-1", "", false},
		{"not a claim at all", "Bash", "bacio worktree init", "", false},
		{"json form fails closed", "Bash", `bacio agent claim --json '{"issue":"BACI-1"}'`, "", false},
		{"pipe fails closed", "Bash", "echo x | bacio agent claim BACI-1", "", false},
		{"subshell fails closed", "Bash", "bacio agent claim $(echo BACI-1)", "", false},
		{"non-issue positional", "Bash", "bacio agent claim notakey --prompt p", "", false},
		{"wrong tool", "Write", "bacio agent claim BACI-12", "", false},
		{"empty", "Bash", "   ", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			key, ok := parseClaimIssue(c.tool, c.command)
			if ok != c.wantOK || key != c.wantKey {
				t.Errorf("parseClaimIssue(%q, %q) = (%q, %v), want (%q, %v)",
					c.tool, c.command, key, ok, c.wantKey, c.wantOK)
			}
		})
	}
}

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

// TestDecidePreToolUseNonWriteTool: decidePreToolUse only handles
// Write/Edit — every other tool (notably Bash, since BACI-134 widened
// the matcher to Write|Edit|Bash) is allowed here and routed to a
// sibling decider in the caller.
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

// TestDecideBashSqlite3 pins the BACI-134 path-based sqlite3 confinement
// guard: a Bash call that targets the live shared bacio DB is denied;
// every other shape (different path, fail-open ambiguous cases, no
// sqlite3 invocation at all) is allowed. Path-based, not verb-based —
// even a SELECT against the live DB is denied because the guard's job
// is to keep every interaction with the shared store going through the
// audit-logged store boundary.
func TestDecideBashSqlite3(t *testing.T) {
	parent := t.TempDir()
	liveDB := filepath.Join(parent, "home", ".bacio", "db.sqlite")
	if err := os.MkdirAll(filepath.Dir(liveDB), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(liveDB, []byte{}, 0o644); err != nil {
		t.Fatalf("write live db: %v", err)
	}
	resolveLive := func() string { return evalSymlinksLenient(liveDB) }

	// A worktree-isolated DB sitting at <worktree>/.bacio/db.sqlite —
	// the BACI-87 isolated-store shape. Different absolute path; the
	// guard must allow it.
	worktree := filepath.Join(parent, "worktree")
	worktreeDB := filepath.Join(worktree, ".bacio", "db.sqlite")
	if err := os.MkdirAll(filepath.Dir(worktreeDB), 0o755); err != nil {
		t.Fatalf("mkdir worktree: %v", err)
	}
	if err := os.WriteFile(worktreeDB, []byte{}, 0o644); err != nil {
		t.Fatalf("write worktree db: %v", err)
	}

	cases := []struct {
		name      string
		toolName  string
		cwd       string
		command   string
		wantAllow bool
	}{
		{
			name:      "delete-against-live-db",
			toolName:  "Bash",
			cwd:       worktree,
			command:   `sqlite3 ` + liveDB + ` "DELETE FROM issues WHERE id=1"`,
			wantAllow: false,
		},
		{
			// Path-based, not verb-based: even a SELECT is denied
			// because reading the shared DB with raw SQL bypasses the
			// audit-logged read surface too. The brief points the worker
			// at `bacio` read verbs instead.
			name:      "select-against-live-db",
			toolName:  "Bash",
			cwd:       worktree,
			command:   `sqlite3 ` + liveDB + ` "SELECT 1"`,
			wantAllow: false,
		},
		{
			name:      "worktree-isolated-db-allowed",
			toolName:  "Bash",
			cwd:       worktree,
			command:   `sqlite3 ` + worktreeDB + ` "SELECT 1"`,
			wantAllow: true,
		},
		{
			// Relative path resolved against cwd (the worktree) — same
			// absolute path as worktreeDB above, allowed.
			name:      "relative-worktree-db-allowed",
			toolName:  "Bash",
			cwd:       worktree,
			command:   `sqlite3 .bacio/db.sqlite "SELECT 1"`,
			wantAllow: true,
		},
		{
			name:      "scratch-tmp-db-allowed",
			toolName:  "Bash",
			cwd:       worktree,
			command:   `sqlite3 /tmp/scratch.db "SELECT 1"`,
			wantAllow: true,
		},
		{
			name:      "no-sqlite3-token-allowed",
			toolName:  "Bash",
			cwd:       worktree,
			command:   `ls -la ~/.bacio/db.sqlite`,
			wantAllow: true,
		},
		{
			// Command substitution hides the actual invocation from a
			// plain strings.Fields tokenisation — fail-open by design.
			name:      "command-substitution-allowed",
			toolName:  "Bash",
			cwd:       worktree,
			command:   `echo $(sqlite3 ` + liveDB + ` "DELETE FROM issues")`,
			wantAllow: true,
		},
		{
			// Pipe — same fail-open reason.
			name:      "pipe-allowed",
			toolName:  "Bash",
			cwd:       worktree,
			command:   `echo "x" | sqlite3 ` + liveDB,
			wantAllow: true,
		},
		{
			// Env-var-referenced path — we can't statically resolve $X.
			name:      "envvar-path-allowed",
			toolName:  "Bash",
			cwd:       worktree,
			command:   `sqlite3 $BACIO_DB "DELETE FROM issues"`,
			wantAllow: true,
		},
		{
			name:      "empty-command-allowed",
			toolName:  "Bash",
			cwd:       worktree,
			command:   ``,
			wantAllow: true,
		},
		{
			// Tool isn't Bash — this decider doesn't fire. (Write/Edit
			// goes through decidePreToolUse instead; the caller routes.)
			name:      "non-bash-tool-allowed",
			toolName:  "Edit",
			cwd:       worktree,
			command:   `sqlite3 ` + liveDB + ` "DELETE FROM issues"`,
			wantAllow: true,
		},
		{
			// Quoted DB path resolves the same as a bare path.
			name:      "quoted-live-db-denied",
			toolName:  "Bash",
			cwd:       worktree,
			command:   `sqlite3 "` + liveDB + `" "SELECT 1"`,
			wantAllow: false,
		},
		{
			// Flags between the sqlite3 binary and the DB path are
			// skipped — `sqlite3 -batch <live-db> ...` still denies.
			name:      "flags-then-live-db-denied",
			toolName:  "Bash",
			cwd:       worktree,
			command:   `sqlite3 -batch ` + liveDB + ` "SELECT 1"`,
			wantAllow: false,
		},
		{
			// `sqlite3` with no path at all (e.g. interactive shell or
			// stdin-driven) — no candidate token to test, fail-open.
			name:      "sqlite3-no-path-allowed",
			toolName:  "Bash",
			cwd:       worktree,
			command:   `sqlite3 -version`,
			wantAllow: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := &preToolUseInput{ToolName: c.toolName, CWD: c.cwd}
			in.ToolInput.Command = c.command

			d := decideBashSqlite3(in, resolveLive)
			if d.allow != c.wantAllow {
				t.Fatalf("decideBashSqlite3 allow=%v, want %v (reason=%q)", d.allow, c.wantAllow, d.reason)
			}
			if !c.wantAllow && !strings.Contains(d.reason, evalSymlinksLenient(liveDB)) {
				t.Fatalf("deny reason should name the live DB %q; got: %s", evalSymlinksLenient(liveDB), d.reason)
			}
		})
	}
}

// TestDecideBashSqlite3HomeExpansion: a `~/` prefix in the candidate
// path is expanded against $HOME so `sqlite3 ~/.bacio/db.sqlite` is
// recognised as targeting the live DB. Set HOME to a temp dir so the
// test doesn't depend on the actual user's home.
func TestDecideBashSqlite3HomeExpansion(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	liveDB := filepath.Join(home, ".bacio", "db.sqlite")
	if err := os.MkdirAll(filepath.Dir(liveDB), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(liveDB, []byte{}, 0o644); err != nil {
		t.Fatalf("write live db: %v", err)
	}
	resolveLive := func() string { return evalSymlinksLenient(liveDB) }

	in := &preToolUseInput{ToolName: "Bash", CWD: t.TempDir()}
	in.ToolInput.Command = `sqlite3 ~/.bacio/db.sqlite "DELETE FROM issues"`

	d := decideBashSqlite3(in, resolveLive)
	if d.allow {
		t.Fatalf("expected deny for sqlite3 against ~/.bacio/db.sqlite, got allow")
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
