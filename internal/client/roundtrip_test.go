package client

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
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// pair builds a temp-DB-backed local backend AND a remote backend
// pointing at an in-process httptest server wrapping the same store,
// so a round-trip test can drive both without a second SQLite file
// and assert they observe identical state.
type pair struct {
	store    *store.Store
	local    Client
	remote   Client
	repo     *model.Repo
	srv      *httptest.Server
	cleanup  func()
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

	local := &localClient{store: s, actor: "tester"}
	remote, err := newRemoteClient(Options{Remote: srv.URL, Actor: "tester"})
	if err != nil {
		t.Fatalf("newRemoteClient: %v", err)
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

	for _, c := range []Client{p.local, p.remote} {
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
	feats, err := p.remote.ListFeatures(ctx, p.repo, false)
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
	if _, err := p.remote.UpdateFeature(ctx, p.repo, "feature-one", &newTitle, nil, false); err != nil {
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

	// Document round-trip.
	doc, err := p.remote.CreateDocument(ctx, p.repo, DocCreateInput{
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
			doc, err := p.remote.CreateDocument(ctx, p.repo, DocCreateInput{
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

	// Register via remote.
	sess, err := p.remote.RegisterAgent(ctx, p.repo, inputs.AgentRegisterInput{
		SessionID: "rt-sess", Actor: "tester", Model: "claude-opus-4-7",
	}, false)
	if err != nil {
		t.Fatalf("remote RegisterAgent: %v", err)
	}
	if sess.SessionID != "rt-sess" {
		t.Fatalf("remote RegisterAgent session_id = %q", sess.SessionID)
	}

	// Local observes the same registered session in the default list.
	got, err := p.local.ListAgentSessions(ctx, AgentSessionFilter{
		Repo: p.repo, RegisteredOnly: true,
	})
	if err != nil {
		t.Fatalf("local ListAgentSessions: %v", err)
	}
	if len(got) != 1 || got[0].SessionID != "rt-sess" {
		t.Fatalf("local list after remote register: %+v", got)
	}

	// Remote claim, observed by local-side derived `taken`.
	if _, err := p.remote.ClaimAgent(ctx, p.repo, inputs.AgentClaimInput{
		SessionID: "rt-sess", IssueKey: iss.Key, Prompt: "do the thing",
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
	view, err := p.remote.ShowAgentSession(ctx, "rt-sess")
	if err != nil {
		t.Fatalf("remote ShowAgentSession: %v", err)
	}
	if len(view.Claims) != 1 || view.Claims[0].Prompt != "do the thing" {
		t.Fatalf("remote show claims: %+v", view.Claims)
	}

	// Remote release clears the assignee back out.
	if _, err := p.remote.ReleaseAgent(ctx, p.repo, inputs.AgentReleaseInput{
		SessionID: "rt-sess", IssueKey: iss.Key,
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
		SessionID: "rt-sess",
	}, false); err != nil {
		t.Fatalf("remote HeartbeatAgent: %v", err)
	}

	// End closes the session out.
	if _, err := p.remote.EndAgent(ctx, p.repo, inputs.AgentEndInput{
		SessionID: "rt-sess", Reason: "stop",
	}, false); err != nil {
		t.Fatalf("remote EndAgent: %v", err)
	}
	ended, _ := p.store.GetAgentSession("rt-sess")
	if ended.EndedAt == nil || ended.EndReason != "stop" {
		t.Fatalf("ended state: %+v", ended)
	}
}
