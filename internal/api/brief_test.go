package api_test

import (
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/api"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

func TestIssueBriefHappy(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	feat := seedFeature(t, s, repo, "auth", "Auth")
	iss, _ := s.CreateIssue(repo.ID, &feat.ID, "x", "", model.StateTodo, nil)
	if _, err := s.CreateComment(store.CreateCommentIn{IssueID: iss.ID, Author: "alice", Body: "first comment"}); err != nil {
		t.Fatalf("comment: %v", err)
	}
	resp, body := apiGet(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/brief")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	for _, want := range []string{
		`"issue"`, `"feature"`, `"relations"`, `"pull_requests"`,
		`"documents"`, `"comments"`, `"warnings"`, `"slug": "auth"`,
		`"first comment"`,
	} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("missing %s in: %s", want, body)
		}
	}
}

func TestIssueBriefNoComments(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "x")
	if _, err := s.CreateComment(store.CreateCommentIn{IssueID: iss.ID, Author: "alice", Body: "should be skipped"}); err != nil {
		t.Fatalf("comment: %v", err)
	}
	resp, body := apiGet(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/brief?no_comments=true")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if strings.Contains(string(body), "should be skipped") {
		t.Fatalf("comments leaked with no_comments=true: %s", body)
	}
	if !strings.Contains(string(body), `"comments": []`) {
		t.Fatalf("expected empty comments array: %s", body)
	}
}

