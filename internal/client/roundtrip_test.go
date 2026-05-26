package client_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/api"
	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// pair builds a temp-DB-backed local backend AND a remote backend
// pointing at an in-process httptest server wrapping the same store,
// so a round-trip test can drive both without a second SQLite file
// and assert they observe identical state. Now in package client_test
// (external) so api can import client without forming a cycle through
// the test binary.
type pair struct {
	store   *store.Store
	local   client.Client
	remote  client.Client
	repo    *model.Repo
	srv     *httptest.Server
	cleanup func()
}

func newPair(t *testing.T) *pair {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "db.sqlite")
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	repo, err := s.CreateRepo("TEST", "test-repo", "/tmp/test-repo", "")
	if err != nil {
		t.Fatalf("CreateRepo: %v", err)
	}

	srv := httptest.NewServer(api.New(s, api.Options{}, slog.New(slog.NewTextHandler(discard{}, nil))).Handler())

	local := client.NewLocalFromStore(s, "tester")
	remote, err := client.Open(context.Background(), client.Options{Remote: srv.URL, Actor: "tester"})
	if err != nil {
		t.Fatalf("client.Open(remote): %v", err)
	}
	return &pair{
		store: s, local: local, remote: remote, repo: repo, srv: srv,
		cleanup: func() {
			srv.Close()
			_ = s.Close()
		},
	}
}

// discard implements io.Writer for a silent slog handler.
type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

func TestRoundTripRepos(t *testing.T) {
	p := newPair(t)
	defer p.cleanup()
	ctx := context.Background()

	for _, c := range []client.Client{p.local, p.remote} {
		repos, err := c.ListRepos(ctx)
		if err != nil {
			t.Fatalf("ListRepos: %v", err)
		}
		if len(repos) != 1 || repos[0].Prefix != "TEST" {
			t.Fatalf("ListRepos: got %+v", repos)
		}
	}
}

func TestRoundTripIssueLifecycle(t *testing.T) {
	p := newPair(t)
	defer p.cleanup()
	ctx := context.Background()

	// Create via local, fetch via remote.
	iss, err := p.local.CreateIssue(ctx, p.repo, inputs.IssueAddInput{
		Title: "from local", State: "todo",
	}, false)
	if err != nil {
		t.Fatalf("local CreateIssue: %v", err)
	}
	if iss.Key != "TEST-1" {
		t.Fatalf("local CreateIssue: got key %q, want TEST-1", iss.Key)
	}
	got, err := p.remote.GetIssueByKey(ctx, p.repo, "TEST-1")
	if err != nil {
		t.Fatalf("remote GetIssueByKey: %v", err)
	}
	if got.Title != "from local" {
		t.Fatalf("remote GetIssueByKey: title %q, want %q", got.Title, "from local")
	}

	// Create via remote, fetch via local.
	iss2, err := p.remote.CreateIssue(ctx, p.repo, inputs.IssueAddInput{
		Title: "from remote", State: "todo",
	}, false)
	if err != nil {
		t.Fatalf("remote CreateIssue: %v", err)
	}
	got2, err := p.local.GetIssueByKey(ctx, p.repo, iss2.Key)
	if err != nil {
		t.Fatalf("local GetIssueByKey: %v", err)
	}
	if got2.Title != "from remote" {
		t.Fatalf("local GetIssueByKey: title %q, want %q", got2.Title, "from remote")
	}

	// State change via remote, observe via local.
	if _, err := p.remote.SetIssueState(ctx, p.repo, iss2.Key, model.StateInProgress, false); err != nil {
		t.Fatalf("remote SetIssueState: %v", err)
	}
	post, err := p.local.GetIssueByKey(ctx, p.repo, iss2.Key)
	if err != nil {
		t.Fatalf("local GetIssueByKey post-state: %v", err)
	}
	if post.State != model.StateInProgress {
		t.Fatalf("post-state: got %q, want in_progress", post.State)
	}

	// Audit-log parity: both creates + state change must show up.
	hist, err := p.local.ListHistory(ctx, p.repo, store.HistoryFilter{Limit: 50, OldestFirst: true})
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	wantOps := []string{"issue.create", "issue.create", "issue.state"}
	gotOps := []string{}
	for _, h := range hist {
		switch h.Op {
		case "issue.create", "issue.state":
			gotOps = append(gotOps, h.Op)
		}
	}
	if len(gotOps) != len(wantOps) {
		t.Fatalf("audit ops: got %v, want %v", gotOps, wantOps)
	}
	for i := range gotOps {
		if gotOps[i] != wantOps[i] {
			t.Fatalf("audit ops[%d]: got %q, want %q", i, gotOps[i], wantOps[i])
		}
	}
}

