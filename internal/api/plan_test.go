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
	a, _ := s.CreateIssue(repo.ID, &feat.ID, "first", "", model.StateTodo, nil, "", "")
	b, _ := s.CreateIssue(repo.ID, &feat.ID, "second", "", model.StateTodo, nil, "", "")
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
	open, _ := s.CreateIssue(repo.ID, &feat.ID, "open", "", model.StateTodo, nil, "", "")
	closed, _ := s.CreateIssue(repo.ID, &feat.ID, "closed", "", model.StateDone, nil, "", "")
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

// TestFeaturePlanIncludeClosed pins the BACI-236 widening — when the
// handler is invoked with ?include_closed=1, every issue in the feature
// shows up (closed ones with `closed:true`) and the `blocks` edges that
// touch a closed endpoint are preserved.
func TestFeaturePlanIncludeClosed(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	feat := seedFeature(t, s, repo, "auth", "Auth")
	open, _ := s.CreateIssue(repo.ID, &feat.ID, "open work", "", model.StateTodo, nil, "", "")
	closed, _ := s.CreateIssue(repo.ID, &feat.ID, "delivered work", "", model.StateDone, nil, "", "")
	// closed blocks open — wire the edge so the graph view's "this
	// delivered work blocks that live work" arrow has something to
	// draw.
	if err := s.CreateRelation(closed.ID, open.ID, model.RelBlocks); err != nil {
		t.Fatalf("relation: %v", err)
	}

	// Default path: closed issue stays hidden, the edge is dropped.
	resp, body := apiGet(t, ts.URL+"/repos/MINI/features/auth/plan")
	if resp.StatusCode != 200 {
		t.Fatalf("default status: %d, body=%s", resp.StatusCode, body)
	}
	if strings.Contains(string(body), closed.Key) {
		t.Fatalf("default path leaked closed issue %s: %s", closed.Key, body)
	}

	// include_closed=1: closed issue surfaces with closed:true; the
	// open entry's blocked_by lists the closed key.
	resp, body = apiGet(t, ts.URL+"/repos/MINI/features/auth/plan?include_closed=1")
	if resp.StatusCode != 200 {
		t.Fatalf("included status: %d, body=%s", resp.StatusCode, body)
	}
	bodyS := string(body)
	if !strings.Contains(bodyS, closed.Key) {
		t.Fatalf("include_closed: expected closed key %s in: %s", closed.Key, bodyS)
	}
	if !strings.Contains(bodyS, open.Key) {
		t.Fatalf("include_closed: expected open key %s in: %s", open.Key, bodyS)
	}
	if !strings.Contains(bodyS, `"closed": true`) {
		t.Fatalf("include_closed: expected closed:true marker in: %s", bodyS)
	}
	// The blocker edge should survive — the open entry's blocked_by
	// references the closed key.
	posClosed := strings.Index(bodyS, `"key": "`+closed.Key+`"`)
	posOpen := strings.Index(bodyS, `"key": "`+open.Key+`"`)
	if posClosed < 0 || posOpen < 0 {
		t.Fatalf("include_closed: expected both keys present: %s", bodyS)
	}
	// Closed precedes open in topo order — it's the blocker.
	if posClosed >= posOpen {
		t.Fatalf("include_closed: closed blocker %s should precede %s: %s", closed.Key, open.Key, bodyS)
	}
	// The open issue's blocked_by must reference the closed key.
	openSlice := bodyS[posOpen:]
	if !strings.Contains(openSlice, closed.Key) {
		t.Fatalf("include_closed: open entry's blocked_by missing %s: %s", closed.Key, openSlice)
	}
}

// TestFeaturePlanIncludeClosedAccepts permissive values.
func TestFeaturePlanIncludeClosedFlagAccepted(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	feat := seedFeature(t, s, repo, "auth", "Auth")
	closed, _ := s.CreateIssue(repo.ID, &feat.ID, "delivered", "", model.StateDone, nil, "", "")

	// "true" should widen the payload too.
	resp, body := apiGet(t, ts.URL+"/repos/MINI/features/auth/plan?include_closed=true")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), closed.Key) {
		t.Fatalf("include_closed=true: expected %s in: %s", closed.Key, body)
	}

	// "0" / unknown should leave the default open-only shape in
	// place — historical behaviour preserved.
	resp, body = apiGet(t, ts.URL+"/repos/MINI/features/auth/plan?include_closed=0")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	if strings.Contains(string(body), closed.Key) {
		t.Fatalf("include_closed=0 should preserve open-only path: %s", body)
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
