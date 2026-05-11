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
	iss, _ := s.CreateIssue(repo.ID, &feat.ID, "x", "", model.StateBacklog, nil)
	if _, err := s.CreateComment(iss.ID, "alice", "first comment"); err != nil {
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
	if _, err := s.CreateComment(iss.ID, "alice", "should be skipped"); err != nil {
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
	iss, _ := s.CreateIssue(repo.ID, &feat.ID, "x", "", model.StateBacklog, nil)
	doc, err := s.CreateDocument(repo.ID, "feat-doc.md", model.DocTypeArchitecture, "feature body content", "")
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
	doc, err := s.CreateDocument(repo.ID, "iss-doc.md", model.DocTypeArchitecture, "the secret body", "")
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

func TestIssueBriefDocContentIncludedByDefault(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "x")
	doc, err := s.CreateDocument(repo.ID, "iss-doc.md", model.DocTypeArchitecture, "the body", "")
	if err != nil {
		t.Fatalf("create doc: %v", err)
	}
	if _, err := s.LinkDocument(doc.ID, store.LinkTarget{IssueID: &iss.ID}, ""); err != nil {
		t.Fatalf("link: %v", err)
	}
	resp, body := apiGet(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/brief")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "the body") {
		t.Fatalf("expected doc content inline: %s", body)
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
