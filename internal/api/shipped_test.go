package api_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/mrgeoffrich/bacio/internal/api"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

func shipIssue(t *testing.T, s *store.Store, repo *model.Repo, title string) *model.Issue {
	t.Helper()
	iss := seedIssue(t, s, repo, title)
	if err := s.SetIssueState(iss.ID, model.StateDone); err != nil {
		t.Fatalf("ship %q: %v", title, err)
	}
	return iss
}

// TestHandleShippedEmpty — empty repo returns 200 {rows: [], total: 0},
// never null. The JS side relies on iterating rows unconditionally.
func TestHandleShippedEmpty(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)

	resp, raw := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/shipped")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body=%s", resp.StatusCode, raw)
	}
	var body api.ShippedListResponse
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode: %v body=%s", err, raw)
	}
	if body.Rows == nil {
		t.Fatalf("empty shipped list must be `[]`, got null")
	}
	if len(body.Rows) != 0 || body.Total != 0 {
		t.Fatalf("rows=%d total=%d, want 0/0", len(body.Rows), body.Total)
	}
}

// TestHandleShippedDefaultLimit — 25 done rows, default limit=20
// returns exactly 20 rows but total=25.
func TestHandleShippedDefaultLimit(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	for i := 0; i < 25; i++ {
		shipIssue(t, s, repo, "iss")
	}

	resp, raw := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/shipped")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body=%s", resp.StatusCode, raw)
	}
	var body api.ShippedListResponse
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Rows) != 20 {
		t.Fatalf("rows: %d, want default 20", len(body.Rows))
	}
	if body.Total != 25 {
		t.Fatalf("total: %d, want 25 (count is independent of limit)", body.Total)
	}
}

// TestHandleShippedSinceWindow — ?since=7d clamps the rows AND the
// total to the last 7 days. One row inside the window, one back-dated
// past it; only the recent row returns and total=1.
func TestHandleShippedSinceWindow(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)

	old := shipIssue(t, s, repo, "old")
	_ = shipIssue(t, s, repo, "young")
	if _, err := s.DB.Exec(`UPDATE issues SET terminal_at = datetime('now','-30 days') WHERE id = ?`, old.ID); err != nil {
		t.Fatalf("back-date: %v", err)
	}

	resp, raw := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/shipped?since=7d")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body=%s", resp.StatusCode, raw)
	}
	var body api.ShippedListResponse
	_ = json.Unmarshal(raw, &body)
	if len(body.Rows) != 1 || body.Total != 1 {
		t.Fatalf("rows=%d total=%d, want 1/1 (old row outside window)", len(body.Rows), body.Total)
	}
	if body.Rows[0].Title != "young" {
		t.Fatalf("row title = %q, want 'young'", body.Rows[0].Title)
	}
}

// TestHandleShippedLimitBound — ?limit=200 clamps rows to the API max
// (100); ?limit=-1 returns 400. Total is independent of limit so it
// reports the full 150 either way.
func TestHandleShippedLimitBound(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	for i := 0; i < 150; i++ {
		shipIssue(t, s, repo, "iss")
	}

	resp, raw := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/shipped?limit=200")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body=%s", resp.StatusCode, raw)
	}
	var body api.ShippedListResponse
	_ = json.Unmarshal(raw, &body)
	if len(body.Rows) != 100 {
		t.Fatalf("rows: %d, want clamp to API max 100", len(body.Rows))
	}
	if body.Total != 150 {
		t.Fatalf("total: %d, want 150 (count must not be clamped by limit)", body.Total)
	}

	resp, raw = apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/shipped?limit=-1")
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d (want 400 on limit=-1) body=%s", resp.StatusCode, raw)
	}
}

// TestHandleShippedPRChip — when an issue has multiple PRs, only the
// first (by insertion order) shows up as the row's PR chip.
func TestHandleShippedPRChip(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)

	iss := shipIssue(t, s, repo, "pred")
	if _, err := s.AttachPR(iss.ID, "https://example.com/pr/1"); err != nil {
		t.Fatalf("attach pr 1: %v", err)
	}
	if _, err := s.AttachPR(iss.ID, "https://example.com/pr/2"); err != nil {
		t.Fatalf("attach pr 2: %v", err)
	}

	resp, raw := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/shipped")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body=%s", resp.StatusCode, raw)
	}
	var body api.ShippedListResponse
	_ = json.Unmarshal(raw, &body)
	if len(body.Rows) != 1 {
		t.Fatalf("rows: %d, want 1", len(body.Rows))
	}
	if body.Rows[0].PRURL != "https://example.com/pr/1" {
		t.Fatalf("PR chip = %q, want first PR URL", body.Rows[0].PRURL)
	}
}

