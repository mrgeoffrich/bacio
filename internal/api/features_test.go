package api_test

import (
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/api"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

func TestFeaturesListEmpty(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	resp, body := apiGet(t, ts.URL+"/repos/MINI/features")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	if strings.TrimSpace(string(body)) != "[]" {
		t.Fatalf("expected [], got %q", body)
	}
}

func TestFeaturesListRepoNotFound(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp, _ := apiGet(t, ts.URL+"/repos/NONE/features")
	if resp.StatusCode != 404 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestFeaturesListLeanByDefault(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	if _, err := s.CreateFeature(repo.ID, "auth", "Auth", "very long body", ""); err != nil {
		t.Fatalf("create: %v", err)
	}
	resp, body := apiGet(t, ts.URL+"/repos/MINI/features")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	if strings.Contains(string(body), "very long body") {
		t.Fatalf("description leaked without with_description: %s", body)
	}
	resp2, body2 := apiGet(t, ts.URL+"/repos/MINI/features?with_description=true")
	if resp2.StatusCode != 200 {
		t.Fatalf("status: %d", resp2.StatusCode)
	}
	if !strings.Contains(string(body2), "very long body") {
		t.Fatalf("description missing with with_description=true: %s", body2)
	}
}

func TestFeaturesListWithDescription1(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	if _, err := s.CreateFeature(repo.ID, "auth", "Auth", "the body", ""); err != nil {
		t.Fatalf("create: %v", err)
	}
	resp, body := apiGet(t, ts.URL+"/repos/MINI/features?with_description=1")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "the body") {
		t.Fatalf("description missing with with_description=1: %s", body)
	}
}

func TestFeatureShowHappy(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	feat := seedFeature(t, s, repo, "auth", "Auth")
	if _, err := s.CreateIssue(repo.ID, &feat.ID, "an issue", "", model.StateTodo, nil); err != nil {
		t.Fatalf("create issue: %v", err)
	}
	resp, body := apiGet(t, ts.URL+"/repos/MINI/features/auth")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	for _, want := range []string{`"feature"`, `"issues"`, `"documents"`, `"an issue"`, `"slug": "auth"`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("missing %s in: %s", want, body)
		}
	}
}

