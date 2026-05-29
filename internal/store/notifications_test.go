package store

import (
	"errors"
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// TestAddNotification_HappyPath covers the happy path: a notification
// scoped to a repo, with a session and an issue deep-link, comes back with
// the resolved FKs, the lifted repo prefix, and an unread (nil read_at)
// timestamp.
func TestAddNotification_HappyPath(t *testing.T) {
	s, repo, iss := seedRepoAndIssue(t)
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "notif-sess", RepoID: repo.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("register session: %v", err)
	}
	n, err := s.AddNotification(AddNotificationIn{
		RepoPrefix:  repo.Prefix,
		SessionID:   "notif-sess",
		IssueKey:    iss.Key,
		Body:        "build is green, shipping now",
		SourceAgent: "spry-quail",
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if n.ID == 0 {
		t.Fatalf("expected populated id, got %+v", n)
	}
	if n.RepoID != repo.ID {
		t.Fatalf("repo id = %d, want %d", n.RepoID, repo.ID)
	}
	if n.RepoPrefix != repo.Prefix {
		t.Fatalf("repo prefix = %q, want %q", n.RepoPrefix, repo.Prefix)
	}
	if n.IssueID == nil || *n.IssueID != iss.ID {
		t.Fatalf("issue id = %v, want %d", n.IssueID, iss.ID)
	}
	if n.IssueKey != iss.Key {
		t.Fatalf("issue key = %q, want %q", n.IssueKey, iss.Key)
	}
	if n.SessionPK == nil {
		t.Fatalf("expected resolved session_pk, got nil")
	}
	if n.SourceAgent != "spry-quail" {
		t.Fatalf("source agent = %q, want spry-quail", n.SourceAgent)
	}
	if n.ReadAt != nil {
		t.Fatalf("fresh notification should be unread, got read_at=%v", n.ReadAt)
	}
}

// TestAddNotification_NoSessionNoIssue covers a ticket-less notification
// fired before any session registered: repo resolves, session_pk + issue
// stay nil, source_agent still records.
func TestAddNotification_NoSessionNoIssue(t *testing.T) {
	s, repo, _ := seedRepoAndIssue(t)
	n, err := s.AddNotification(AddNotificationIn{
		RepoPrefix:  repo.Prefix,
		Body:        "heads up: no ticket on this one",
		SourceAgent: "bacio-channel",
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if n.SessionPK != nil {
		t.Fatalf("expected nil session_pk, got %v", n.SessionPK)
	}
	if n.IssueID != nil {
		t.Fatalf("expected nil issue_id, got %v", n.IssueID)
	}
	if n.IssueKey != "" {
		t.Fatalf("expected empty issue_key, got %q", n.IssueKey)
	}
}

// TestAddNotification_UnknownSessionTolerated: an unknown session id is not
// an error — the row records source_agent without a session FK.
func TestAddNotification_UnknownSessionTolerated(t *testing.T) {
	s, repo, _ := seedRepoAndIssue(t)
	n, err := s.AddNotification(AddNotificationIn{
		RepoPrefix:  repo.Prefix,
		SessionID:   "ghost-session",
		Body:        "still fires",
		SourceAgent: "bacio-channel",
	})
	if err != nil {
		t.Fatalf("add with unknown session should not error: %v", err)
	}
	if n.SessionPK != nil {
		t.Fatalf("unknown session should leave session_pk nil, got %v", n.SessionPK)
	}
}

// TestAddNotification_UnknownRepo returns ErrNotFound — there's no repo FK.
func TestAddNotification_UnknownRepo(t *testing.T) {
	s, _, _ := seedRepoAndIssue(t)
	_, err := s.AddNotification(AddNotificationIn{
		RepoPrefix:  "ZZZZ",
		Body:        "nowhere",
		SourceAgent: "bacio-channel",
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for unknown repo, got %v", err)
	}
}

// TestAddNotification_IssueWrongRepo rejects a deep-link to an issue that
// lives in a different repo than the notification is scoped to.
func TestAddNotification_IssueWrongRepo(t *testing.T) {
	s, repo, iss := seedRepoAndIssue(t)
	other, err := s.CreateRepo("OTHR", "other-test", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create other repo: %v", err)
	}
	_, err = s.AddNotification(AddNotificationIn{
		RepoPrefix:  other.Prefix,
		IssueKey:    iss.Key, // belongs to repo, not other
		Body:        "mismatch",
		SourceAgent: "bacio-channel",
	})
	if err == nil {
		t.Fatalf("expected cross-repo issue to be rejected")
	}
	_ = repo
}

// TestAddNotification_RejectsEmptyBody / _RejectsControlChars cover the
// boundary validator.
func TestAddNotification_RejectsEmptyBody(t *testing.T) {
	s, repo, _ := seedRepoAndIssue(t)
	if _, err := s.AddNotification(AddNotificationIn{
		RepoPrefix: repo.Prefix, Body: "   ", SourceAgent: "bacio-channel",
	}); err == nil {
		t.Fatalf("empty body should be rejected")
	}
}

func TestAddNotification_RejectsControlChars(t *testing.T) {
	s, repo, _ := seedRepoAndIssue(t)
	if _, err := s.AddNotification(AddNotificationIn{
		RepoPrefix: repo.Prefix, Body: "bad\x00null", SourceAgent: "bacio-channel",
	}); err == nil {
		t.Fatalf("control char should be rejected")
	}
	tooLong := strings.Repeat("x", model.MaxNotificationBodyLen+1)
	if _, err := s.AddNotification(AddNotificationIn{
		RepoPrefix: repo.Prefix, Body: tooLong, SourceAgent: "bacio-channel",
	}); err == nil {
		t.Fatalf("over-cap body should be rejected")
	}
}

// TestListNotifications_UnreadFilter: a read row drops out of the unread
// list but stays in the all-rows list.
func TestListNotifications_UnreadFilter(t *testing.T) {
	s, repo, _ := seedRepoAndIssue(t)
	read, err := s.AddNotification(AddNotificationIn{RepoPrefix: repo.Prefix, Body: "one", SourceAgent: "bacio-channel"})
	if err != nil {
		t.Fatalf("add one: %v", err)
	}
	if _, err := s.AddNotification(AddNotificationIn{RepoPrefix: repo.Prefix, Body: "two", SourceAgent: "bacio-channel"}); err != nil {
		t.Fatalf("add two: %v", err)
	}
	if _, err := s.MarkNotificationRead(read.ID); err != nil {
		t.Fatalf("mark read: %v", err)
	}
	unread, err := s.ListNotifications(NotificationFilter{UnreadOnly: true})
	if err != nil {
		t.Fatalf("list unread: %v", err)
	}
	if len(unread) != 1 {
		t.Fatalf("unread list returned %d, want 1", len(unread))
	}
	all, err := s.ListNotifications(NotificationFilter{})
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("all list returned %d, want 2", len(all))
	}
}

// TestListNotifications_CrossRepo: a nil RepoID lists across repos; a
// scoped RepoID returns only that repo's rows.
func TestListNotifications_CrossRepo(t *testing.T) {
	s, repo, _ := seedRepoAndIssue(t)
	other, err := s.CreateRepo("OTHR", "other-test", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create other repo: %v", err)
	}
	if _, err := s.AddNotification(AddNotificationIn{RepoPrefix: repo.Prefix, Body: "a", SourceAgent: "bacio-channel"}); err != nil {
		t.Fatalf("add a: %v", err)
	}
	if _, err := s.AddNotification(AddNotificationIn{RepoPrefix: other.Prefix, Body: "b", SourceAgent: "bacio-channel"}); err != nil {
		t.Fatalf("add b: %v", err)
	}
	global, err := s.ListNotifications(NotificationFilter{})
	if err != nil {
		t.Fatalf("list global: %v", err)
	}
	if len(global) != 2 {
		t.Fatalf("global list returned %d, want 2", len(global))
	}
	scoped, err := s.ListNotifications(NotificationFilter{RepoID: &other.ID})
	if err != nil {
		t.Fatalf("list scoped: %v", err)
	}
	if len(scoped) != 1 || scoped[0].Body != "b" {
		t.Fatalf("scoped list = %+v, want one 'b' row", scoped)
	}
}

// TestMarkNotificationRead: marking flips read_at; a second mark is
// idempotent (no error, read_at unchanged). Unknown id is ErrNotFound.
func TestMarkNotificationRead(t *testing.T) {
	s, repo, _ := seedRepoAndIssue(t)
	n, err := s.AddNotification(AddNotificationIn{RepoPrefix: repo.Prefix, Body: "mark me", SourceAgent: "bacio-channel"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	got, err := s.MarkNotificationRead(n.ID)
	if err != nil {
		t.Fatalf("mark read: %v", err)
	}
	if got.ReadAt == nil {
		t.Fatalf("expected read_at stamped, got nil")
	}
	first := *got.ReadAt
	again, err := s.MarkNotificationRead(n.ID)
	if err != nil {
		t.Fatalf("second mark read: %v", err)
	}
	if again.ReadAt == nil || !again.ReadAt.Equal(first) {
		t.Fatalf("second mark should be idempotent: %v vs %v", again.ReadAt, first)
	}
	if _, err := s.MarkNotificationRead(99999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id should be ErrNotFound, got %v", err)
	}
}

// TestMarkAllNotificationsRead flips every unread row and returns the count.
func TestMarkAllNotificationsRead(t *testing.T) {
	s, repo, _ := seedRepoAndIssue(t)
	for _, b := range []string{"a", "b", "c"} {
		if _, err := s.AddNotification(AddNotificationIn{RepoPrefix: repo.Prefix, Body: b, SourceAgent: "bacio-channel"}); err != nil {
			t.Fatalf("add %q: %v", b, err)
		}
	}
	n, err := s.MarkAllNotificationsRead(nil)
	if err != nil {
		t.Fatalf("mark all: %v", err)
	}
	if n != 3 {
		t.Fatalf("mark all flipped %d rows, want 3", n)
	}
	left, err := s.CountUnreadNotifications(nil)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if left != 0 {
		t.Fatalf("unread after mark-all = %d, want 0", left)
	}
}

// TestCountUnread tracks the badge count across add + mark-read, both
// global and repo-scoped.
func TestCountUnread(t *testing.T) {
	s, repo, _ := seedRepoAndIssue(t)
	other, err := s.CreateRepo("OTHR", "other-test", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create other repo: %v", err)
	}
	a, err := s.AddNotification(AddNotificationIn{RepoPrefix: repo.Prefix, Body: "a", SourceAgent: "bacio-channel"})
	if err != nil {
		t.Fatalf("add a: %v", err)
	}
	if _, err := s.AddNotification(AddNotificationIn{RepoPrefix: other.Prefix, Body: "b", SourceAgent: "bacio-channel"}); err != nil {
		t.Fatalf("add b: %v", err)
	}
	global, err := s.CountUnreadNotifications(nil)
	if err != nil {
		t.Fatalf("count global: %v", err)
	}
	if global != 2 {
		t.Fatalf("global unread = %d, want 2", global)
	}
	scoped, err := s.CountUnreadNotifications(&repo.ID)
	if err != nil {
		t.Fatalf("count scoped: %v", err)
	}
	if scoped != 1 {
		t.Fatalf("scoped unread = %d, want 1", scoped)
	}
	if _, err := s.MarkNotificationRead(a.ID); err != nil {
		t.Fatalf("mark read: %v", err)
	}
	global, err = s.CountUnreadNotifications(nil)
	if err != nil {
		t.Fatalf("recount global: %v", err)
	}
	if global != 1 {
		t.Fatalf("global unread after read = %d, want 1", global)
	}
}