// TestHandleShippedCustomerImpact (BACI-349) confirms the shipped-list
// DTO carries customer_impact: a row with an impact set surfaces it, a
// row without one carries the empty string (the popover falls back to
// the title client-side).
func TestHandleShippedCustomerImpact(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)

	// One shipped issue with an impact, one without.
	withImpact, err := s.CreateIssue(repo.ID, nil, "fix login", "", model.StateTodo, nil, "", "Login no longer 500s on Safari")
	if err != nil {
		t.Fatalf("create with-impact: %v", err)
	}
	if err := s.SetIssueState(withImpact.ID, model.StateDone); err != nil {
		t.Fatalf("ship with-impact: %v", err)
	}
	shipIssue(t, s, repo, "internal refactor")

	resp, raw := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/shipped")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body=%s", resp.StatusCode, raw)
	}
	var body api.ShippedListResponse
	_ = json.Unmarshal(raw, &body)
	byKey := map[string]api.ShippedIssue{}
	for _, r := range body.Rows {
		byKey[r.Key] = r
	}
	if got := byKey[withImpact.Key].CustomerImpact; got != "Login no longer 500s on Safari" {
		t.Fatalf("with-impact row customerImpact = %q, want the Safari line", got)
	}
	// The refactor row shipped without an impact — its DTO field is empty.
	for k, r := range byKey {
		if k != withImpact.Key && r.CustomerImpact != "" {
			t.Fatalf("blank-impact row %s carried customerImpact = %q, want empty", k, r.CustomerImpact)
		}
	}
}

// TestHandleShippedNonExistentRepo — /repos/ZZZZ/shipped returns 404
// like every other per-repo route. The count sibling does too.
func TestHandleShippedNonExistentRepo(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})

	resp, _ := apiGet(t, ts.URL+"/repos/ZZZZ/shipped")
	if resp.StatusCode != 404 {
		t.Fatalf("status: %d, want 404 on unknown repo", resp.StatusCode)
	}
	resp, _ = apiGet(t, ts.URL+"/repos/ZZZZ/shipped/count")
	if resp.StatusCode != 404 {
		t.Fatalf("count status: %d, want 404 on unknown repo", resp.StatusCode)
	}
}

// TestHandleShippedNewestFirst — three done issues land in the order
// they were shipped (newest-first).
func TestHandleShippedNewestFirst(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)

	first := shipIssue(t, s, repo, "first")
	time.Sleep(1100 * time.Millisecond)
	second := shipIssue(t, s, repo, "second")
	time.Sleep(1100 * time.Millisecond)
	third := shipIssue(t, s, repo, "third")
	_ = first
	_ = second
	_ = third

	resp, raw := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/shipped")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body=%s", resp.StatusCode, raw)
	}
	var body api.ShippedListResponse
	_ = json.Unmarshal(raw, &body)
	if len(body.Rows) != 3 || body.Total != 3 {
		t.Fatalf("rows=%d total=%d, want 3/3", len(body.Rows), body.Total)
	}
	want := []string{"third", "second", "first"}
	for i, w := range want {
		if body.Rows[i].Title != w {
			t.Fatalf("rows[%d].Title = %q, want %q (newest-first)", i, body.Rows[i].Title, w)
		}
	}
}

// TestHandleShippedCountEmpty (BACI-221) — empty repo returns total=0
// without erroring; the count endpoint never returns null.
func TestHandleShippedCountEmpty(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)

	resp, raw := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/shipped/count")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body=%s", resp.StatusCode, raw)
	}
	var body api.ShippedCountResponse
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode: %v body=%s", err, raw)
	}
	if body.Total != 0 {
		t.Fatalf("total = %d, want 0 on empty repo", body.Total)
	}
}

// TestHandleShippedCountForever (BACI-221) — count without a ?since=
// scope returns every shipped row, archived rows included. The pre-
// BACI-221 cards-derived count couldn't represent "Forever"; the
// server-side count must.
func TestHandleShippedCountForever(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	for i := 0; i < 12; i++ {
		shipIssue(t, s, repo, "iss")
	}

	resp, raw := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/shipped/count")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body=%s", resp.StatusCode, raw)
	}
	var body api.ShippedCountResponse
	_ = json.Unmarshal(raw, &body)
	if body.Total != 12 {
		t.Fatalf("total = %d, want 12 (forever scope must count all)", body.Total)
	}
}