func TestFeatureShowEmptyArrays(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedFeature(t, s, repo, "empty", "Empty")
	resp, body := apiGet(t, ts.URL+"/repos/MINI/features/empty")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"issues": []`) || !strings.Contains(string(body), `"documents": []`) {
		t.Fatalf("expected [] arrays, got: %s", body)
	}
}

func TestFeatureShowSlugNotFound(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	resp, _ := apiGet(t, ts.URL+"/repos/MINI/features/nope")
	if resp.StatusCode != 404 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestFeatureShowCrossRepoNotFound(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo1 := seedRepo(t, s)
	repo2 := seedRepo2(t, s)
	seedFeature(t, s, repo2, "auth", "Auth")
	_ = repo1
	resp, _ := apiGet(t, ts.URL+"/repos/MINI/features/auth")
	if resp.StatusCode != 404 {
		t.Fatalf("expected 404 cross-repo, got %d", resp.StatusCode)
	}
}

func TestFeaturesReadDoesNotWriteHistory(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedFeature(t, s, repo, "auth", "Auth")
	apiGet(t, ts.URL+"/repos/MINI/features")
	apiGet(t, ts.URL+"/repos/MINI/features/auth")
	assertHistoryOps(t, s, nil)
}

// TestAPI_PutFeatureHide (BACI-177) round-trips the per-feature "Show
// on board" toggle through the REST endpoint:
//   - GET /repos/{p}/features/hidden returns the empty set at first
//   - PUT /repos/{p}/features/{slug}/hide with {hidden:true} flips it
//   - GET reflects the new state and stamps a feature.hide audit row
//   - PUT with {hidden:false} clears it and stamps feature.unhide
//   - the feature show endpoint surfaces hidden_on_board=true while hidden
func TestAPI_PutFeatureHide(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedFeature(t, s, repo, "auth", "Auth")

	// Initially nothing hidden.
	resp, body := apiGet(t, ts.URL+"/repos/MINI/features/hidden")
	if resp.StatusCode != 200 {
		t.Fatalf("GET hidden: %d, body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"slugs": []`) && !strings.Contains(string(body), `"slugs":[]`) {
		t.Fatalf("expected empty slugs, got %s", body)
	}

	// Flip on.
	resp, body = apiPut(t, ts.URL+"/repos/MINI/features/auth/hide", map[string]any{"hidden": true})
	if resp.StatusCode != 200 {
		t.Fatalf("PUT hide=true: %d, body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"hidden": true`) && !strings.Contains(string(body), `"hidden":true`) {
		t.Fatalf("PUT response missing hidden:true, got %s", body)
	}

	// GET reflects the new state.
	resp, body = apiGet(t, ts.URL+"/repos/MINI/features/hidden")
	if resp.StatusCode != 200 {
		t.Fatalf("GET hidden (after set): %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), `"auth"`) {
		t.Fatalf("auth missing from hidden slugs: %s", body)
	}

	// Feature show carries the derived flag.
	resp, body = apiGet(t, ts.URL+"/repos/MINI/features/auth")
	if resp.StatusCode != 200 {
		t.Fatalf("GET feature show: %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), `"hidden_on_board": true`) && !strings.Contains(string(body), `"hidden_on_board":true`) {
		t.Fatalf("feature show missing hidden_on_board:true, got %s", body)
	}

	// Audit row for the flip.
	assertHistoryOps(t, s, []string{"feature.hide"})

	// Flip off.
	resp, _ = apiPut(t, ts.URL+"/repos/MINI/features/auth/hide", map[string]any{"hidden": false})
	if resp.StatusCode != 200 {
		t.Fatalf("PUT hide=false: %d", resp.StatusCode)
	}
	assertHistoryOps(t, s, []string{"feature.hide", "feature.unhide"})

	// GET back to empty.
	resp, body = apiGet(t, ts.URL+"/repos/MINI/features/hidden")
	if resp.StatusCode != 200 {
		t.Fatalf("GET hidden (after unset): %d", resp.StatusCode)
	}
	if strings.Contains(string(body), `"auth"`) {
		t.Fatalf("auth still in hidden slugs after unset: %s", body)
	}
}

// TestAPI_PutFeatureHide_MissingHiddenField rejects a request with no
// hidden field — pointer-of-bool with no default catches the absent
// case rather than silently setting hidden=false.
func TestAPI_PutFeatureHide_MissingHiddenField(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedFeature(t, s, repo, "auth", "Auth")
	resp, body := apiPut(t, ts.URL+"/repos/MINI/features/auth/hide", map[string]any{})
	if resp.StatusCode != 400 {
		t.Fatalf("expected 400 for missing hidden, got %d, body=%s", resp.StatusCode, body)
	}
}

// TestAPI_PutFeatureHide_IdempotentSkipsAudit pins that flipping to the
// current state is a no-op write and produces no audit row.
func TestAPI_PutFeatureHide_IdempotentSkipsAudit(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedFeature(t, s, repo, "auth", "Auth")
	// Default is visible. PUT hidden=false again — should be a no-op.
	resp, _ := apiPut(t, ts.URL+"/repos/MINI/features/auth/hide", map[string]any{"hidden": false})
	if resp.StatusCode != 200 {
		t.Fatalf("PUT no-op: %d", resp.StatusCode)
	}
	assertHistoryOps(t, s, nil)
}

// TestAPI_BoardCards_HidesHiddenFeatures (BACI-177) hides a feature
// and then checks that the cards endpoint drops every card belonging
// to that feature. Mirrors what the React Board sees after a toggle.
func TestAPI_BoardCards_HidesHiddenFeatures(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	authFeat := seedFeature(t, s, repo, "auth", "Auth")
	opsFeat := seedFeature(t, s, repo, "ops", "Ops")
	if _, err := s.CreateIssue(repo.ID, &authFeat.ID, "auth-issue", "", model.StateTodo, nil); err != nil {
		t.Fatalf("create auth issue: %v", err)
	}
	if _, err := s.CreateIssue(repo.ID, &opsFeat.ID, "ops-issue", "", model.StateTodo, nil); err != nil {
		t.Fatalf("create ops issue: %v", err)
	}
	if _, err := s.CreateIssue(repo.ID, nil, "unattached-issue", "", model.StateTodo, nil); err != nil {
		t.Fatalf("create unattached issue: %v", err)
	}

	// Default: every card shows.
	resp, body := apiGet(t, ts.URL+"/repos/MINI/cards")
	if resp.StatusCode != 200 {
		t.Fatalf("GET cards: %d, body=%s", resp.StatusCode, body)
	}
	for _, want := range []string{"auth-issue", "ops-issue", "unattached-issue"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("expected %q in default board: %s", want, body)
		}
	}

	// Hide auth.
	resp, _ = apiPut(t, ts.URL+"/repos/MINI/features/auth/hide", map[string]any{"hidden": true})
	if resp.StatusCode != 200 {
		t.Fatalf("PUT hide: %d", resp.StatusCode)
	}

	// auth-issue gone; ops-issue + unattached stay.
	resp, body = apiGet(t, ts.URL+"/repos/MINI/cards")
	if resp.StatusCode != 200 {
		t.Fatalf("GET cards (after hide): %d", resp.StatusCode)
	}
	if strings.Contains(string(body), "auth-issue") {
		t.Fatalf("auth-issue still in board after hiding auth: %s", body)
	}
	for _, want := range []string{"ops-issue", "unattached-issue"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("expected %q to remain visible: %s", want, body)
		}
	}

	// Also pin /repos/MINI/issues (no feature filter) drops the auth one.
	resp, body = apiGet(t, ts.URL+"/repos/MINI/issues")
	if resp.StatusCode != 200 {
		t.Fatalf("GET issues: %d", resp.StatusCode)
	}
	if strings.Contains(string(body), "auth-issue") {
		t.Fatalf("auth-issue still in /issues after hide: %s", body)
	}

	// But a feature-scoped query (?feature=auth) still returns the auth
	// issue — the explicit ask short-circuits the global hide filter.
	resp, body = apiGet(t, ts.URL+"/repos/MINI/issues?feature=auth")
	if resp.StatusCode != 200 {
		t.Fatalf("GET issues?feature=auth: %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "auth-issue") {
		t.Fatalf("?feature=auth missing auth-issue (the explicit ask should bypass hide): %s", body)
	}
}

func TestFeatureCreateHappy(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	resp, body := apiPost(t, ts.URL+"/repos/MINI/features",
		`{"title":"Auth Rewrite","slug":"auth-rewrite","description":"long body"}`)
	if resp.StatusCode != 201 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	for _, want := range []string{`"slug": "auth-rewrite"`, `"title": "Auth Rewrite"`, `"description": "long body"`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("missing %s in: %s", want, body)
		}
	}
	assertHistoryOps(t, s, []string{"feature.create"})
}

