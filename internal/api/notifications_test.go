package api_test

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/api"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// seedNotification inserts a notification scoped to the given repo prefix.
func seedNotification(t *testing.T, s *store.Store, prefix, body string) *model.Notification {
	t.Helper()
	n, err := s.AddNotification(store.AddNotificationIn{
		RepoPrefix:  prefix,
		Body:        body,
		SourceAgent: "bacio-channel",
	})
	if err != nil {
		t.Fatalf("seed notification: %v", err)
	}
	return n
}

// TestNotificationsListUnreadDefault: the bare list endpoint returns only
// unread rows by default; ?state=all returns read rows too. Cross-repo.
func TestNotificationsListUnreadDefault(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	other := seedRepo2(t, s)
	read := seedNotification(t, s, repo.Prefix, "in repo one")
	seedNotification(t, s, other.Prefix, "in repo two")
	if _, err := s.MarkNotificationRead(read.ID); err != nil {
		t.Fatalf("mark read: %v", err)
	}

	// Default (unread): only the un-marked, cross-repo row.
	resp, body := apiGet(t, ts.URL+"/notifications")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body=%s", resp.StatusCode, body)
	}
	var unread []*model.Notification
	if err := json.Unmarshal(body, &unread); err != nil {
		t.Fatalf("decode: %v body=%s", err, body)
	}
	if len(unread) != 1 || unread[0].Body != "in repo two" {
		t.Fatalf("unread list = %+v, want one 'in repo two' row", unread)
	}
	if unread[0].RepoPrefix != other.Prefix {
		t.Fatalf("row repo prefix = %q, want %q", unread[0].RepoPrefix, other.Prefix)
	}

	// ?state=all: both rows.
	resp, body = apiGet(t, ts.URL+"/notifications?state=all")
	if resp.StatusCode != 200 {
		t.Fatalf("all status: %d body=%s", resp.StatusCode, body)
	}
	var all []*model.Notification
	if err := json.Unmarshal(body, &all); err != nil {
		t.Fatalf("decode all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("all list = %d rows, want 2", len(all))
	}
}

// TestNotificationsListBadState rejects an unknown ?state= with 400.
func TestNotificationsListBadState(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp, body := apiGet(t, ts.URL+"/notifications?state=bogus")
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d body=%s, want 400", resp.StatusCode, body)
	}
}

// TestNotificationsCount returns the cross-repo unread count.
func TestNotificationsCount(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedNotification(t, s, repo.Prefix, "a")
	seedNotification(t, s, repo.Prefix, "b")

	resp, body := apiGet(t, ts.URL+"/notifications/count")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body=%s", resp.StatusCode, body)
	}
	var out struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Count != 2 {
		t.Fatalf("count = %d, want 2", out.Count)
	}
}

// TestNotificationReadHappy marks one read, records the actor in the audit
// row, and decrements the unread count. A 404 on a missing id.
func TestNotificationReadHappy(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	n := seedNotification(t, s, repo.Prefix, "mark me")

	url := ts.URL + "/notifications/" + strconv.FormatInt(n.ID, 10) + "/read"
	resp, body := apiReq(t, "POST", url, nil, map[string]string{"X-Actor": "geoff"})
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body=%s", resp.StatusCode, body)
	}
	var got model.Notification
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ReadAt == nil {
		t.Fatalf("expected read_at stamped")
	}

	// Audit row attributed to X-Actor.
	rows, err := s.ListHistory(store.HistoryFilter{Op: "notification.read"})
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(rows) != 1 || rows[0].Actor != "geoff" {
		t.Fatalf("audit rows = %+v, want one row by geoff", rows)
	}

	// 404 on missing id.
	resp, _ = apiPost(t, ts.URL+"/notifications/99999/read", nil)
	if resp.StatusCode != 404 {
		t.Fatalf("missing id status = %d, want 404", resp.StatusCode)
	}
}

// TestNotificationReadDryRun projects the row without writing or auditing.
func TestNotificationReadDryRun(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	n := seedNotification(t, s, repo.Prefix, "dry")

	url := ts.URL + "/notifications/" + strconv.FormatInt(n.ID, 10) + "/read?dry_run=1"
	resp, body := apiPost(t, url, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body=%s", resp.StatusCode, body)
	}
	cur, _ := s.GetNotification(n.ID)
	if cur.ReadAt != nil {
		t.Fatalf("dry-run should not stamp read_at")
	}
	rows, _ := s.ListHistory(store.HistoryFilter{Op: "notification.read"})
	if len(rows) != 0 {
		t.Fatalf("dry-run should write no audit row, got %d", len(rows))
	}
}

// TestNotificationsReadAll marks every unread row read, cross-repo, and
// returns the count flipped + writes a notification.read-all audit row.
func TestNotificationsReadAll(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	other := seedRepo2(t, s)
	seedNotification(t, s, repo.Prefix, "a")
	seedNotification(t, s, other.Prefix, "b")

	resp, body := apiReq(t, "POST", ts.URL+"/notifications/read-all", nil,
		map[string]string{"X-Actor": "geoff"})
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body=%s", resp.StatusCode, body)
	}
	var out struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Count != 2 {
		t.Fatalf("count = %d, want 2", out.Count)
	}
	if left, _ := s.CountUnreadNotifications(nil); left != 0 {
		t.Fatalf("unread after read-all = %d, want 0", left)
	}
	rows, _ := s.ListHistory(store.HistoryFilter{Op: "notification.read-all"})
	if len(rows) != 1 || rows[0].Actor != "geoff" {
		t.Fatalf("audit rows = %+v, want one read-all row by geoff", rows)
	}
}
