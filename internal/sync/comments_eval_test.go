package sync

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// TestExportComment_NonEvalYAMLByteIdentical (BACI-131) guards the
// "no diff churn" invariant: a normal comment's exported YAML must
// not gain any of the new optional eval keys, so existing comment
// files in the sync repo round-trip byte-identical.
func TestExportComment_NonEvalYAMLByteIdentical(t *testing.T) {
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	r, err := s.CreateRepo("MINI", "bacio", "/local/path", "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	iss, err := s.CreateIssue(r.ID, nil, "x", "", model.StateTodo, nil)
	if err != nil {
		t.Fatalf("create issue: %v", err)
	}
	if _, err := s.CreateComment(store.CreateCommentIn{
		IssueID: iss.ID, Author: "alice", Body: "normal",
	}); err != nil {
		t.Fatalf("CreateComment: %v", err)
	}

	dir := t.TempDir()
	if _, err := (&Engine{Store: s}).Export(context.Background(), dir); err != nil {
		t.Fatalf("export: %v", err)
	}

	commentsDir := filepath.Join(dir, "repos", "MINI", "issues", "MINI-1", "comments")
	entries, err := os.ReadDir(commentsDir)
	if err != nil {
		t.Fatalf("read comments: %v", err)
	}
	var yamlPath string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".yaml") {
			yamlPath = filepath.Join(commentsDir, e.Name())
		}
	}
	if yamlPath == "" {
		t.Fatalf("no yaml file emitted")
	}
	yamlBytes, err := os.ReadFile(yamlPath)
	if err != nil {
		t.Fatalf("read yaml: %v", err)
	}
	for _, banned := range []string{"eval:", "agent_session_id:", "mode:", "dispatch_id:"} {
		if strings.Contains(string(yamlBytes), banned) {
			t.Fatalf("non-eval YAML leaked %q: %s", banned, yamlBytes)
		}
	}
}

// TestRoundTripEvalComment (BACI-131) asserts the export → import
// cycle preserves eval, agent_session_id, and mode but never carries
// dispatch_id across the wire (it's a local-DB FK that can't survive
// the round-trip).
func TestRoundTripEvalComment(t *testing.T) {
	src, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open src: %v", err)
	}
	t.Cleanup(func() { _ = src.Close() })
	r, err := src.CreateRepo("MINI", "bacio", "/src/path", "")
	if err != nil {
		t.Fatalf("create src repo: %v", err)
	}
	iss, err := src.CreateIssue(r.ID, nil, "x", "", model.StateTodo, nil)
	if err != nil {
		t.Fatalf("create issue: %v", err)
	}
	// A real dispatch row so the FK on the eval comment is valid.
	if _, err := src.UpsertAgentSession(store.UpsertAgentSessionIn{
		SessionID: "be1a8a0f-975c-4be3-9084-1d226f20ec27",
		RepoID:    r.ID,
		Actor:     "agent-claude",
	}); err != nil {
		t.Fatalf("UpsertAgentSession: %v", err)
	}
	disp, err := src.AddDispatch(store.AddDispatchIn{
		RepoID: r.ID, TargetSessionID: "be1a8a0f-975c-4be3-9084-1d226f20ec27",
		IssueID: &iss.ID, Mode: "implement", CreatedBy: "supervisor",
	})
	if err != nil {
		t.Fatalf("AddDispatch: %v", err)
	}
	cm, err := src.CreateComment(store.CreateCommentIn{
		IssueID:        iss.ID,
		Author:         "geoff",
		Body:           "missed criterion 3\n",
		Eval:           true,
		AgentSessionID: "be1a8a0f-975c-4be3-9084-1d226f20ec27",
		DispatchID:     &disp.ID,
		Mode:           "implement",
	})
	if err != nil {
		t.Fatalf("CreateComment: %v", err)
	}

	dir := t.TempDir()
	if _, err := (&Engine{Store: src}).Export(context.Background(), dir); err != nil {
		t.Fatalf("export: %v", err)
	}

	dst, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open dst: %v", err)
	}
	t.Cleanup(func() { _ = dst.Close() })
	if _, err := (&Engine{Store: dst, SkipPropagateDeletes: true}).Import(context.Background(), dir); err != nil {
		t.Fatalf("import: %v", err)
	}

	got, err := dst.GetCommentByUUID(cm.UUID)
	if err != nil {
		t.Fatalf("get imported comment: %v", err)
	}
	if !got.Eval || got.AgentSessionID != "be1a8a0f-975c-4be3-9084-1d226f20ec27" || got.Mode != "implement" {
		t.Fatalf("eval triple lost on import: %+v", got)
	}
	// dispatch_id is a local-DB integer FK; the importer leaves it
	// NULL because the dst DB has no matching agent_dispatches row.
	if got.DispatchID != nil {
		t.Fatalf("DispatchID = %v, want nil (dispatch_id is local-only)", got.DispatchID)
	}
}
