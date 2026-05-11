package api_test

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/mrgeoffrich/bacio/internal/api"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

func seedHistoryRows(t *testing.T, s *store.Store, repo *model.Repo, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if err := s.RecordHistory(model.HistoryEntry{
			RepoID:      &repo.ID,
			RepoPrefix:  repo.Prefix,
			Actor:       "tester",
			Op:          fmt.Sprintf("issue.create"),
			Kind:        "issue",
			TargetLabel: fmt.Sprintf("MINI-%d", i+1),
		}); err != nil {
			t.Fatalf("seed history: %v", err)
		}
	}
}

func TestHistoryRepoSingle(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedHistoryRows(t, s, repo, 3)

	resp, raw := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/history")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body=%s", resp.StatusCode, raw)
	}
	var rows []*model.HistoryEntry
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows: %d", len(rows))
	}
}

func TestHistoryDefaultLimit(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedHistoryRows(t, s, repo, 60)

	resp, raw := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/history")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var rows []*model.HistoryEntry
	_ = json.Unmarshal(raw, &rows)
	if len(rows) != 50 {
		t.Fatalf("expected default limit 50, got %d", len(rows))
	}
}

func TestHistoryLimitZeroReturnsAll(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedHistoryRows(t, s, repo, 60)

	resp, raw := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/history?limit=0")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var rows []*model.HistoryEntry
	_ = json.Unmarshal(raw, &rows)
	if len(rows) != 60 {
		t.Fatalf("rows: %d", len(rows))
	}
}

func TestHistoryOffset(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedHistoryRows(t, s, repo, 5)

	resp, raw := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/history?offset=2&limit=10")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var rows []*model.HistoryEntry
	_ = json.Unmarshal(raw, &rows)
	if len(rows) != 3 {
		t.Fatalf("rows: %d (expected 3 after offset=2)", len(rows))
	}
}

