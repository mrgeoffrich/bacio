package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/git"
)

func TestClaudeProjectSlug(t *testing.T) {
	cases := map[string]string{
		"/Users/geoff/Repos/bacio":              "-Users-geoff-Repos-bacio",
		"/Users/geoff/Repos/bacio/.claude/wt/x": "-Users-geoff-Repos-bacio--claude-wt-x",
		"/tmp/baci-10-worktree":                 "-tmp-baci-10-worktree",
	}
	for in, want := range cases {
		if got := claudeProjectSlug(in); got != want {
			t.Errorf("claudeProjectSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

// layoutTranscript writes a fake ~/.claude/projects tree:
//
//	<home>/.claude/projects/<slug>/<sessionID>/subagents/agent-<agentID>.jsonl (+ .meta.json)
//
// and returns the project dir whose slug yields <slug>.
func layoutTranscript(t *testing.T, home, projectDir, sessionID, agentID, jsonl, meta string) {
	t.Helper()
	slug := claudeProjectSlug(projectDir)
	dir := filepath.Join(home, ".claude", "projects", slug, sessionID, "subagents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir transcript tree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent-"+agentID+".jsonl"), []byte(jsonl), 0o644); err != nil {
		t.Fatalf("write jsonl: %v", err)
	}
	if meta != "" {
		if err := os.WriteFile(filepath.Join(dir, "agent-"+agentID+".meta.json"), []byte(meta), 0o644); err != nil {
			t.Fatalf("write meta: %v", err)
		}
	}
}

func TestLocateSubagentTranscript(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	projectDir := "/Users/test/Repos/proj"
	jsonl := `{"type":"user","message":{"role":"user","content":"hi"}}`
	layoutTranscript(t, home, projectDir, "sess-1", "abc123", jsonl,
		`{"agentType":"bacio-plan-worker","description":"Plan something"}`)

	tr, err := locateSubagentTranscript(projectDir, "abc123")
	if err != nil {
		t.Fatalf("locate: %v", err)
	}
	if tr.parentSessionID != "sess-1" {
		t.Errorf("parentSessionID = %q, want sess-1", tr.parentSessionID)
	}
	if tr.agentType != "bacio-plan-worker" || tr.description != "Plan something" {
		t.Errorf("meta not read: %+v", tr)
	}
	if !strings.HasSuffix(tr.path, "subagents/agent-abc123.jsonl") {
		t.Errorf("unexpected path %q", tr.path)
	}

	// 0-match case: a different agent id errors clearly.
	if _, err := locateSubagentTranscript(projectDir, "missing999"); err == nil {
		t.Fatal("expected an error for an unknown agent id")
	} else if !strings.Contains(err.Error(), "no transcript found") {
		t.Errorf("error %q should mention 'no transcript found'", err)
	}
}

// TestAttachTranscriptEndToEnd drives channelSource.AttachTranscript
// against a temp DB + a faked transcript tree, then confirms the raw
// .jsonl doc was created and linked to the issue.
func TestAttachTranscriptEndToEnd(t *testing.T) {
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
	iss, err := c.CreateIssue(ctx, repo, inputs.IssueAddInput{Title: "Test issue"}, false)
	if err != nil {
		t.Fatalf("create issue: %v", err)
	}

	projectDir := filepath.Join(repoRoot)
	jsonl := strings.Join([]string{
		`{"type":"user","message":{"role":"user","content":"Do the thing."}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Did it."}]}}`,
	}, "\n")
	layoutTranscript(t, home, projectDir, "sess-parent", "deadbeef", jsonl,
		`{"agentType":"bacio-implement-worker","description":"Implement `+iss.Key+`"}`)

	src := &channelSource{
		c:          c,
		repo:       repo,
		repoRoot:   repoRoot,
		projectDir: projectDir,
		host:       "testhost",
		claudePID:  int64(os.Getpid()),
		channelPID: 99999,
		pushed:     map[int64]bool{},
	}

	// "agent-" prefix is stripped by the channel handler; AttachTranscript
	// itself receives the bare id.
	msg, err := src.AttachTranscript(ctx, iss.Key, "deadbeef", "the summary")
	if err != nil {
		t.Fatalf("AttachTranscript: %v", err)
	}
	if !strings.Contains(msg, iss.Key) || !strings.Contains(msg, "deadbeef") {
		t.Errorf("confirmation %q missing issue key / agent id", msg)
	}

	// The raw transcript doc should be visible from the issue brief.
	brief, err := c.BriefIssue(ctx, repo, iss.Key, client.BriefOptions{})
	if err != nil {
		t.Fatalf("issue brief: %v", err)
	}
	if len(brief.Documents) != 1 {
		t.Fatalf("expected 1 linked document, got %d", len(brief.Documents))
	}
	doc := brief.Documents[0]
	wantName := "bacio-transcript-" + iss.Key + "-agent-deadbeef.jsonl"
	if doc.Filename != wantName {
		t.Errorf("doc filename = %q, want %q", doc.Filename, wantName)
	}
	// The body is the raw .jsonl, stored verbatim (fixture is well under cap).
	if doc.Content != jsonl {
		t.Errorf("doc content should byte-equal the raw transcript fixture:\ngot:  %q\nwant: %q", doc.Content, jsonl)
	}
	// The supervisor's note is moved onto the link description, not the body.
	if !strings.Contains(doc.Description, "the summary") {
		t.Errorf("link description should carry the supervisor note: %q", doc.Description)
	}
	if !strings.Contains(doc.Description, "raw .jsonl") {
		t.Errorf("link description should mention raw .jsonl: %q", doc.Description)
	}

	// Idempotent: re-attaching the same (issue, agent) pair just refreshes.
	if _, err := src.AttachTranscript(ctx, iss.Key, "deadbeef", "updated"); err != nil {
		t.Fatalf("re-attach: %v", err)
	}
	brief2, err := c.BriefIssue(ctx, repo, iss.Key, client.BriefOptions{})
	if err != nil {
		t.Fatalf("issue brief 2: %v", err)
	}
	if len(brief2.Documents) != 1 {
		t.Fatalf("re-attach created a duplicate doc: %d docs", len(brief2.Documents))
	}
}
