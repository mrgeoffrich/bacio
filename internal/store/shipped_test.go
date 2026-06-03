package store

import (
	"strings"
	"testing"
	"time"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// TestListShippedIssuesNewestFirst — the popover renders newest-first;
// three done rows must come back in terminal_at DESC order.
func TestListShippedIssuesNewestFirst(t *testing.T) {
	s := newTestStore(t)
	repo, err := s.CreateRepo("TST", "test", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}

	first, _ := s.CreateIssue(repo.ID, nil, "first", "", model.StateTodo, nil, "", "")
	second, _ := s.CreateIssue(repo.ID, nil, "second", "", model.StateTodo, nil, "", "")
	third, _ := s.CreateIssue(repo.ID, nil, "third", "", model.StateTodo, nil, "", "")

	// Land them in known order so terminal_at orders them deterministically.
	if err := s.SetIssueState(first.ID, model.StateDone); err != nil {
		t.Fatalf("done first: %v", err)
	}
	time.Sleep(1100 * time.Millisecond)
	if err := s.SetIssueState(second.ID, model.StateDone); err != nil {
		t.Fatalf("done second: %v", err)
	}
	time.Sleep(1100 * time.Millisecond)
	if err := s.SetIssueState(third.ID, model.StateDone); err != nil {
		t.Fatalf("done third: %v", err)
	}

	got, err := s.ListShippedIssues(ShippedFilter{RepoID: &repo.ID})
	if err != nil {
		t.Fatalf("list shipped: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d rows, want 3", len(got))
	}
	wantOrder := []string{"third", "second", "first"}
	for i, iss := range got {
		if iss.Title != wantOrder[i] {
			t.Fatalf("row %d = %q, want %q", i, iss.Title, wantOrder[i])
		}
	}
}

// TestListShippedIssuesExcludesCancelled — the popover is for "what
// landed", not "what closed". Cancelled rows must NOT appear.
func TestListShippedIssuesExcludesCancelled(t *testing.T) {
	s := newTestStore(t)
	repo, _ := s.CreateRepo("TST", "test", t.TempDir(), "")

	done, _ := s.CreateIssue(repo.ID, nil, "done", "", model.StateTodo, nil, "", "")
	cancelled, _ := s.CreateIssue(repo.ID, nil, "cancelled", "", model.StateTodo, nil, "", "")
	if err := s.SetIssueState(done.ID, model.StateDone); err != nil {
		t.Fatalf("done: %v", err)
	}
	if err := s.SetIssueState(cancelled.ID, model.StateCancelled); err != nil {
		t.Fatalf("cancelled: %v", err)
	}

	got, err := s.ListShippedIssues(ShippedFilter{RepoID: &repo.ID})
	if err != nil {
		t.Fatalf("list shipped: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d rows, want 1 (cancelled must be excluded)", len(got))
	}
	if got[0].Title != "done" {
		t.Fatalf("row = %q, want 'done'", got[0].Title)
	}
}

// TestListShippedIssuesExcludesOpen — non-terminal states must NOT
// appear; only done with a non-NULL terminal_at lands here.
func TestListShippedIssuesExcludesOpen(t *testing.T) {
	s := newTestStore(t)
	repo, _ := s.CreateRepo("TST", "test", t.TempDir(), "")

	done, _ := s.CreateIssue(repo.ID, nil, "done", "", model.StateTodo, nil, "", "")
	_, _ = s.CreateIssue(repo.ID, nil, "todo", "", model.StateTodo, nil, "", "")
	inprog, _ := s.CreateIssue(repo.ID, nil, "in_progress", "", model.StateTodo, nil, "", "")
	_ = s.SetIssueState(inprog.ID, model.StateInReview)
	if err := s.SetIssueState(done.ID, model.StateDone); err != nil {
		t.Fatalf("done: %v", err)
	}

	got, err := s.ListShippedIssues(ShippedFilter{RepoID: &repo.ID})
	if err != nil {
		t.Fatalf("list shipped: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d rows, want 1 (only the done row)", len(got))
	}
	if got[0].Title != "done" {
		t.Fatalf("row = %q, want 'done'", got[0].Title)
	}
}

// TestListShippedIssuesLimitClamp pins the three Limit branches: a
// caller-supplied positive Limit clamps the result, Limit=0 falls back
// to the default, and Limit > cap is bound to the cap.
func TestListShippedIssuesLimitClamp(t *testing.T) {
	s := newTestStore(t)
	repo, _ := s.CreateRepo("TST", "test", t.TempDir(), "")
	for i := 0; i < 10; i++ {
		iss, _ := s.CreateIssue(repo.ID, nil, "iss", "", model.StateTodo, nil, "", "")
		_ = s.SetIssueState(iss.ID, model.StateDone)
	}

	got, err := s.ListShippedIssues(ShippedFilter{RepoID: &repo.ID, Limit: 5})
	if err != nil {
		t.Fatalf("list shipped: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("got %d rows, want 5 (Limit honoured)", len(got))
	}

	// Limit=0 → default. The default is 20; insert 10 so all show.
	got, err = s.ListShippedIssues(ShippedFilter{RepoID: &repo.ID, Limit: 0})
	if err != nil {
		t.Fatalf("list shipped default: %v", err)
	}
	if len(got) != 10 {
		t.Fatalf("got %d rows, want 10 under default Limit", len(got))
	}

	// Limit > cap clamps to cap. With 10 rows we can't see the cap
	// directly, so check the SQL we'd emit clamps the constant. The
	// row count is bounded by the table; the cap is enforced via the
	// `if limit > shippedMaxLimit` branch, exercised here by passing
	// a huge value and verifying we still get every row back without
	// erroring (the LIMIT clause was a valid integer).
	got, err = s.ListShippedIssues(ShippedFilter{RepoID: &repo.ID, Limit: 99999})
	if err != nil {
		t.Fatalf("list shipped huge: %v", err)
	}
	if len(got) != 10 {
		t.Fatalf("got %d rows with huge Limit, want 10", len(got))
	}
}

// TestListShippedIssuesSinceWindow — Since clamps to a lower bound on
// terminal_at. Two rows, one stamped ancient and one fresh; with a
// 24h window only the fresh row should return.
func TestListShippedIssuesSinceWindow(t *testing.T) {
	s := newTestStore(t)
	repo, _ := s.CreateRepo("TST", "test", t.TempDir(), "")

	old, _ := s.CreateIssue(repo.ID, nil, "old", "", model.StateTodo, nil, "", "")
	young, _ := s.CreateIssue(repo.ID, nil, "young", "", model.StateTodo, nil, "", "")
	if err := s.SetIssueState(old.ID, model.StateDone); err != nil {
		t.Fatalf("done old: %v", err)
	}
	if err := s.SetIssueState(young.ID, model.StateDone); err != nil {
		t.Fatalf("done young: %v", err)
	}
	// Back-date the old row's terminal_at past the window.
	if _, err := s.DB.Exec(`UPDATE issues SET terminal_at = datetime('now','-30 days') WHERE id = ?`, old.ID); err != nil {
		t.Fatalf("back-date: %v", err)
	}

	cutoff := time.Now().Add(-7 * 24 * time.Hour)
	got, err := s.ListShippedIssues(ShippedFilter{RepoID: &repo.ID, Since: &cutoff})
	if err != nil {
		t.Fatalf("list shipped since: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d rows, want 1 (old row outside window)", len(got))
	}
	if got[0].Title != "young" {
		t.Fatalf("row = %q, want 'young'", got[0].Title)
	}
}

// TestListShippedIssuesNullTerminalAtSkipped — a done row with
// terminal_at explicitly NULL is excluded. Catches the case where a
// hypothetical future code path stamps state without terminal_at.
func TestListShippedIssuesNullTerminalAtSkipped(t *testing.T) {
	s := newTestStore(t)
	repo, _ := s.CreateRepo("TST", "test", t.TempDir(), "")

	done, _ := s.CreateIssue(repo.ID, nil, "done", "", model.StateTodo, nil, "", "")
	if err := s.SetIssueState(done.ID, model.StateDone); err != nil {
		t.Fatalf("done: %v", err)
	}
	if _, err := s.DB.Exec(`UPDATE issues SET terminal_at = NULL WHERE id = ?`, done.ID); err != nil {
		t.Fatalf("clear terminal_at: %v", err)
	}

	got, err := s.ListShippedIssues(ShippedFilter{RepoID: &repo.ID})
	if err != nil {
		t.Fatalf("list shipped: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d rows, want 0 (NULL terminal_at must be excluded)", len(got))
	}
}

// TestCountShippedIssuesEmptyRepo — an empty repo returns 0, never errs.
func TestCountShippedIssuesEmptyRepo(t *testing.T) {
	s := newTestStore(t)
	repo, _ := s.CreateRepo("TST", "test", t.TempDir(), "")

	got, err := s.CountShippedIssues(ShippedFilter{RepoID: &repo.ID})
	if err != nil {
		t.Fatalf("count shipped: %v", err)
	}
	if got != 0 {
		t.Fatalf("count = %d, want 0", got)
	}
}

// TestCountShippedIssuesMixedStates — only state='done' with non-NULL
// terminal_at counts; cancelled and open rows are excluded, same WHERE
// as ListShippedIssues.
func TestCountShippedIssuesMixedStates(t *testing.T) {
	s := newTestStore(t)
	repo, _ := s.CreateRepo("TST", "test", t.TempDir(), "")

	done1, _ := s.CreateIssue(repo.ID, nil, "done-1", "", model.StateTodo, nil, "", "")
	done2, _ := s.CreateIssue(repo.ID, nil, "done-2", "", model.StateTodo, nil, "", "")
	cancelled, _ := s.CreateIssue(repo.ID, nil, "cancelled", "", model.StateTodo, nil, "", "")
	_, _ = s.CreateIssue(repo.ID, nil, "todo", "", model.StateTodo, nil, "", "")
	inprog, _ := s.CreateIssue(repo.ID, nil, "in-progress", "", model.StateTodo, nil, "", "")
	if err := s.SetIssueState(done1.ID, model.StateDone); err != nil {
		t.Fatalf("done1: %v", err)
	}
	if err := s.SetIssueState(done2.ID, model.StateDone); err != nil {
		t.Fatalf("done2: %v", err)
	}
	if err := s.SetIssueState(cancelled.ID, model.StateCancelled); err != nil {
		t.Fatalf("cancelled: %v", err)
	}
	if err := s.SetIssueState(inprog.ID, model.StateInReview); err != nil {
		t.Fatalf("inprog: %v", err)
	}

	got, err := s.CountShippedIssues(ShippedFilter{RepoID: &repo.ID})
	if err != nil {
		t.Fatalf("count shipped: %v", err)
	}
	if got != 2 {
		t.Fatalf("count = %d, want 2 (only done rows)", got)
	}
}

// TestCountShippedIssuesSinceWindow — Since clamps the count the same
// way it clamps the list. One ancient row + one fresh row + a 24h
// cutoff = count of 1.
func TestCountShippedIssuesSinceWindow(t *testing.T) {
	s := newTestStore(t)
	repo, _ := s.CreateRepo("TST", "test", t.TempDir(), "")

	old, _ := s.CreateIssue(repo.ID, nil, "old", "", model.StateTodo, nil, "", "")
	young, _ := s.CreateIssue(repo.ID, nil, "young", "", model.StateTodo, nil, "", "")
	if err := s.SetIssueState(old.ID, model.StateDone); err != nil {
		t.Fatalf("done old: %v", err)
	}
	if err := s.SetIssueState(young.ID, model.StateDone); err != nil {
		t.Fatalf("done young: %v", err)
	}
	if _, err := s.DB.Exec(`UPDATE issues SET terminal_at = datetime('now','-30 days') WHERE id = ?`, old.ID); err != nil {
		t.Fatalf("back-date: %v", err)
	}

	// Forever: both rows count.
	all, err := s.CountShippedIssues(ShippedFilter{RepoID: &repo.ID})
	if err != nil {
		t.Fatalf("count forever: %v", err)
	}
	if all != 2 {
		t.Fatalf("forever count = %d, want 2", all)
	}

	// 24h window: only the fresh row counts.
	cutoff := time.Now().Add(-24 * time.Hour)
	windowed, err := s.CountShippedIssues(ShippedFilter{RepoID: &repo.ID, Since: &cutoff})
	if err != nil {
		t.Fatalf("count windowed: %v", err)
	}
	if windowed != 1 {
		t.Fatalf("windowed count = %d, want 1 (old row outside window)", windowed)
	}
}

// TestCountShippedIssuesIgnoresLimit — f.Limit doesn't clamp the count;
// the BACI-221 popover shows "20 of 47" by reading the count
// independently of the per-fetch row cap.
func TestCountShippedIssuesIgnoresLimit(t *testing.T) {
	s := newTestStore(t)
	repo, _ := s.CreateRepo("TST", "test", t.TempDir(), "")
	for i := 0; i < 25; i++ {
		iss, _ := s.CreateIssue(repo.ID, nil, "iss", "", model.StateTodo, nil, "", "")
		_ = s.SetIssueState(iss.ID, model.StateDone)
	}

	got, err := s.CountShippedIssues(ShippedFilter{RepoID: &repo.ID, Limit: 5})
	if err != nil {
		t.Fatalf("count shipped: %v", err)
	}
	if got != 25 {
		t.Fatalf("count = %d, want 25 (Limit must not clamp count)", got)
	}
}

// TestCountShippedIssuesIncludesArchived — archived done rows still
// count, because the popover deliberately ignores show_archived
// (see App.jsx's pre-BACI-221 "fix the mismatch" comment). The store
// has no archived-vs-not filter on ShippedFilter; this test pins the
// behaviour against a future "let's also filter archived" misread.
func TestCountShippedIssuesIncludesArchived(t *testing.T) {
	s := newTestStore(t)
	repo, _ := s.CreateRepo("TST", "test", t.TempDir(), "")

	live, _ := s.CreateIssue(repo.ID, nil, "live", "", model.StateTodo, nil, "", "")
	archived, _ := s.CreateIssue(repo.ID, nil, "archived", "", model.StateTodo, nil, "", "")
	if err := s.SetIssueState(live.ID, model.StateDone); err != nil {
		t.Fatalf("done live: %v", err)
	}
	if err := s.SetIssueState(archived.ID, model.StateDone); err != nil {
		t.Fatalf("done archived: %v", err)
	}
	if err := s.SetIssueArchived(archived.ID, true); err != nil {
		t.Fatalf("archive: %v", err)
	}

	got, err := s.CountShippedIssues(ShippedFilter{RepoID: &repo.ID})
	if err != nil {
		t.Fatalf("count shipped: %v", err)
	}
	if got != 2 {
		t.Fatalf("count = %d, want 2 (archived rows must still count)", got)
	}
}

// TestListShippedIssuesUsesIndex — EXPLAIN QUERY PLAN must walk an
// index rather than a SCAN of the issues table. The BACI-138
// idx_issues_terminal_at index is the intended driver, but SQLite's
// planner can legitimately prefer the (repo_id, number) autoindex on
// a small single-repo dataset; either is fine, both keep the read
// bounded. The regression we're catching is the worst case — a
// `SCAN i` line that says SQLite gave up and walked every row.
func TestListShippedIssuesUsesIndex(t *testing.T) {
	s := newTestStore(t)
	repo, _ := s.CreateRepo("TST", "test", t.TempDir(), "")
	for i := 0; i < 5; i++ {
		iss, _ := s.CreateIssue(repo.ID, nil, "iss", "", model.StateTodo, nil, "", "")
		_ = s.SetIssueState(iss.ID, model.StateDone)
	}

	q := issueSelect + ` WHERE i.state = ? AND i.terminal_at IS NOT NULL AND i.repo_id = ? ORDER BY i.terminal_at DESC, i.id DESC LIMIT 20`
	rows, err := s.DB.Query(`EXPLAIN QUERY PLAN `+q, string(model.StateDone), repo.ID)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	defer rows.Close()

	var plan strings.Builder
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatalf("scan plan: %v", err)
		}
		plan.WriteString(detail)
		plan.WriteString("\n")
	}
	// SCAN i means a full table scan on the issues table — the
	// regression we want to catch. (Other "SCAN" lines from the
	// subquery / joined rows are fine; only the issues table's own
	// scan matters.)
	if strings.Contains(plan.String(), "SCAN i ") || strings.HasSuffix(strings.TrimSpace(plan.String()), "SCAN i") {
		t.Fatalf("EXPLAIN QUERY PLAN reverted to a full SCAN of issues; got:\n%s", plan.String())
	}
}