func TestFeatureCreateSlugAutoDerived(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	resp, body := apiPost(t, ts.URL+"/repos/MINI/features",
		`{"title":"Auth Rewrite"}`)
	if resp.StatusCode != 201 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"slug": "auth-rewrite"`) {
		t.Fatalf("expected derived slug auth-rewrite in: %s", body)
	}
}

func TestFeatureCreateTitleRequired(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	resp, _ := apiPost(t, ts.URL+"/repos/MINI/features", `{}`)
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	assertHistoryOps(t, s, nil)
}

func TestFeatureCreateUnknownField(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	resp, _ := apiPost(t, ts.URL+"/repos/MINI/features",
		`{"title":"x","mystery":1}`)
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestFeatureCreateSlugConflict(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedFeature(t, s, repo, "auth", "Auth")
	resp, _ := apiPost(t, ts.URL+"/repos/MINI/features",
		`{"title":"Other","slug":"auth"}`)
	if resp.StatusCode != 409 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestFeatureCreateRepoNotFound(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp, _ := apiPost(t, ts.URL+"/repos/NONE/features", `{"title":"x"}`)
	if resp.StatusCode != 404 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestFeatureCreateDryRunQuery(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	resp, body := apiPost(t, ts.URL+"/repos/MINI/features?dry_run=true",
		`{"title":"Auth"}`)
	if resp.StatusCode != 201 || resp.Header.Get("X-Dry-Run") != "applied" {
		t.Fatalf("status: %d, header=%q", resp.StatusCode, resp.Header.Get("X-Dry-Run"))
	}
	if !strings.Contains(string(body), `"slug": "auth"`) {
		t.Fatalf("expected projected slug auth in: %s", body)
	}
	assertHistoryOps(t, s, nil)
}

func TestFeatureCreateDryRunHeader(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	resp, _ := apiReq(t, "POST", ts.URL+"/repos/MINI/features",
		`{"title":"Hdr"}`, map[string]string{"X-Dry-Run": "1"})
	if resp.StatusCode != 201 || resp.Header.Get("X-Dry-Run") != "applied" {
		t.Fatalf("status: %d, header=%q", resp.StatusCode, resp.Header.Get("X-Dry-Run"))
	}
	assertHistoryOps(t, s, nil)
}

func TestFeatureEditTitleOnly(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedFeature(t, s, repo, "auth", "Old")
	resp, body := apiPatch(t, ts.URL+"/repos/MINI/features/auth",
		`{"title":"New"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"title": "New"`) {
		t.Fatalf("body: %s", body)
	}
	assertHistoryOps(t, s, []string{"feature.update"})
}

func TestFeatureEditDescriptionOnly(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	if _, err := s.CreateFeature(repo.ID, "auth", "Auth", "had body", ""); err != nil {
		t.Fatalf("create: %v", err)
	}
	resp, body := apiPatch(t, ts.URL+"/repos/MINI/features/auth",
		`{"description":"new body"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"description": "new body"`) {
		t.Fatalf("body: %s", body)
	}
}

func TestFeatureEditBoth(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedFeature(t, s, repo, "auth", "Old")
	resp, body := apiPatch(t, ts.URL+"/repos/MINI/features/auth",
		`{"title":"New","description":"new body"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"title": "New"`) ||
		!strings.Contains(string(body), `"description": "new body"`) {
		t.Fatalf("body: %s", body)
	}
}

func TestFeatureEditNullDescriptionClears(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	if _, err := s.CreateFeature(repo.ID, "auth", "Auth", "had body", ""); err != nil {
		t.Fatalf("create: %v", err)
	}
	resp, body := apiPatch(t, ts.URL+"/repos/MINI/features/auth",
		`{"description":null}`)
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	if strings.Contains(string(body), "had body") {
		t.Fatalf("description not cleared: %s", body)
	}
}

func TestFeatureEditTitleEmptyRejected(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedFeature(t, s, repo, "auth", "Auth")
	resp, _ := apiPatch(t, ts.URL+"/repos/MINI/features/auth", `{"title":""}`)
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestFeatureEditTitleNullRejected(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedFeature(t, s, repo, "auth", "Auth")
	resp, _ := apiPatch(t, ts.URL+"/repos/MINI/features/auth", `{"title":null}`)
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestFeatureEditNothingToUpdate(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedFeature(t, s, repo, "auth", "Auth")
	resp, _ := apiPatch(t, ts.URL+"/repos/MINI/features/auth", `{}`)
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestFeatureEditUnknownField(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedFeature(t, s, repo, "auth", "Auth")
	resp, _ := apiPatch(t, ts.URL+"/repos/MINI/features/auth",
		`{"title":"x","mystery":1}`)
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestFeatureEditDryRun(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedFeature(t, s, repo, "auth", "Auth")
	resp, body := apiPatch(t, ts.URL+"/repos/MINI/features/auth?dry_run=true",
		`{"title":"projected"}`)
	if resp.StatusCode != 200 || resp.Header.Get("X-Dry-Run") != "applied" {
		t.Fatalf("status: %d, header=%q", resp.StatusCode, resp.Header.Get("X-Dry-Run"))
	}
	if !strings.Contains(string(body), `"title": "projected"`) {
		t.Fatalf("body: %s", body)
	}
	assertHistoryOps(t, s, nil)
	roundtrip, _ := s.GetFeatureBySlug(repo.ID, "auth")
	if roundtrip.Title != "Auth" {
		t.Fatalf("title was actually changed: %s", roundtrip.Title)
	}
}

func TestFeatureEditURLSlugWins(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedFeature(t, s, repo, "auth", "Auth")
	seedFeature(t, s, repo, "billing", "Billing")
	resp, _ := apiPatch(t, ts.URL+"/repos/MINI/features/auth",
		`{"slug":"billing","title":"renamed"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	updatedAuth, _ := s.GetFeatureBySlug(repo.ID, "auth")
	updatedBilling, _ := s.GetFeatureBySlug(repo.ID, "billing")
	if updatedAuth.Title != "renamed" || updatedBilling.Title == "renamed" {
		t.Fatalf("URL slug did not win: auth=%s billing=%s", updatedAuth.Title, updatedBilling.Title)
	}
}

func TestFeatureEditNotFound(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	resp, _ := apiPatch(t, ts.URL+"/repos/MINI/features/nope", `{"title":"x"}`)
	if resp.StatusCode != 404 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestFeatureDeleteHappy(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedFeature(t, s, repo, "auth", "Captured Title")
	resp, body := apiDelete(t, ts.URL+"/repos/MINI/features/auth", nil)
	if resp.StatusCode != 204 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	if len(body) != 0 {
		t.Fatalf("body should be empty: %q", body)
	}
	assertHistoryOps(t, s, []string{"feature.delete"})
	rows, _ := s.ListHistory(store.HistoryFilter{OldestFirst: true})
	if rows[0].Details != "Captured Title" {
		t.Fatalf("audit details: %q", rows[0].Details)
	}
}

func TestFeatureDeleteDryRunCascade(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	feat := seedFeature(t, s, repo, "auth", "Auth")
	if _, err := s.CreateIssue(repo.ID, &feat.ID, "child", "", model.StateTodo, nil); err != nil {
		t.Fatalf("create issue: %v", err)
	}
	resp, body := apiDelete(t, ts.URL+"/repos/MINI/features/auth?dry_run=true", nil)
	if resp.StatusCode != 200 || resp.Header.Get("X-Dry-Run") != "applied" {
		t.Fatalf("status: %d, header=%q", resp.StatusCode, resp.Header.Get("X-Dry-Run"))
	}
	for _, want := range []string{
		`"would_delete": true`,
		`"issues_unlinked": 1`,
		`"document_links": 0`,
	} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("missing %s in: %s", want, body)
		}
	}
	assertHistoryOps(t, s, nil)
	roundtrip, err := s.GetFeatureBySlug(repo.ID, "auth")
	if err != nil || roundtrip == nil {
		t.Fatalf("feature was actually deleted: %v", err)
	}
}

func TestFeatureDeleteNotFound(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	resp, _ := apiDelete(t, ts.URL+"/repos/MINI/features/nope", nil)
	if resp.StatusCode != 404 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}