// TestHandleShippedCountWithSince (BACI-221) — ?since=24h cuts the
// count to the matching window. Pins the "Today" pill behaviour.
func TestHandleShippedCountWithSince(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)

	old := shipIssue(t, s, repo, "old")
	_ = shipIssue(t, s, repo, "fresh")
	if _, err := s.DB.Exec(`UPDATE issues SET terminal_at = datetime('now','-3 days') WHERE id = ?`, old.ID); err != nil {
		t.Fatalf("back-date: %v", err)
	}

	resp, raw := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/shipped/count?since=24h")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body=%s", resp.StatusCode, raw)
	}
	var body api.ShippedCountResponse
	_ = json.Unmarshal(raw, &body)
	if body.Total != 1 {
		t.Fatalf("total = %d, want 1 (old row outside window)", body.Total)
	}
}

// TestHandleShippedCountBadSince (BACI-221) — a malformed ?since=
// surfaces as a 400 with the same envelope shape /history uses, so the
// frontend can render the validation error inline.
func TestHandleShippedCountBadSince(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)

	resp, _ := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/shipped/count?since=notaduration")
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d, want 400 on bad ?since=", resp.StatusCode)
	}
}

// TestHandleShippedSinceTS (BACI-312) — ?since_ts=<RFC3339> is the
// absolute-cutoff path the local-midnight "Today" scope uses. An issue
// shipped before the absolute cutoff is excluded, one shipped after it
// is included, on both the list and the count endpoint. Pins that the
// cutoff is the supplied timestamp, not a rolling duration.
func TestHandleShippedSinceTS(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)

	old := shipIssue(t, s, repo, "old")
	_ = shipIssue(t, s, repo, "young")
	// Back-date the old row well before the cutoff; the young row keeps
	// its just-now terminal_at.
	if _, err := s.DB.Exec(`UPDATE issues SET terminal_at = datetime('now','-2 days') WHERE id = ?`, old.ID); err != nil {
		t.Fatalf("back-date: %v", err)
	}
	// Cutoff: one day ago, in RFC3339 / UTC. Excludes the 2-day-old row,
	// includes the fresh one.
	cutoff := time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)

	resp, raw := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/shipped?since_ts="+cutoff)
	if resp.StatusCode != 200 {
		t.Fatalf("list status: %d body=%s", resp.StatusCode, raw)
	}
	var list api.ShippedListResponse
	_ = json.Unmarshal(raw, &list)
	if len(list.Rows) != 1 || list.Total != 1 {
		t.Fatalf("list rows=%d total=%d, want 1/1 (old row before cutoff)", len(list.Rows), list.Total)
	}
	if list.Rows[0].Title != "young" {
		t.Fatalf("list row title = %q, want 'young'", list.Rows[0].Title)
	}

	resp, raw = apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/shipped/count?since_ts="+cutoff)
	if resp.StatusCode != 200 {
		t.Fatalf("count status: %d body=%s", resp.StatusCode, raw)
	}
	var count api.ShippedCountResponse
	_ = json.Unmarshal(raw, &count)
	if count.Total != 1 {
		t.Fatalf("count total = %d, want 1 (old row before cutoff)", count.Total)
	}
}

// TestHandleShippedSinceTSBoth (BACI-312) — supplying both ?since= and
// ?since_ts= is a 400; a request carries at most one cutoff.
func TestHandleShippedSinceTSBoth(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)

	cutoff := time.Now().UTC().Format(time.RFC3339)
	resp, _ := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/shipped?since=7d&since_ts="+cutoff)
	if resp.StatusCode != 400 {
		t.Fatalf("list status: %d, want 400 when both since and since_ts present", resp.StatusCode)
	}
	resp, _ = apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/shipped/count?since=7d&since_ts="+cutoff)
	if resp.StatusCode != 400 {
		t.Fatalf("count status: %d, want 400 when both since and since_ts present", resp.StatusCode)
	}
}

// TestHandleShippedSinceTSBad (BACI-312) — a malformed ?since_ts=
// surfaces as a 400, same envelope shape as ?since=.
func TestHandleShippedSinceTSBad(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)

	resp, _ := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/shipped/count?since_ts=notatimestamp")
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d, want 400 on bad ?since_ts=", resp.StatusCode)
	}
}