func TestRoundTripFeatureLifecycle(t *testing.T) {
	p := newPair(t)
	defer p.cleanup()
	ctx := context.Background()

	f, err := p.local.CreateFeature(ctx, p.repo, inputs.FeatureAddInput{Title: "Feature One"}, false)
	if err != nil {
		t.Fatalf("local CreateFeature: %v", err)
	}
	if f.Slug != "feature-one" {
		t.Fatalf("CreateFeature slug = %q, want feature-one", f.Slug)
	}

	// Remote can find it, lists it, and shows it.
	feats, err := p.remote.ListFeatures(ctx, p.repo, false, false)
	if err != nil {
		t.Fatalf("remote ListFeatures: %v", err)
	}
	if len(feats) != 1 {
		t.Fatalf("remote ListFeatures: got %d, want 1", len(feats))
	}
	view, err := p.remote.ShowFeature(ctx, p.repo, "feature-one")
	if err != nil {
		t.Fatalf("remote ShowFeature: %v", err)
	}
	if view.Feature.Title != "Feature One" {
		t.Fatalf("remote ShowFeature title: %q", view.Feature.Title)
	}

	// Edit through remote.
	newTitle := "Feature One Updated"
	if _, err := p.remote.UpdateFeature(ctx, p.repo, "feature-one", &newTitle, nil, nil, nil, false); err != nil {
		t.Fatalf("remote UpdateFeature: %v", err)
	}
	post, err := p.local.GetFeatureBySlug(ctx, p.repo, "feature-one")
	if err != nil {
		t.Fatalf("local GetFeatureBySlug post-update: %v", err)
	}
	if post.Title != newTitle {
		t.Fatalf("post-update title: got %q, want %q", post.Title, newTitle)
	}
}

// TestRoundTripFeatureBranchName (BACI-225) confirms the new branch_name
// field round-trips through the local and remote clients: set on create,
// read via show, cleared via edit. Pinned because the remote path adds
// the field to a hand-built JSON map and a future refactor could
// silently drop it.
func TestRoundTripFeatureBranchName(t *testing.T) {
	p := newPair(t)
	defer p.cleanup()
	ctx := context.Background()

	// Create via the remote client with a branch set; local must see it.
	f, err := p.remote.CreateFeature(ctx, p.repo, inputs.FeatureAddInput{
		Title:      "Branchy",
		Slug:       "branchy",
		BranchName: "feat/branchy",
	}, false)
	if err != nil {
		t.Fatalf("remote CreateFeature: %v", err)
	}
	if f.BranchName == nil || *f.BranchName != "feat/branchy" {
		t.Fatalf("create branch = %v, want feat/branchy", f.BranchName)
	}
	got, err := p.local.GetFeatureBySlug(ctx, p.repo, "branchy")
	if err != nil {
		t.Fatalf("local GetFeatureBySlug: %v", err)
	}
	if got.BranchName == nil || *got.BranchName != "feat/branchy" {
		t.Fatalf("local sees branch = %v, want feat/branchy", got.BranchName)
	}

	// Edit via remote, non-empty new value.
	newBranch := "feat/branchy-v2"
	if _, err := p.remote.UpdateFeature(ctx, p.repo, "branchy", nil, nil, nil, &newBranch, false); err != nil {
		t.Fatalf("remote UpdateFeature branch: %v", err)
	}
	got, err = p.local.GetFeatureBySlug(ctx, p.repo, "branchy")
	if err != nil {
		t.Fatalf("local GetFeatureBySlug post-update: %v", err)
	}
	if got.BranchName == nil || *got.BranchName != "feat/branchy-v2" {
		t.Fatalf("post-update branch = %v, want feat/branchy-v2", got.BranchName)
	}

	// Clear via remote (non-nil empty pointer).
	empty := ""
	if _, err := p.remote.UpdateFeature(ctx, p.repo, "branchy", nil, nil, nil, &empty, false); err != nil {
		t.Fatalf("remote UpdateFeature clear: %v", err)
	}
	got, err = p.local.GetFeatureBySlug(ctx, p.repo, "branchy")
	if err != nil {
		t.Fatalf("local GetFeatureBySlug post-clear: %v", err)
	}
	if got.BranchName != nil {
		t.Fatalf("post-clear BranchName = %v, want nil", *got.BranchName)
	}
}

