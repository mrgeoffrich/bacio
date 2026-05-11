package api_test

import (
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/api"
	"github.com/mrgeoffrich/bacio/internal/model"
)

func TestFeaturePlanEmpty(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedFeature(t, s, repo, "auth", "Auth")
	resp, body := apiGet(t, ts.URL+"/repos/MINI/features/auth/plan")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	for _, want := range []string{`"feature": "auth"`, `"order": []`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("missing %s in: %s", want, body)
		}
	}
}

func TestFeaturePlanOrdered(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	feat := seedFeature(t, s, repo, "auth", "Auth")
	a, _ := s.CreateIssue(repo.ID, &feat.ID, "first", "", model.StateTodo, nil)
	b, _ := s.CreateIssue(repo.ID, &feat.ID, "second", "", model.StateTodo, nil)
	// b blocks a -> b must precede a in order. CreateRelation(from=b, to=a, blocks)
	// means b.blocks.a, so a is blocked-by b.
	if err := s.CreateRelation(b.ID, a.ID, model.RelBlocks); err != nil {
		t.Fatalf("relation: %v", err)
	}
	resp, body := apiGet(t, ts.URL+"/repos/MINI/features/auth/plan")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	bodyS := string(body)
	posB := strings.Index(bodyS, b.Key)
	posA := strings.Index(bodyS, a.Key)
	if posB < 0 || posA < 0 || posB >= posA {
		t.Fatalf("blocker %s should precede %s in: %s", b.Key, a.Key, bodyS)
	}
	if !strings.Contains(bodyS, `"blocked_by"`) {
		t.Fatalf("expected blocked_by in: %s", bodyS)
	}
}

func TestFeaturePlanSkipsClosedIssues(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	feat := seedFeature(t, s, repo, "auth", "Auth")
	open, _ := s.CreateIssue(repo.ID, &feat.ID, "open", "", model.StateTodo, nil)
	closed, _ := s.CreateIssue(repo.ID, &feat.ID, "closed", "", model.StateDone, nil)
	resp, body := apiGet(t, ts.URL+"/repos/MINI/features/auth/plan")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), open.Key) {
		t.Fatalf("expected %s in plan: %s", open.Key, body)
	}
	if strings.Contains(string(body), closed.Key) {
		t.Fatalf("closed issue %s leaked into plan: %s", closed.Key, body)
	}
}

func TestFeaturePlanSlugNotFound(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	resp, _ := apiGet(t, ts.URL+"/repos/MINI/features/nope/plan")
	if resp.StatusCode != 404 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestFeaturePlanReadDoesNotWriteHistory(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedFeature(t, s, repo, "auth", "Auth")
	apiGet(t, ts.URL+"/repos/MINI/features/auth/plan")
	assertHistoryOps(t, s, nil)
}