func TestHistoryOpFilter(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	if err := s.RecordHistory(model.HistoryEntry{
		RepoID: &repo.ID, RepoPrefix: repo.Prefix,
		Actor: "a", Op: "issue.create", Kind: "issue",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordHistory(model.HistoryEntry{
		RepoID: &repo.ID, RepoPrefix: repo.Prefix,
		Actor: "a", Op: "issue.update", Kind: "issue",
	}); err != nil {
		t.Fatal(err)
	}
	resp, raw := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/history?op=issue.create")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var rows []*model.HistoryEntry
	_ = json.Unmarshal(raw, &rows)
	if len(rows) != 1 || rows[0].Op != "issue.create" {
		t.Fatalf("got: %+v", rows)
	}
}

func TestHistoryKindFilter(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	_ = s.RecordHistory(model.HistoryEntry{RepoID: &repo.ID, RepoPrefix: repo.Prefix, Actor: "a", Op: "issue.create", Kind: "issue"})
	_ = s.RecordHistory(model.HistoryEntry{RepoID: &repo.ID, RepoPrefix: repo.Prefix, Actor: "a", Op: "feature.create", Kind: "feature"})

	resp, raw := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/history?kind=feature")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var rows []*model.HistoryEntry
	_ = json.Unmarshal(raw, &rows)
	if len(rows) != 1 || rows[0].Kind != "feature" {
		t.Fatalf("got: %+v", rows)
	}
}

func TestHistoryActorFilter(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	_ = s.RecordHistory(model.HistoryEntry{RepoID: &repo.ID, RepoPrefix: repo.Prefix, Actor: "alice", Op: "issue.create", Kind: "issue"})
	_ = s.RecordHistory(model.HistoryEntry{RepoID: &repo.ID, RepoPrefix: repo.Prefix, Actor: "bob", Op: "issue.create", Kind: "issue"})

	resp, raw := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/history?actor=alice")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var rows []*model.HistoryEntry
	_ = json.Unmarshal(raw, &rows)
	if len(rows) != 1 || rows[0].Actor != "alice" {
		t.Fatalf("got: %+v", rows)
	}
}

func TestHistoryUserFilterAlias(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	_ = s.RecordHistory(model.HistoryEntry{RepoID: &repo.ID, RepoPrefix: repo.Prefix, Actor: "alice", Op: "issue.create", Kind: "issue"})
	_ = s.RecordHistory(model.HistoryEntry{RepoID: &repo.ID, RepoPrefix: repo.Prefix, Actor: "bob", Op: "issue.create", Kind: "issue"})

	resp, raw := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/history?user_filter=bob")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var rows []*model.HistoryEntry
	_ = json.Unmarshal(raw, &rows)
	if len(rows) != 1 || rows[0].Actor != "bob" {
		t.Fatalf("got: %+v", rows)
	}
}

func TestHistorySinceWorks(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	_ = s.RecordHistory(model.HistoryEntry{RepoID: &repo.ID, RepoPrefix: repo.Prefix, Actor: "a", Op: "issue.create", Kind: "issue"})

	resp, raw := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/history?since=1h")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var rows []*model.HistoryEntry
	_ = json.Unmarshal(raw, &rows)
	if len(rows) != 1 {
		t.Fatalf("rows: %d", len(rows))
	}
}

func TestHistoryFromToWorks(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	_ = s.RecordHistory(model.HistoryEntry{RepoID: &repo.ID, RepoPrefix: repo.Prefix, Actor: "a", Op: "issue.create", Kind: "issue"})

	yesterday := time.Now().Add(-24 * time.Hour).Format("2006-01-02")
	tomorrow := time.Now().Add(24 * time.Hour).Format("2006-01-02")
	resp, raw := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/history?from="+yesterday+"&to="+tomorrow)
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body=%s", resp.StatusCode, raw)
	}
	var rows []*model.HistoryEntry
	_ = json.Unmarshal(raw, &rows)
	if len(rows) != 1 {
		t.Fatalf("rows: %d", len(rows))
	}
}

func TestHistorySinceFromExclusive(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	resp, _ := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/history?since=1h&from=2026-05-01")
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestHistoryToBeforeFrom(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	resp, _ := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/history?from=2026-05-10&to=2026-05-01")
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestHistoryMalformedTimestamp(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	resp, _ := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/history?from=garbage")
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestHistoryOldestFirst(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	_ = s.RecordHistory(model.HistoryEntry{RepoID: &repo.ID, RepoPrefix: repo.Prefix, Actor: "a", Op: "issue.create", Kind: "issue", TargetLabel: "first"})
	_ = s.RecordHistory(model.HistoryEntry{RepoID: &repo.ID, RepoPrefix: repo.Prefix, Actor: "a", Op: "issue.create", Kind: "issue", TargetLabel: "second"})

	respDefault, rawDefault := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/history")
	if respDefault.StatusCode != 200 {
		t.Fatalf("status: %d", respDefault.StatusCode)
	}
	var defaultRows []*model.HistoryEntry
	_ = json.Unmarshal(rawDefault, &defaultRows)
	if len(defaultRows) < 2 || defaultRows[0].TargetLabel != "second" {
		t.Fatalf("default order wrong: %+v", defaultRows)
	}

	resp, raw := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/history?oldest_first=true")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var rows []*model.HistoryEntry
	_ = json.Unmarshal(raw, &rows)
	if len(rows) < 2 || rows[0].TargetLabel != "first" {
		t.Fatalf("oldest_first order wrong: %+v", rows)
	}
}

func TestHistoryAllRepos(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	repo2 := seedRepo2(t, s)
	_ = s.RecordHistory(model.HistoryEntry{RepoID: &repo.ID, RepoPrefix: repo.Prefix, Actor: "a", Op: "issue.create", Kind: "issue"})
	_ = s.RecordHistory(model.HistoryEntry{RepoID: &repo2.ID, RepoPrefix: repo2.Prefix, Actor: "a", Op: "issue.create", Kind: "issue"})

	resp, raw := apiGet(t, ts.URL+"/history")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var rows []*model.HistoryEntry
	_ = json.Unmarshal(raw, &rows)
	if len(rows) != 2 {
		t.Fatalf("cross-repo rows: %d", len(rows))
	}

	resp1, raw1 := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/history")
	if resp1.StatusCode != 200 {
		t.Fatalf("status: %d", resp1.StatusCode)
	}
	var rows1 []*model.HistoryEntry
	_ = json.Unmarshal(raw1, &rows1)
	if len(rows1) != 1 || *rows1[0].RepoID != repo.ID {
		t.Fatalf("scoped rows: %+v", rows1)
	}
}

func TestHistoryEmptyArrayNotNull(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	_ = seedRepo(t, s)
	resp, raw := apiGet(t, ts.URL+"/history")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if len(raw) == 0 || raw[0] != '[' {
		t.Fatalf("expected [], got %s", string(raw))
	}
}