func TestRoundTripCommentsAndDocs(t *testing.T) {
	p := newPair(t)
	defer p.cleanup()
	ctx := context.Background()

	// Create an issue + comment through local.
	iss, err := p.local.CreateIssue(ctx, p.repo, inputs.IssueAddInput{Title: "with comments"}, false)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if _, err := p.local.AddComment(ctx, p.repo, inputs.CommentAddInput{
		IssueKey: iss.Key, Author: "alice", Body: "first comment",
	}, false); err != nil {
		t.Fatalf("AddComment: %v", err)
	}

	// Remote sees it via ShowIssue.
	view, err := p.remote.ShowIssue(ctx, p.repo, iss.Key)
	if err != nil {
		t.Fatalf("remote ShowIssue: %v", err)
	}
	if len(view.Comments) != 1 || view.Comments[0].Body != "first comment" {
		t.Fatalf("remote ShowIssue comments: %+v", view.Comments)
	}

	// Delete the comment over the remote client; local must agree it's gone.
	uuid := view.Comments[0].UUID
	if _, _, err := p.remote.DeleteComment(ctx, p.repo, inputs.CommentRmInput{
		IssueKey: iss.Key, CommentUUID: uuid,
	}, false); err != nil {
		t.Fatalf("remote DeleteComment: %v", err)
	}
	localCs, err := p.local.ListComments(ctx, p.repo, iss.Key)
	if err != nil {
		t.Fatalf("local ListComments: %v", err)
	}
	if len(localCs) != 0 {
		t.Fatalf("local still sees deleted comment: %+v", localCs)
	}

	// Re-add a comment, then delete via the local client with a dry-run
	// first — the dry-run must not remove the row.
	cm2, err := p.local.AddComment(ctx, p.repo, inputs.CommentAddInput{
		IssueKey: iss.Key, Author: "bob", Body: "second comment",
	}, false)
	if err != nil {
		t.Fatalf("AddComment #2: %v", err)
	}
	preview, _, err := p.local.DeleteComment(ctx, p.repo, inputs.CommentRmInput{
		IssueKey: iss.Key, CommentUUID: cm2.UUID,
	}, true)
	if err != nil {
		t.Fatalf("local DeleteComment dry-run: %v", err)
	}
	if preview == nil || preview.WouldRemove != 1 {
		t.Fatalf("dry-run preview: %+v", preview)
	}
	if cs, _ := p.local.ListComments(ctx, p.repo, iss.Key); len(cs) != 1 {
		t.Fatalf("dry-run deleted the comment: %d", len(cs))
	}
	if _, _, err := p.local.DeleteComment(ctx, p.repo, inputs.CommentRmInput{
		IssueKey: iss.Key, CommentUUID: cm2.UUID,
	}, false); err != nil {
		t.Fatalf("local DeleteComment: %v", err)
	}
	if cs, _ := p.remote.ListComments(ctx, p.repo, iss.Key); len(cs) != 0 {
		t.Fatalf("remote still sees deleted comment: %d", len(cs))
	}

	// Document round-trip.
	doc, err := p.remote.CreateDocument(ctx, p.repo, client.DocCreateInput{
		Filename: "design.md",
		Type:     model.DocTypeDesigns,
		Body:     "# Design\nbody",
	}, false)
	if err != nil {
		t.Fatalf("remote CreateDocument: %v", err)
	}
	if doc.Filename != "design.md" {
		t.Fatalf("CreateDocument filename: %q", doc.Filename)
	}
	body, err := p.local.DownloadDocument(ctx, p.repo, "design.md")
	if err != nil {
		t.Fatalf("local DownloadDocument: %v", err)
	}
	if !strings.Contains(string(body), "# Design") {
		t.Fatalf("DownloadDocument body: %q", string(body))
	}
}

