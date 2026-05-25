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

// TestHandleShippedEmpty — empty repo returns 200 [], never null.
// The JS side relies on iterating the array unconditionally.
func TestHandleShippedEmpty(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)

	resp, raw := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/shipped")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body=%s", resp.StatusCode, raw)
	}
	if string(raw) != "[]\n" && string(raw) != "[]" {
		t.Fatalf("empty shipped list must be `[]`, got %q", raw)
	}
}

// TestHandleShippedDefaultLimit — 25 done rows, default limit=20
// returns exactly 20.
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
	var rows []api.ShippedIssue
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rows) != 20 {
		t.Fatalf("rows: %d, want default 20", len(rows))
	}
}

// TestHandleShippedSinceWindow — ?since=7d clamps to the last 7 days.
// One row inside the window, one back-dated past it; only the recent
// row returns.
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
	var rows []api.ShippedIssue
	_ = json.Unmarshal(raw, &rows)
	if len(rows) != 1 {
		t.Fatalf("rows: %d, want 1 (old row outside window)", len(rows))
	}
	if rows[0].Title != "young" {
		t.Fatalf("row title = %q, want 'young'", rows[0].Title)
	}
}

// TestHandleShippedLimitBound — ?limit=200 clamps to the API max
// (100); ?limit=-1 returns 400.
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
	var rows []api.ShippedIssue
	_ = json.Unmarshal(raw, &rows)
	if len(rows) != 100 {
		t.Fatalf("rows: %d, want clamp to API max 100", len(rows))
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
	var rows []api.ShippedIssue
	_ = json.Unmarshal(raw, &rows)
	if len(rows) != 1 {
		t.Fatalf("rows: %d, want 1", len(rows))
	}
	if rows[0].PRURL != "https://example.com/pr/1" {
		t.Fatalf("PR chip = %q, want first PR URL", rows[0].PRURL)
	}
}

// TestHandleShippedNonExistentRepo — /repos/ZZZZ/shipped returns 404
// like every other per-repo route.
func TestHandleShippedNonExistentRepo(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})

	resp, _ := apiGet(t, ts.URL+"/repos/ZZZZ/shipped")
	if resp.StatusCode != 404 {
		t.Fatalf("status: %d, want 404 on unknown repo", resp.StatusCode)
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
	var rows []api.ShippedIssue
	_ = json.Unmarshal(raw, &rows)
	if len(rows) != 3 {
		t.Fatalf("rows: %d, want 3", len(rows))
	}
	want := []string{"third", "second", "first"}
	for i, w := range want {
		if rows[i].Title != w {
			t.Fatalf("rows[%d].Title = %q, want %q (newest-first)", i, rows[i].Title, w)
		}
	}
}
