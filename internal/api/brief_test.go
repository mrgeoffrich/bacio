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
	iss, _ := s.CreateIssue(repo.ID, &feat.ID, "x", "", model.StateTodo, nil, "")
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
	iss, _ := s.CreateIssue(repo.ID, &feat.ID, "x", "", model.StateTodo, nil, "")
	// BACI-203 strips every linked-doc body — the assertion below
	// checks the entire entry vanishes when no_feature_docs=1, not
	// just the body. DocTypePlan stays here so the seeded shape
	// matches the BACI-115 era for any other reader.
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

// TestIssueBriefDocContentStripped covers the BACI-203 rule that no
// linked-doc body is inlined in the brief — every doc surfaces
// filename + type + size_bytes + linked_via only. Replaces the
// BACI-115 plan/review-only carve-out: the LinkedDocPanel now renders
// a link to /documents/<filename> rather than embedding the body, so
// the brief stays slim even for plan/review docs.
func TestIssueBriefDocContentStripped(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "x")
	// Plan, review, transcript, and architecture docs — every body
	// must be stripped from the brief regardless of type.
	planDoc, err := s.CreateDocument(repo.ID, "iss-plan.md", model.DocTypePlan, "the plan body", "")
	if err != nil {
		t.Fatalf("create plan doc: %v", err)
	}
	if _, err := s.LinkDocument(planDoc.ID, store.LinkTarget{IssueID: &iss.ID}, ""); err != nil {
		t.Fatalf("link plan: %v", err)
	}
	reviewDoc, err := s.CreateDocument(repo.ID, "iss-review.md", model.DocTypeReview, "the review body", "")
	if err != nil {
		t.Fatalf("create review doc: %v", err)
	}
	if _, err := s.LinkDocument(reviewDoc.ID, store.LinkTarget{IssueID: &iss.ID}, ""); err != nil {
		t.Fatalf("link review: %v", err)
	}
	trDoc, err := s.CreateDocument(repo.ID, "bacio-transcript-MINI-1-agent-x.jsonl", model.DocTypeTranscript, "transcript body bytes", "")
	if err != nil {
		t.Fatalf("create transcript doc: %v", err)
	}
	if _, err := s.LinkDocument(trDoc.ID, store.LinkTarget{IssueID: &iss.ID}, ""); err != nil {
		t.Fatalf("link transcript: %v", err)
	}
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
	// Metadata MUST stay: filenames, size_bytes, linked_via.
	for _, want := range []string{
		"iss-plan.md", "iss-review.md",
		"bacio-transcript-MINI-1-agent-x.jsonl", "arch-notes.md",
		`"size_bytes"`,
		`"linked_via"`,
	} {
		if !strings.Contains(s_, want) {
			t.Errorf("expected %q in brief: %s", want, s_)
		}
	}
	// Every body MUST be stripped — including plan + review which the
	// BACI-115 carve-out had been keeping.
	for _, omit := range []string{
		"the plan body",
		"the review body",
		"transcript body bytes",
		"arch body words",
	} {
		if strings.Contains(s_, omit) {
			t.Errorf("body %q leaked through brief: %s", omit, s_)
		}
	}
}

// TestIssueBriefLatestPlan (BACI-216) covers the dedicated
// `latest_plan` field on the brief: absent when no plan is linked,
// populated with the doc's filename / uuid / updated_at when one
// is. Mirrors the per-issue brief lookup added to handleIssueBrief.
func TestIssueBriefLatestPlan(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "x")

	// Absent before any plan is linked.
	respNone, bodyNone := apiGet(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/brief")
	if respNone.StatusCode != 200 {
		t.Fatalf("status: %d, body=%s", respNone.StatusCode, bodyNone)
	}
	if strings.Contains(string(bodyNone), `"latest_plan"`) {
		t.Fatalf("latest_plan should be omitted when no plan is linked: %s", bodyNone)
	}

	// Link a plan-typed doc — brief must surface it.
	planDoc, err := s.CreateDocument(repo.ID, "iss-plan.md", model.DocTypePlan, "the plan body", "")
	if err != nil {
		t.Fatalf("create plan doc: %v", err)
	}
	if _, err := s.LinkDocument(planDoc.ID, store.LinkTarget{IssueID: &iss.ID}, ""); err != nil {
		t.Fatalf("link plan: %v", err)
	}
	respPlan, bodyPlan := apiGet(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/brief")
	if respPlan.StatusCode != 200 {
		t.Fatalf("status: %d, body=%s", respPlan.StatusCode, bodyPlan)
	}
	for _, want := range []string{
		`"latest_plan"`, `"filename": "iss-plan.md"`, `"document_id"`,
	} {
		if !strings.Contains(string(bodyPlan), want) {
			t.Fatalf("expected %q in brief with plan linked: %s", want, bodyPlan)
		}
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