// TestRoundTripFeatureComments pins the BACI-124 feature-comment add /
// list / show / delete cycle across the local + remote backends — the
// dual-backend equivalent of TestRoundTripCommentsAndDocs.
func TestRoundTripFeatureComments(t *testing.T) {
	p := newPair(t)
	defer p.cleanup()
	ctx := context.Background()

	// Create a feature via the local backend.
	feat, err := p.local.CreateFeature(ctx, p.repo, inputs.FeatureAddInput{
		Title: "Auth rewrite", Slug: "auth",
	}, false)
	if err != nil {
		t.Fatalf("CreateFeature: %v", err)
	}

	// Add a comment over local; the remote ShowFeature sees it.
	if _, err := p.local.AddFeatureComment(ctx, p.repo, inputs.FeatureCommentAddInput{
		FeatureSlug: feat.Slug, Author: "alice", Body: "first handoff",
	}, false); err != nil {
		t.Fatalf("AddFeatureComment: %v", err)
	}
	view, err := p.remote.ShowFeature(ctx, p.repo, feat.Slug)
	if err != nil {
		t.Fatalf("remote ShowFeature: %v", err)
	}
	if len(view.Comments) != 1 || view.Comments[0].Body != "first handoff" {
		t.Fatalf("remote ShowFeature comments: %+v", view.Comments)
	}

	// Delete via the remote client — local agrees the row is gone.
	uuid := view.Comments[0].UUID
	if _, _, err := p.remote.DeleteFeatureComment(ctx, p.repo, inputs.FeatureCommentRmInput{
		FeatureSlug: feat.Slug, CommentUUID: uuid,
	}, false); err != nil {
		t.Fatalf("remote DeleteFeatureComment: %v", err)
	}
	localCs, err := p.local.ListFeatureComments(ctx, p.repo, feat.Slug)
	if err != nil {
		t.Fatalf("local ListFeatureComments: %v", err)
	}
	if len(localCs) != 0 {
		t.Fatalf("local still sees deleted feature comment: %+v", localCs)
	}

	// Re-add through remote; the local dry-run path returns a preview
	// without removing the row.
	cm2, err := p.remote.AddFeatureComment(ctx, p.repo, inputs.FeatureCommentAddInput{
		FeatureSlug: feat.Slug, Author: "bob", Body: "second handoff",
	}, false)
	if err != nil {
		t.Fatalf("remote AddFeatureComment #2: %v", err)
	}
	preview, _, err := p.local.DeleteFeatureComment(ctx, p.repo, inputs.FeatureCommentRmInput{
		FeatureSlug: feat.Slug, CommentUUID: cm2.UUID,
	}, true)
	if err != nil {
		t.Fatalf("local DeleteFeatureComment dry-run: %v", err)
	}
	if preview == nil || preview.WouldRemove != 1 {
		t.Fatalf("dry-run preview: %+v", preview)
	}
	if cs, _ := p.local.ListFeatureComments(ctx, p.repo, feat.Slug); len(cs) != 1 {
		t.Fatalf("dry-run deleted the comment: %d", len(cs))
	}
	if _, _, err := p.local.DeleteFeatureComment(ctx, p.repo, inputs.FeatureCommentRmInput{
		FeatureSlug: feat.Slug, CommentUUID: cm2.UUID,
	}, false); err != nil {
		t.Fatalf("local DeleteFeatureComment: %v", err)
	}
	if cs, _ := p.remote.ListFeatureComments(ctx, p.repo, feat.Slug); len(cs) != 0 {
		t.Fatalf("remote still sees deleted feature comment: %d", len(cs))
	}
}

func TestRoundTripDryRun(t *testing.T) {
	p := newPair(t)
	defer p.cleanup()
	ctx := context.Background()

	// Dry-run create on remote: response shape matches a real call,
	// nothing written.
	projected, err := p.remote.CreateIssue(ctx, p.repo, inputs.IssueAddInput{
		Title: "would create", State: "todo",
	}, true)
	if err != nil {
		t.Fatalf("remote dry-run CreateIssue: %v", err)
	}
	if projected.Title != "would create" {
		t.Fatalf("dry-run projected title: %q", projected.Title)
	}
	// No issue actually exists.
	if _, err := p.local.GetIssueByKey(ctx, p.repo, "TEST-1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("after dry-run, expected ErrNotFound; got %v", err)
	}
	// And no audit row.
	hist, err := p.local.ListHistory(ctx, p.repo, store.HistoryFilter{Limit: 50})
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	for _, h := range hist {
		if h.Op == "issue.create" {
			t.Fatalf("dry-run wrote a history row: %+v", h)
		}
	}
}

