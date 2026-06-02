package api_test

import (
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/api"
)

// TestFeatureCommentsListEmpty pins the empty-list contract: `GET .../comments`
// on a feature with none returns [].
func TestFeatureCommentsListEmpty(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	feat := seedFeature(t, s, repo, "auth", "Auth")
	resp, body := apiGet(t, ts.URL+"/repos/MINI/features/"+feat.Slug+"/comments")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if strings.TrimSpace(string(body)) != "[]" {
		t.Fatalf("expected [], got %s", body)
	}
}

// TestFeatureCommentAddHappy posts a comment and asserts the row + the
// feature_comment.add audit op.
func TestFeatureCommentAddHappy(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	feat := seedFeature(t, s, repo, "auth", "Auth")
	resp, body := apiPost(t, ts.URL+"/repos/MINI/features/"+feat.Slug+"/comments",
		`{"author":"alice","body":"## Handoff\n\nNotes."}`)
	if resp.StatusCode != 201 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	for _, want := range []string{`"author": "alice"`, `"## Handoff`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("missing %s in: %s", want, body)
		}
	}
	assertHistoryOps(t, s, []string{"feature_comment.add"})
}

// TestFeatureCommentAddDryRun pins the dry-run contract — no row, no audit.
func TestFeatureCommentAddDryRun(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	feat := seedFeature(t, s, repo, "auth", "Auth")
	resp, body := apiPost(t, ts.URL+"/repos/MINI/features/"+feat.Slug+"/comments?dry_run=true",
		`{"author":"alice","body":"hello"}`)
	if resp.StatusCode != 201 || resp.Header.Get("X-Dry-Run") != "applied" {
		t.Fatalf("status: %d, header=%q", resp.StatusCode, resp.Header.Get("X-Dry-Run"))
	}
	if !strings.Contains(string(body), `"body": "hello"`) {
		t.Fatalf("body: %s", body)
	}
	got, _ := s.ListFeatureComments(feat.ID)
	if len(got) != 0 {
		t.Fatalf("dry-run wrote a row: %d", len(got))
	}
	assertHistoryOps(t, s, nil)
}

func TestFeatureCommentAddMissingAuthor(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	feat := seedFeature(t, s, repo, "auth", "Auth")
	resp, _ := apiPost(t, ts.URL+"/repos/MINI/features/"+feat.Slug+"/comments",
		`{"body":"hello"}`)
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestFeatureCommentAddMissingBody(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	feat := seedFeature(t, s, repo, "auth", "Auth")
	resp, _ := apiPost(t, ts.URL+"/repos/MINI/features/"+feat.Slug+"/comments",
		`{"author":"alice"}`)
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestFeatureCommentDeleteHappy(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	feat := seedFeature(t, s, repo, "auth", "Auth")
	res, err := s.CreateFeatureComment(feat.ID, "alice", "hi", "")
	if err != nil {
		t.Fatalf("CreateFeatureComment: %v", err)
	}
	cm := res.Comment
	resp, body := apiDelete(t, ts.URL+"/repos/MINI/features/"+feat.Slug+"/comments/"+cm.UUID, nil)
	if resp.StatusCode != 204 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	got, _ := s.ListFeatureComments(feat.ID)
	if len(got) != 0 {
		t.Fatalf("expected row gone, got %d", len(got))
	}
	assertHistoryOps(t, s, []string{"feature_comment.delete"})
}

func TestFeatureCommentDeleteDryRun(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	feat := seedFeature(t, s, repo, "auth", "Auth")
	res, err := s.CreateFeatureComment(feat.ID, "alice", "hi", "")
	if err != nil {
		t.Fatalf("CreateFeatureComment: %v", err)
	}
	cm := res.Comment
	resp, body := apiDelete(t, ts.URL+"/repos/MINI/features/"+feat.Slug+"/comments/"+cm.UUID+"?dry_run=1", nil)
	if resp.StatusCode != 200 || resp.Header.Get("X-Dry-Run") != "applied" {
		t.Fatalf("status: %d, header=%q, body=%s", resp.StatusCode, resp.Header.Get("X-Dry-Run"), body)
	}
	if !strings.Contains(string(body), `"would_remove": 1`) {
		t.Fatalf("body: %s", body)
	}
	got, _ := s.ListFeatureComments(feat.ID)
	if len(got) != 1 {
		t.Fatalf("dry-run deleted the row: %d", len(got))
	}
	assertHistoryOps(t, s, nil)
}

func TestFeatureCommentDeleteUnknownUUID(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	feat := seedFeature(t, s, repo, "auth", "Auth")
	resp, _ := apiDelete(t, ts.URL+"/repos/MINI/features/"+feat.Slug+"/comments/019e0000-0000-7000-8000-000000000000", nil)
	if resp.StatusCode != 404 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

// TestFeatureCommentDeleteWrongFeature ensures a comment on feature A can't
// be removed by addressing it through feature B's slug — the handler
// 404s and the row survives.
func TestFeatureCommentDeleteWrongFeature(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	a := seedFeature(t, s, repo, "auth", "Auth")
	b := seedFeature(t, s, repo, "billing", "Billing")
	res, err := s.CreateFeatureComment(a.ID, "alice", "hi", "")
	if err != nil {
		t.Fatalf("CreateFeatureComment: %v", err)
	}
	cm := res.Comment
	resp, _ := apiDelete(t, ts.URL+"/repos/MINI/features/"+b.Slug+"/comments/"+cm.UUID, nil)
	if resp.StatusCode != 404 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	got, _ := s.ListFeatureComments(a.ID)
	if len(got) != 1 {
		t.Fatalf("comment on wrong feature was deleted: %d", len(got))
	}
}

// TestFeatureShowSurfacesComments pins the BACI-124 read shape: the
// FeatureView response carries a `comments` array.
func TestFeatureShowSurfacesComments(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	feat := seedFeature(t, s, repo, "auth", "Auth")
	if _, err := s.CreateFeatureComment(feat.ID, "alice", "hi", ""); err != nil {
		t.Fatalf("CreateFeatureComment: %v", err)
	}
	resp, body := apiGet(t, ts.URL+"/repos/MINI/features/"+feat.Slug)
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), `"comments"`) {
		t.Fatalf("FeatureView missing comments field: %s", body)
	}
	if !strings.Contains(string(body), `"author": "alice"`) {
		t.Fatalf("comment row not surfaced in FeatureView: %s", body)
	}
}
