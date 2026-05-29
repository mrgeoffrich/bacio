package client

import (
	"context"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/store"
)

// TestAddNotificationRecordsAudit locks in that AddNotification writes a
// notification.send history row attributed to the source agent, so
// `bacio history --op notification.send` surfaces every notification.
func TestAddNotificationRecordsAudit(t *testing.T) {
	c, _ := openTestLocalClient(t)
	repo := seedRepo(t, c.store, "NOTI", "/tmp/noti")

	n, err := c.AddNotification(context.Background(), AddNotificationInput{
		RepoPrefix:  repo.Prefix,
		Body:        "shipped successfully",
		SourceAgent: "spry-quail",
	})
	if err != nil {
		t.Fatalf("AddNotification: %v", err)
	}

	rows, err := c.store.ListHistory(store.HistoryFilter{Op: "notification.send"})
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 notification.send row, got %d", len(rows))
	}
	got := rows[0]
	if got.Actor != "spry-quail" {
		t.Fatalf("audit actor = %q, want spry-quail", got.Actor)
	}
	if got.Kind != "notification" {
		t.Fatalf("audit kind = %q, want notification", got.Kind)
	}
	if got.TargetID == nil || *got.TargetID != n.ID {
		t.Fatalf("audit target_id = %v, want %d", got.TargetID, n.ID)
	}
}

// TestAddNotificationDefaultsSourceAgent: an empty SourceAgent falls back to
// the "bacio-channel" sentinel (a notification fired before register).
func TestAddNotificationDefaultsSourceAgent(t *testing.T) {
	c, _ := openTestLocalClient(t)
	repo := seedRepo(t, c.store, "NOTI", "/tmp/noti")

	n, err := c.AddNotification(context.Background(), AddNotificationInput{
		RepoPrefix: repo.Prefix,
		Body:       "no agent yet",
	})
	if err != nil {
		t.Fatalf("AddNotification: %v", err)
	}
	if n.SourceAgent != "bacio-channel" {
		t.Fatalf("source agent = %q, want bacio-channel fallback", n.SourceAgent)
	}
}

// TestMarkNotificationReadRecordsAudit covers the read mutation audit +
// dry-run no-write contract.
func TestMarkNotificationReadRecordsAudit(t *testing.T) {
	c, _ := openTestLocalClient(t)
	repo := seedRepo(t, c.store, "NOTI", "/tmp/noti")
	n, err := c.AddNotification(context.Background(), AddNotificationInput{
		RepoPrefix: repo.Prefix, Body: "mark me", SourceAgent: "bacio-channel",
	})
	if err != nil {
		t.Fatalf("AddNotification: %v", err)
	}

	// Dry-run: no write, no audit row.
	if _, err := c.MarkNotificationRead(context.Background(), n.ID, true); err != nil {
		t.Fatalf("dry-run mark: %v", err)
	}
	rows, _ := c.store.ListHistory(store.HistoryFilter{Op: "notification.read"})
	if len(rows) != 0 {
		t.Fatalf("dry-run should write no audit row, got %d", len(rows))
	}
	cur, _ := c.store.GetNotification(n.ID)
	if cur.ReadAt != nil {
		t.Fatalf("dry-run should not stamp read_at")
	}

	// Real mark: read_at stamped + audit row.
	updated, err := c.MarkNotificationRead(context.Background(), n.ID, false)
	if err != nil {
		t.Fatalf("mark: %v", err)
	}
	if updated.ReadAt == nil {
		t.Fatalf("expected read_at stamped")
	}
	rows, _ = c.store.ListHistory(store.HistoryFilter{Op: "notification.read"})
	if len(rows) != 1 {
		t.Fatalf("expected 1 notification.read row, got %d", len(rows))
	}
}

// TestMarkAllNotificationsReadRecordsAudit covers the bulk mutation audit +
// dry-run count projection.
func TestMarkAllNotificationsReadRecordsAudit(t *testing.T) {
	c, _ := openTestLocalClient(t)
	repo := seedRepo(t, c.store, "NOTI", "/tmp/noti")
	for _, b := range []string{"a", "b"} {
		if _, err := c.AddNotification(context.Background(), AddNotificationInput{
			RepoPrefix: repo.Prefix, Body: b, SourceAgent: "bacio-channel",
		}); err != nil {
			t.Fatalf("add %q: %v", b, err)
		}
	}

	// Dry-run projects the unread count without writing.
	got, err := c.MarkAllNotificationsRead(context.Background(), nil, true)
	if err != nil {
		t.Fatalf("dry-run mark-all: %v", err)
	}
	if got != 2 {
		t.Fatalf("dry-run count = %d, want 2", got)
	}
	if left, _ := c.store.CountUnreadNotifications(nil); left != 2 {
		t.Fatalf("dry-run should not flip rows, unread = %d", left)
	}

	// Real mark-all flips both + writes one audit row.
	n, err := c.MarkAllNotificationsRead(context.Background(), nil, false)
	if err != nil {
		t.Fatalf("mark-all: %v", err)
	}
	if n != 2 {
		t.Fatalf("mark-all flipped %d, want 2", n)
	}
	rows, _ := c.store.ListHistory(store.HistoryFilter{Op: "notification.read-all"})
	if len(rows) != 1 {
		t.Fatalf("expected 1 notification.read-all row, got %d", len(rows))
	}
}