func TestRoundTripDocSpecialFilename(t *testing.T) {
	// Filenames that are valid per the store's strict filename validator but
	// require URL escaping ("with space.md", "+" reserved char, etc.) round
	// through every doc verb on the remote backend.
	p := newPair(t)
	defer p.cleanup()
	ctx := context.Background()

	for _, name := range []string{"with space.md", "a+b.md", "q?z.md"} {
		t.Run(name, func(t *testing.T) {
			doc, err := p.remote.CreateDocument(ctx, p.repo, client.DocCreateInput{
				Filename: name,
				Type:     model.DocTypeDesigns,
				Body:     "BODY-" + name,
			}, false)
			if err != nil {
				t.Fatalf("CreateDocument: %v", err)
			}
			if doc.Filename != name {
				t.Fatalf("filename: got %q, want %q", doc.Filename, name)
			}

			view, err := p.remote.ShowDocument(ctx, p.repo, name, true)
			if err != nil {
				t.Fatalf("ShowDocument: %v", err)
			}
			if view.Document.Filename != name {
				t.Fatalf("show filename: %q", view.Document.Filename)
			}

			body, err := p.remote.DownloadDocument(ctx, p.repo, name)
			if err != nil {
				t.Fatalf("DownloadDocument: %v", err)
			}
			if !strings.Contains(string(body), "BODY-"+name) {
				t.Fatalf("download body: %q", string(body))
			}

			newType := "architecture"
			if _, err := p.remote.EditDocument(ctx, p.repo, name, &newType, nil, false); err != nil {
				t.Fatalf("EditDocument: %v", err)
			}

			iss, err := p.remote.CreateIssue(ctx, p.repo, inputs.IssueAddInput{
				Title: "for-link", State: "todo",
			}, false)
			if err != nil {
				t.Fatalf("CreateIssue: %v", err)
			}
			if _, err := p.remote.LinkDocument(ctx, p.repo, inputs.DocLinkInput{
				Filename: name, IssueKey: iss.Key,
			}, false); err != nil {
				t.Fatalf("LinkDocument: %v", err)
			}
			if _, _, err := p.remote.UnlinkDocument(ctx, p.repo, inputs.DocUnlinkInput{
				Filename: name, IssueKey: iss.Key,
			}, false); err != nil {
				t.Fatalf("UnlinkDocument: %v", err)
			}

			renamed := "renamed-" + name
			if _, err := p.remote.RenameDocument(ctx, p.repo, name, renamed, "", false); err != nil {
				t.Fatalf("RenameDocument: %v", err)
			}

			if _, _, err := p.remote.DeleteDocument(ctx, p.repo, renamed, false); err != nil {
				t.Fatalf("DeleteDocument: %v", err)
			}
		})
	}
}

func TestRoundTripNotFoundError(t *testing.T) {
	p := newPair(t)
	defer p.cleanup()
	ctx := context.Background()

	if _, err := p.remote.GetIssueByKey(ctx, p.repo, "TEST-999"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("remote 404 should unwrap to store.ErrNotFound; got %v", err)
	}
	if _, err := p.local.GetIssueByKey(ctx, p.repo, "TEST-999"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("local miss should be store.ErrNotFound; got %v", err)
	}
}

