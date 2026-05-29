package client

import (
	"context"
	"fmt"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// AddNotification records a BACI-287 agent→user notification. The store
// resolves the repo (by prefix) + optional session / issue FKs and
// validates the body. Writes a notification.send audit row so
// `bacio history --op notification.send` surfaces every notification —
// unlike a question there is no answer/abandon lifecycle, so the send is
// the only audited create event.
func (c *localClient) AddNotification(ctx context.Context, in AddNotificationInput) (*model.Notification, error) {
	sourceAgent := in.SourceAgent
	if sourceAgent == "" {
		sourceAgent = "bacio-channel"
	}
	n, err := c.store.AddNotification(store.AddNotificationIn{
		RepoPrefix:  in.RepoPrefix,
		SessionID:   in.SessionID,
		IssueKey:    in.IssueKey,
		Body:        in.Body,
		SourceAgent: sourceAgent,
	})
	if err != nil {
		return nil, err
	}
	repoID := n.RepoID
	c.recordOp(model.HistoryEntry{
		Actor:  sourceAgent,
		RepoID: &repoID,
		Op:     "notification.send", Kind: "notification",
		TargetID:    &n.ID,
		TargetLabel: n.IssueKey,
		Details:     fmt.Sprintf("agent=%s,issue=%s,bytes=%d", sourceAgent, n.IssueKey, len(n.Body)),
	})
	return n, nil
}

// ListNotifications returns notifications matching the filter, newest-first.
func (c *localClient) ListNotifications(ctx context.Context, filter NotificationListFilter) ([]*model.Notification, error) {
	return c.store.ListNotifications(store.NotificationFilter{
		RepoID:     filter.RepoID,
		UnreadOnly: filter.UnreadOnly,
		Limit:      filter.Limit,
	})
}

// CountUnreadNotifications returns the unread badge count.
func (c *localClient) CountUnreadNotifications(ctx context.Context, repoID *int64) (int, error) {
	return c.store.CountUnreadNotifications(repoID)
}

// GetNotification fetches one notification by id.
func (c *localClient) GetNotification(ctx context.Context, id int64) (*model.Notification, error) {
	return c.store.GetNotification(id)
}

// MarkNotificationRead stamps read_at on one notification. dryRun runs the
// existence guard but writes nothing — the returned row is the existing row
// unchanged. Emits a notification.read audit row.
func (c *localClient) MarkNotificationRead(ctx context.Context, id int64, dryRun bool) (*model.Notification, error) {
	current, err := c.store.GetNotification(id)
	if err != nil {
		return nil, err
	}
	if dryRun {
		return current, nil
	}
	updated, err := c.store.MarkNotificationRead(id)
	if err != nil {
		return nil, err
	}
	repoID := updated.RepoID
	c.recordOp(model.HistoryEntry{
		RepoID: &repoID,
		Op:     "notification.read", Kind: "notification",
		TargetID:    &updated.ID,
		TargetLabel: updated.IssueKey,
	})
	return updated, nil
}

// MarkAllNotificationsRead stamps read_at on every unread row (nil repoID =
// cross-repo) and returns the count flipped. dryRun projects the current
// unread count without writing. Emits a notification.read-all audit row.
func (c *localClient) MarkAllNotificationsRead(ctx context.Context, repoID *int64, dryRun bool) (int, error) {
	if dryRun {
		return c.store.CountUnreadNotifications(repoID)
	}
	n, err := c.store.MarkAllNotificationsRead(repoID)
	if err != nil {
		return 0, err
	}
	c.recordOp(model.HistoryEntry{
		RepoID: repoID,
		Op:     "notification.read-all", Kind: "notification",
		Details: fmt.Sprintf("count=%d", n),
	})
	return n, nil
}