func TestIssueBriefNoFeatureDocs(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	feat := seedFeature(t, s, repo, "auth", "Auth")
	iss, _ := s.CreateIssue(repo.ID, &feat.ID, "x", "", model.StateTodo, nil)
	// Use DocTypePlan so the doc's body would be inlined by default —
	// that gives the `no_feature_docs=1` query string something to
	// suppress so the assertion stays meaningful after BACI-115.
	doc, err := s.CreateDocument(repo.ID, "feat-doc.md", model.DocTypePlan, "feature body content", "")
	if err != nil {
		t.Fatalf("create doc: %v", err)
	}
	if _, err := s.LinkDocument(doc.ID, store.LinkTarget{FeatureID: &feat.ID}, "spec"); err != nil {
		t.Fatalf("link: %v", err)
	}
	respDef, bodyDef := apiGet(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/brief")
	if respDef.StatusCode != 200 {
		t.Fatalf("status: %d", respDef.StatusCode)
	}
	if !strings.Contains(string(bodyDef), "feat-doc.md") {
		t.Fatalf("feature doc missing by default: %s", bodyDef)
	}
	respSkip, bodySkip := apiGet(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/brief?no_feature_docs=1")
	if respSkip.StatusCode != 200 {
		t.Fatalf("status: %d", respSkip.StatusCode)
	}
	if strings.Contains(string(bodySkip), "feat-doc.md") {
		t.Fatalf("feature doc leaked with no_feature_docs=1: %s", bodySkip)
	}
}

func TestIssueBriefNoDocContent(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "x")
	// Use DocTypePlan so the doc body would be inlined by default —
	// otherwise `no_doc_content=true` would be a tautology after
	// BACI-115 narrowed inlining to plan/review.
	doc, err := s.CreateDocument(repo.ID, "iss-doc.md", model.DocTypePlan, "the secret body", "")
	if err != nil {
		t.Fatalf("create doc: %v", err)
	}
	if _, err := s.LinkDocument(doc.ID, store.LinkTarget{IssueID: &iss.ID}, "context"); err != nil {
		t.Fatalf("link: %v", err)
	}
	resp, body := apiGet(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/brief?no_doc_content=true")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if strings.Contains(string(body), "the secret body") {
		t.Fatalf("body leaked with no_doc_content=true: %s", body)
	}
	if !strings.Contains(string(body), "iss-doc.md") {
		t.Fatalf("filename should remain: %s", body)
	}
	if !strings.Contains(string(body), `"linked_via"`) {
		t.Fatalf("linked_via should remain: %s", body)
	}
}

// TestIssueBriefDocContentIncludedByDefault covers the BACI-115 inline
// rule end-to-end: plan + review doc bodies are inlined; transcript
// and other types surface metadata + size_bytes only.
func TestIssueBriefDocContentIncludedByDefault(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "x")
	// A plan doc — body MUST be inlined.
	planDoc, err := s.CreateDocument(repo.ID, "iss-plan.md", model.DocTypePlan, "the plan body", "")
	if err != nil {
		t.Fatalf("create plan doc: %v", err)
	}
	if _, err := s.LinkDocument(planDoc.ID, store.LinkTarget{IssueID: &iss.ID}, ""); err != nil {
		t.Fatalf("link plan: %v", err)
	}
	// A review doc — body MUST be inlined.
	reviewDoc, err := s.CreateDocument(repo.ID, "iss-review.md", model.DocTypeReview, "the review body", "")
	if err != nil {
		t.Fatalf("create review doc: %v", err)
	}
	if _, err := s.LinkDocument(reviewDoc.ID, store.LinkTarget{IssueID: &iss.ID}, ""); err != nil {
		t.Fatalf("link review: %v", err)
	}
	// A transcript doc — body MUST NOT be inlined.
	trDoc, err := s.CreateDocument(repo.ID, "bacio-transcript-MINI-1-agent-x.jsonl", model.DocTypeTranscript, "transcript body bytes", "")
	if err != nil {
		t.Fatalf("create transcript doc: %v", err)
	}
	if _, err := s.LinkDocument(trDoc.ID, store.LinkTarget{IssueID: &iss.ID}, ""); err != nil {
		t.Fatalf("link transcript: %v", err)
	}
	// An `architecture` doc — body MUST NOT be inlined (only plan/review are).
	archDoc, err := s.CreateDocument(repo.ID, "arch-notes.md", model.DocTypeArchitecture, "arch body words", "")
	if err != nil {
		t.Fatalf("create arch doc: %v", err)
	}
	if _, err := s.LinkDocument(archDoc.ID, store.LinkTarget{IssueID: &iss.ID}, ""); err != nil {
		t.Fatalf("link arch: %v", err)
	}
	resp, body := apiGet(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/brief")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	s_ := string(body)
	for _, want := range []string{
		"the plan body",
		"the review body",
		// Filenames + size_bytes for every doc, including the omitted ones.
		"iss-plan.md", "iss-review.md",
		"bacio-transcript-MINI-1-agent-x.jsonl", "arch-notes.md",
		`"size_bytes"`,
	} {
		if !strings.Contains(s_, want) {
			t.Errorf("expected %q in brief: %s", want, s_)
		}
	}
	for _, omit := range []string{
		"transcript body bytes",
		"arch body words",
	} {
		if strings.Contains(s_, omit) {
			t.Errorf("unexpectedly inlined %q in brief: %s", omit, s_)
		}
	}
}

// TestIssueBriefLegacyTranscriptFilenameFallback covers the read-side
// filename fallback (BACI-115): a transcript attached before the
// `transcript` type existed is still typed `project_complete` in the
// DB. The brief must NOT inline it — the filename pattern guard kicks
// in regardless of the stored type.
func TestIssueBriefLegacyTranscriptFilenameFallback(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "x")
	doc, err := s.CreateDocument(repo.ID, "bacio-transcript-MINI-1-agent-legacy.jsonl", model.DocTypeProjectComplete, "legacy transcript body", "")
	if err != nil {
		t.Fatalf("create legacy transcript doc: %v", err)
	}
	if _, err := s.LinkDocument(doc.ID, store.LinkTarget{IssueID: &iss.ID}, ""); err != nil {
		t.Fatalf("link: %v", err)
	}
	resp, body := apiGet(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/brief")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	s_ := string(body)
	if strings.Contains(s_, "legacy transcript body") {
		t.Fatalf("legacy transcript body leaked through brief: %s", s_)
	}
	if !strings.Contains(s_, "bacio-transcript-MINI-1-agent-legacy.jsonl") {
		t.Fatalf("transcript filename missing from brief metadata: %s", s_)
	}
}

func TestIssueBriefUnknownIssue(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	resp, _ := apiGet(t, ts.URL+"/repos/MINI/issues/MINI-999/brief")
	if resp.StatusCode != 404 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestIssueBriefCrossRepo(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo1 := seedRepo(t, s)
	repo2 := seedRepo2(t, s)
	iss := seedIssue(t, s, repo2, "x")
	_ = repo1
	resp, _ := apiGet(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/brief")
	if resp.StatusCode != 404 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestIssueBriefNoHistoryWritten(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "x")
	apiGet(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/brief")
	assertHistoryOps(t, s, nil)
}