// TestRoundTripAgentLifecycle exercises the BACI-34 agent verbs through
// the remote backend and verifies the local store observes the same
// state — the strongest single-test correctness check for parity.
func TestRoundTripAgentLifecycle(t *testing.T) {
	p := newPair(t)
	defer p.cleanup()
	ctx := context.Background()

	iss, err := p.local.CreateIssue(ctx, p.repo, inputs.IssueAddInput{
		Title: "round-trip target", State: "todo",
	}, false)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	// Register via remote. BACI-100: the register route requires a
	// structurally valid UUID session_id, so use a real one.
	const sid = "9c0f7a32-3ab2-4c4d-8e6f-0a1b2c3d4e5f"
	sess, err := p.remote.RegisterAgent(ctx, p.repo, inputs.AgentRegisterInput{
		SessionID: sid, Actor: "tester", Model: "claude-opus-4-7",
	}, false)
	if err != nil {
		t.Fatalf("remote RegisterAgent: %v", err)
	}
	if sess.SessionID != sid {
		t.Fatalf("remote RegisterAgent session_id = %q", sess.SessionID)
	}

	// Local observes the same registered session in the default list.
	got, err := p.local.ListAgentSessions(ctx, client.AgentSessionFilter{
		Repo: p.repo, RegisteredOnly: true,
	})
	if err != nil {
		t.Fatalf("local ListAgentSessions: %v", err)
	}
	if len(got) != 1 || got[0].SessionID != sid {
		t.Fatalf("local list after remote register: %+v", got)
	}

	// Remote claim, observed by local-side derived `taken`.
	if _, err := p.remote.ClaimAgent(ctx, p.repo, inputs.AgentClaimInput{
		SessionID: sid, IssueKey: iss.Key, Prompt: "do the thing",
	}, false); err != nil {
		t.Fatalf("remote ClaimAgent: %v", err)
	}
	open, err := p.local.ListOpenClaims(ctx, p.repo)
	if err != nil {
		t.Fatalf("local ListOpenClaims: %v", err)
	}
	if len(open) != 1 || open[0].IssueKey != iss.Key {
		t.Fatalf("local open claims: %+v", open)
	}
	post, _ := p.local.GetIssueByKey(ctx, p.repo, iss.Key)
	if post.Assignee != "tester" {
		t.Fatalf("local-observed assignee after remote claim = %q", post.Assignee)
	}

	// Remote show (cross-repo verb) sees the claim history.
	view, err := p.remote.ShowAgentSession(ctx, sid)
	if err != nil {
		t.Fatalf("remote ShowAgentSession: %v", err)
	}
	if len(view.Claims) != 1 || view.Claims[0].Prompt != "do the thing" {
		t.Fatalf("remote show claims: %+v", view.Claims)
	}

	// Remote release clears the assignee back out.
	if _, err := p.remote.ReleaseAgent(ctx, p.repo, inputs.AgentReleaseInput{
		SessionID: sid, IssueKey: iss.Key, FinalState: string(model.StateInReview),
	}, false); err != nil {
		t.Fatalf("remote ReleaseAgent: %v", err)
	}
	open2, _ := p.local.ListOpenClaims(ctx, p.repo)
	if len(open2) != 0 {
		t.Fatalf("open claims after release: %+v", open2)
	}
	post2, _ := p.local.GetIssueByKey(ctx, p.repo, iss.Key)
	if post2.Assignee != "" {
		t.Fatalf("assignee after remote release = %q", post2.Assignee)
	}

	// Heartbeat is silent (no audit row, just bumps last_seen_at).
	if _, err := p.remote.HeartbeatAgent(ctx, p.repo, inputs.AgentHeartbeatInput{
		SessionID: sid,
	}, false); err != nil {
		t.Fatalf("remote HeartbeatAgent: %v", err)
	}

	// End closes the session out.
	if _, err := p.remote.EndAgent(ctx, p.repo, inputs.AgentEndInput{
		SessionID: sid, Reason: "stop",
	}, false); err != nil {
		t.Fatalf("remote EndAgent: %v", err)
	}
	ended, _ := p.store.GetAgentSession(sid)
	if ended.EndedAt == nil || ended.EndReason != "stop" {
		t.Fatalf("ended state: %+v", ended)
	}
}

