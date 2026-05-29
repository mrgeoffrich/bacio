package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// BACI-287 agent→user notification REST surface — the read + mark-read side
// of the notification bell. The write side is channel-only (the
// send_user_notification MCP tool), so there is no POST-create endpoint
// here. The list/count are cross-repo by design (the global bell, mirroring
// /history's all-repos read):
//
//	GET  /notifications[?state=unread|all][&limit=N]  — list (default unread)
//	GET  /notifications/count                         — unread badge count
//	POST /notifications/{id}/read                     — mark one read
//	POST /notifications/read-all                      — mark all read
//
// X-Actor lands in the audit row's actor for the mark mutations (falls back
// to "api"). ?dry_run=1 / X-Dry-Run projects without touching the store.

// notificationCountResponse is the body of GET /notifications/count and the
// mark-all response — the unread badge count the bell polls.
type notificationCountResponse struct {
	Count int `json:"count"`
}

const (
	// notificationsAPIDefaultLimit is what the bell's list fetch asks for
	// when ?limit= is omitted; matches the store-side default cap.
	notificationsAPIDefaultLimit = 200
	// notificationsAPIMaxLimit caps caller-supplied ?limit= — past this the
	// operator wants the audit log, not the bell.
	notificationsAPIMaxLimit = 500
)

// handleNotificationsList returns notifications cross-repo, newest-first.
// ?state=unread (default) drops read rows; ?state=all returns every row.
func (d deps) handleNotificationsList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filter := store.NotificationFilter{}

	switch state := q.Get("state"); state {
	case "", "unread":
		filter.UnreadOnly = true
	case "all":
		filter.UnreadOnly = false
	default:
		writeError(w, http.StatusBadRequest, "invalid_input",
			fmt.Sprintf("invalid state %q (valid: unread, all)", state),
			map[string]any{"field": "state"})
		return
	}

	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "invalid_input", "limit must be a positive integer", map[string]any{"field": "limit"})
			return
		}
		if n > notificationsAPIMaxLimit {
			n = notificationsAPIMaxLimit
		}
		filter.Limit = n
	} else {
		filter.Limit = notificationsAPIDefaultLimit
	}

	rows, err := d.store.ListNotifications(filter)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if rows == nil {
		rows = []*model.Notification{}
	}
	writeJSON(w, http.StatusOK, rows)
}

// handleNotificationsCount returns the cross-repo unread badge count — the
// bell polls this on the same cadence as the other live read endpoints.
func (d deps) handleNotificationsCount(w http.ResponseWriter, r *http.Request) {
	n, err := d.store.CountUnreadNotifications(nil)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, notificationCountResponse{Count: n})
}

// handleNotificationShow returns one notification by id.
func (d deps) handleNotificationShow(w http.ResponseWriter, r *http.Request) {
	id, ok := readNotificationID(r, w)
	if !ok {
		return
	}
	n, err := d.store.GetNotification(id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found",
				fmt.Sprintf("no notification with id %d", id), nil)
			return
		}
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, n)
}

// handleNotificationRead marks one notification read. Idempotent — marking
// an already-read row is not an error. X-Actor lands in the audit row.
func (d deps) handleNotificationRead(w http.ResponseWriter, r *http.Request) {
	id, ok := readNotificationID(r, w)
	if !ok {
		return
	}
	// Drain any body cleanly even though this endpoint takes none.
	if _, ok := readBody(r, w); !ok {
		return
	}
	current, err := d.store.GetNotification(id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found",
				fmt.Sprintf("no notification with id %d", id), nil)
			return
		}
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if isDryRun(r) {
		writeDryRun(w, http.StatusOK, current)
		return
	}
	updated, err := d.store.MarkNotificationRead(id)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	actor := ActorFromContext(r.Context())
	repoID := updated.RepoID
	recordOp(d.store, d.logger, model.HistoryEntry{
		Actor:  actor,
		RepoID: &repoID,
		Op:     "notification.read", Kind: "notification",
		TargetID: &updated.ID, TargetLabel: updated.IssueKey,
	})
	writeJSON(w, http.StatusOK, updated)
}

// handleNotificationsReadAll marks every unread notification read,
// cross-repo, returning the count flipped.
func (d deps) handleNotificationsReadAll(w http.ResponseWriter, r *http.Request) {
	if _, ok := readBody(r, w); !ok {
		return
	}
	if isDryRun(r) {
		n, err := d.store.CountUnreadNotifications(nil)
		if err != nil {
			status, code := statusForError(err)
			writeError(w, status, code, err.Error(), nil)
			return
		}
		writeDryRun(w, http.StatusOK, notificationCountResponse{Count: n})
		return
	}
	n, err := d.store.MarkAllNotificationsRead(nil)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	actor := ActorFromContext(r.Context())
	recordOp(d.store, d.logger, model.HistoryEntry{
		Actor: actor,
		Op:    "notification.read-all", Kind: "notification",
		Details: fmt.Sprintf("count=%d", n),
	})
	writeJSON(w, http.StatusOK, notificationCountResponse{Count: n})
}

func readNotificationID(r *http.Request, w http.ResponseWriter) (int64, bool) {
	raw := r.PathValue("id")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_input",
			fmt.Sprintf("invalid notification id %q", raw),
			map[string]any{"field": "id"})
		return 0, false
	}
	return id, true
}
