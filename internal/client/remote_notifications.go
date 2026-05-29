package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// AddNotification is channel-only — the channel always talks to the local
// store directly (it doesn't go through --remote), so the write side has no
// HTTP surface. Local-only, matching the user-message + question writes.
func (c *remoteClient) AddNotification(ctx context.Context, in AddNotificationInput) (*model.Notification, error) {
	return nil, remoteAgentNotSupported("notifications")
}

// ListNotifications reads the cross-repo notification list over REST so a
// remote-mode desktop / web bell works. The global list endpoint takes a
// `state=unread|all` filter (default unread).
func (c *remoteClient) ListNotifications(ctx context.Context, filter NotificationListFilter) ([]*model.Notification, error) {
	q := url.Values{}
	if filter.UnreadOnly {
		q.Set("state", "unread")
	} else {
		q.Set("state", "all")
	}
	if filter.Limit > 0 {
		q.Set("limit", strconv.Itoa(filter.Limit))
	}
	var out []*model.Notification
	if err := c.do(ctx, http.MethodGet, "/notifications", q, nil, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []*model.Notification{}
	}
	return out, nil
}

// CountUnreadNotifications reads the unread badge count over REST.
func (c *remoteClient) CountUnreadNotifications(ctx context.Context, repoID *int64) (int, error) {
	var out struct {
		Count int `json:"count"`
	}
	if err := c.do(ctx, http.MethodGet, "/notifications/count", nil, nil, &out); err != nil {
		return 0, err
	}
	return out.Count, nil
}

// GetNotification reads one notification by id over REST.
func (c *remoteClient) GetNotification(ctx context.Context, id int64) (*model.Notification, error) {
	var out model.Notification
	if err := c.do(ctx, http.MethodGet, "/notifications/"+strconv.FormatInt(id, 10), nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// MarkNotificationRead stamps read_at on one notification over REST.
func (c *remoteClient) MarkNotificationRead(ctx context.Context, id int64, dryRun bool) (*model.Notification, error) {
	q := url.Values{}
	if dryRun {
		q.Set("dry_run", "true")
	}
	var out model.Notification
	if err := c.do(ctx, http.MethodPost, "/notifications/"+strconv.FormatInt(id, 10)+"/read", q, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// MarkAllNotificationsRead marks every unread row read over REST, returning
// the count flipped.
func (c *remoteClient) MarkAllNotificationsRead(ctx context.Context, repoID *int64, dryRun bool) (int, error) {
	q := url.Values{}
	if dryRun {
		q.Set("dry_run", "true")
	}
	var out struct {
		Count int `json:"count"`
	}
	if err := c.do(ctx, http.MethodPost, "/notifications/read-all", q, nil, &out); err != nil {
		return 0, err
	}
	return out.Count, nil
}