// TestHandleShippedAllRepos (BACI-371) — GET /shipped ignores repo_id,
// so rows from both repos come back newest-first and total spans both.
func TestHandleShippedAllRepos(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	other := seedRepo2(t, s)

	older := shipIssue(t, s, repo, "mine")
	_ = shipIssue(t, s, other, "theirs")
	if _, err := s.DB.Exec(`UPDATE issues SET terminal_at = datetime('now','-1 days') WHERE id = ?`, older.ID); err != nil {
		t.Fatalf("back-date: %v", err)
	}

	resp, raw := apiGet(t, ts.URL+"/shipped")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body=%s", resp.StatusCode, raw)
	}
	var body api.ShippedListResponse
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode: %v body=%s", err, raw)
	}
	if len(body.Rows) != 2 || body.Total != 2 {
		t.Fatalf("rows=%d total=%d, want 2/2 (both repos)", len(body.Rows), body.Total)
	}
	if body.Rows[0].Title != "theirs" || body.Rows[1].Title != "mine" {
		t.Fatalf("rows = [%q %q], want newest-first [theirs mine]", body.Rows[0].Title, body.Rows[1].Title)
	}
	// The per-repo route must still narrow — the popover's "this repo".
	resp, raw = apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/shipped")
	if resp.StatusCode != 200 {
		t.Fatalf("per-repo status: %d body=%s", resp.StatusCode, raw)
	}
	var scoped api.ShippedListResponse
	_ = json.Unmarshal(raw, &scoped)
	if len(scoped.Rows) != 1 || scoped.Rows[0].Title != "mine" {
		t.Fatalf("per-repo rows = %d, want only this repo's row", len(scoped.Rows))
	}
}

// TestHandleShippedCountAllRepos (BACI-371) — the cross-repo count is
// the sum across repos, strictly greater than either per-repo count.
func TestHandleShippedCountAllRepos(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	other := seedRepo2(t, s)
	for i := 0; i < 3; i++ {
		shipIssue(t, s, repo, "mine")
	}
	shipIssue(t, s, other, "theirs")

	resp, raw := apiGet(t, ts.URL+"/shipped/count")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body=%s", resp.StatusCode, raw)
	}
	var body api.ShippedCountResponse
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode: %v body=%s", err, raw)
	}
	if body.Total != 4 {
		t.Fatalf("total = %d, want 4 (3 + 1 across repos)", body.Total)
	}

	resp, raw = apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/shipped/count")
	if resp.StatusCode != 200 {
		t.Fatalf("per-repo status: %d body=%s", resp.StatusCode, raw)
	}
	var scoped api.ShippedCountResponse
	_ = json.Unmarshal(raw, &scoped)
	if scoped.Total != 3 {
		t.Fatalf("per-repo total = %d, want 3", scoped.Total)
	}
	if body.Total <= scoped.Total {
		t.Fatalf("cross-repo total %d must exceed per-repo total %d", body.Total, scoped.Total)
	}
}

// TestHandleShippedAllReposSinceWindow (BACI-371) — the cross-repo
// routes share parseShippedSince with their per-repo siblings, so the
// ?since= window clamps rows and count across repos too.
func TestHandleShippedAllReposSinceWindow(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	other := seedRepo2(t, s)

	old := shipIssue(t, s, other, "old")
	_ = shipIssue(t, s, repo, "young")
	if _, err := s.DB.Exec(`UPDATE issues SET terminal_at = datetime('now','-30 days') WHERE id = ?`, old.ID); err != nil {
		t.Fatalf("back-date: %v", err)
	}

	resp, raw := apiGet(t, ts.URL+"/shipped?since=7d")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body=%s", resp.StatusCode, raw)
	}
	var body api.ShippedListResponse
	_ = json.Unmarshal(raw, &body)
	if len(body.Rows) != 1 || body.Total != 1 {
		t.Fatalf("rows=%d total=%d, want 1/1 (old cross-repo row outside window)", len(body.Rows), body.Total)
	}

	resp, raw = apiGet(t, ts.URL+"/shipped/count?since=7d")
	if resp.StatusCode != 200 {
		t.Fatalf("count status: %d body=%s", resp.StatusCode, raw)
	}
	var count api.ShippedCountResponse
	_ = json.Unmarshal(raw, &count)
	if count.Total != 1 {
		t.Fatalf("count total = %d, want 1", count.Total)
	}
}