// TestRoundTripPromptTemplates exercises the BACI-36 settings verbs
// through the remote backend and verifies the local store observes the
// same state. Covers body + state-gate, set + reset, with a quick
// dry-run sanity check on both paths.
func TestRoundTripPromptTemplates(t *testing.T) {
	p := newPair(t)
	defer p.cleanup()
	ctx := context.Background()

	// Bodies: list defaults round-trip → set via remote → observe via local.
	tplsBefore, err := p.remote.GetPromptTemplates(ctx)
	if err != nil {
		t.Fatalf("remote GetPromptTemplates: %v", err)
	}
	if got := tplsBefore[string(model.DispatchModePlan)]; got != model.DefaultPromptTemplate(model.DispatchModePlan) {
		t.Fatalf("plan default mismatch: %q", got)
	}

	if err := p.remote.SetPromptTemplate(ctx, string(model.DispatchModePlan), "custom-via-remote", false); err != nil {
		t.Fatalf("remote SetPromptTemplate: %v", err)
	}
	got, _ := p.store.GetPromptTemplate(model.DispatchModePlan)
	if got != "custom-via-remote" {
		t.Fatalf("local-observed plan body after remote set: %q", got)
	}

	// Reset path (empty body → DELETE).
	if err := p.remote.SetPromptTemplate(ctx, string(model.DispatchModePlan), "", false); err != nil {
		t.Fatalf("remote SetPromptTemplate reset: %v", err)
	}
	got, _ = p.store.GetPromptTemplate(model.DispatchModePlan)
	if got != model.DefaultPromptTemplate(model.DispatchModePlan) {
		t.Fatalf("reset did not revert: %q", got)
	}

	// States: same pattern.
	if err := p.remote.SetPromptStates(ctx, string(model.DispatchModeReview),
		[]string{"in_review", "needs_action"}, false); err != nil {
		t.Fatalf("remote SetPromptStates: %v", err)
	}
	gotStates, _ := p.store.GetPromptStates(model.DispatchModeReview)
	if len(gotStates) != 2 || gotStates[0] != model.StateInReview || gotStates[1] != model.StateNeedsAction {
		t.Fatalf("local-observed review states: %v", gotStates)
	}

	// Dry-run set should NOT touch the store.
	if err := p.remote.SetPromptStates(ctx, string(model.DispatchModeReview),
		[]string{"todo"}, true); err != nil {
		t.Fatalf("remote SetPromptStates dry-run: %v", err)
	}
	postDry, _ := p.store.GetPromptStates(model.DispatchModeReview)
	if len(postDry) != 2 {
		t.Fatalf("dry-run set persisted: %v", postDry)
	}

	// Reset states via remote (empty list → DELETE).
	if err := p.remote.SetPromptStates(ctx, string(model.DispatchModeReview), nil, false); err != nil {
		t.Fatalf("remote SetPromptStates reset: %v", err)
	}
	postReset, _ := p.store.GetPromptStates(model.DispatchModeReview)
	want := model.DefaultPromptStates(model.DispatchModeReview)
	if len(postReset) != len(want) {
		t.Fatalf("reset did not revert: got %v want %v", postReset, want)
	}
}

// TestRoundTripListShippedIssues — BACI-187. Local + remote backends
// observe the same shipped list (state='done' AND terminal_at IS NOT
// NULL, newest-first). Cancelled and open rows are excluded; the
// remote DTO decode preserves key + title + terminal_at on each row.
func TestRoundTripListShippedIssues(t *testing.T) {
	p := newPair(t)
	defer p.cleanup()
	ctx := context.Background()

	// Seed: one done, one cancelled, one open.
	done, err := p.local.CreateIssue(ctx, p.repo, inputs.IssueAddInput{Title: "done"}, false)
	if err != nil {
		t.Fatalf("create done: %v", err)
	}
	cancel, err := p.local.CreateIssue(ctx, p.repo, inputs.IssueAddInput{Title: "cancelled"}, false)
	if err != nil {
		t.Fatalf("create cancelled: %v", err)
	}
	if _, err := p.local.CreateIssue(ctx, p.repo, inputs.IssueAddInput{Title: "open"}, false); err != nil {
		t.Fatalf("create open: %v", err)
	}
	if _, err := p.local.SetIssueState(ctx, p.repo, done.Key, model.StateDone, false); err != nil {
		t.Fatalf("ship done: %v", err)
	}
	if _, err := p.local.SetIssueState(ctx, p.repo, cancel.Key, model.StateCancelled, false); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	for label, c := range map[string]client.Client{"local": p.local, "remote": p.remote} {
		got, err := c.ListShippedIssues(ctx, p.repo, store.ShippedFilter{})
		if err != nil {
			t.Fatalf("%s ListShippedIssues: %v", label, err)
		}
		if len(got) != 1 {
			t.Fatalf("%s rows: %d, want 1 (only the done row)", label, len(got))
		}
		if got[0].Key != done.Key {
			t.Fatalf("%s key = %q, want %q", label, got[0].Key, done.Key)
		}
		if got[0].Title != "done" {
			t.Fatalf("%s title = %q, want 'done'", label, got[0].Title)
		}
		if got[0].TerminalAt == nil {
			t.Fatalf("%s row missing TerminalAt", label)
		}
	}
}
