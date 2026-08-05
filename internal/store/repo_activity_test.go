package store

import (
	"testing"
	"time"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// sqliteStamp renders a time the way SQLite's CURRENT_TIMESTAMP does, so
// backdated fixtures compare lexicographically against real rows.
func sqliteStamp(t time.Time) string {
	return t.UTC().Format("2006-01-02 15:04:05")
}

// activityByPrefix indexes a ListRepoActivity result for assertions.
func activityByPrefix(t *testing.T, s *Store) map[string]RepoActivity {
	t.Helper()
	rows, err := s.ListRepoActivity()
	if err != nil {
		t.Fatalf("ListRepoActivity: %v", err)
	}
	out := map[string]RepoActivity{}
	for _, r := range rows {
		out[r.Prefix] = r
	}
	return out
}

// TestListRepoActivityOrdersBySignals covers the two-signal merge: a
// history row is the primary recency signal, and issues.updated_at is the
// floor for a repo whose audit rows have been pruned.
func TestListRepoActivityOrdersBySignals(t *testing.T) {
	s := newTestStore(t)
	recent, err := s.CreateRepo("RCNT", "recent", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create recent repo: %v", err)
	}
	stale, err := s.CreateRepo("STAL", "stale", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create stale repo: %v", err)
	}
	// The stale repo has an issue only — no history at all.
	staleIssue, err := s.CreateIssue(stale.ID, nil, "old", "", model.StateTodo, nil, "", "")
	if err != nil {
		t.Fatalf("create stale issue: %v", err)
	}
	if _, err := s.DB.Exec(`UPDATE issues SET updated_at = ? WHERE id = ?`,
		sqliteStamp(time.Now().Add(-30*24*time.Hour)), staleIssue.ID); err != nil {
		t.Fatalf("backdate stale issue: %v", err)
	}
	// The recent repo has a history row from an hour ago and no issues.
	if _, err := s.DB.Exec(
		`INSERT INTO history (repo_id, repo_prefix, actor, op, kind, created_at) VALUES (?, ?, 'user', 'issue.update', 'issue', ?)`,
		recent.ID, recent.Prefix, sqliteStamp(time.Now().Add(-time.Hour))); err != nil {
		t.Fatalf("insert history: %v", err)
	}

	got := activityByPrefix(t, s)
	if got["RCNT"].LastActivityAt == nil {
		t.Fatal("RCNT last activity is nil, want the history timestamp")
	}
	if got["STAL"].LastActivityAt == nil {
		t.Fatal("STAL last activity is nil, want the issue updated_at floor")
	}
	if !got["RCNT"].LastActivityAt.After(*got["STAL"].LastActivityAt) {
		t.Errorf("RCNT (%v) should be more recent than STAL (%v)",
			got["RCNT"].LastActivityAt, got["STAL"].LastActivityAt)
	}

	// Now touch the stale repo's issue: the issue timestamp overtakes an
	// older history row on the same repo, so the later of the two wins.
	if _, err := s.DB.Exec(
		`INSERT INTO history (repo_id, repo_prefix, actor, op, kind, created_at) VALUES (?, ?, 'user', 'issue.create', 'issue', ?)`,
		stale.ID, stale.Prefix, sqliteStamp(time.Now().Add(-48*time.Hour))); err != nil {
		t.Fatalf("insert stale history: %v", err)
	}
	if _, err := s.DB.Exec(`UPDATE issues SET updated_at = ? WHERE id = ?`,
		sqliteStamp(time.Now().Add(-time.Minute)), staleIssue.ID); err != nil {
		t.Fatalf("touch stale issue: %v", err)
	}
	got = activityByPrefix(t, s)
	if !got["STAL"].LastActivityAt.After(*got["RCNT"].LastActivityAt) {
		t.Errorf("touched STAL (%v) should now beat RCNT (%v)",
			got["STAL"].LastActivityAt, got["RCNT"].LastActivityAt)
	}
}

// TestListRepoActivityCountsRunningJobs locks in that the in-flight count
// is scoped to the repo's own issues and to running jobs only.
func TestListRepoActivityCountsRunningJobs(t *testing.T) {
	s, repo, iss := seedRepoAndIssue(t)
	other, err := s.CreateRepo("OTHR", "other", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create other repo: %v", err)
	}
	otherIssue, err := s.CreateIssue(other.ID, nil, "sibling", "", model.StateTodo, nil, "", "")
	if err != nil {
		t.Fatalf("create other issue: %v", err)
	}
	// A second issue in the target repo whose chain is completed, not
	// running — it must not inflate the count.
	doneIssue, err := s.CreateIssue(repo.ID, nil, "finished", "", model.StateTodo, nil, "", "")
	if err != nil {
		t.Fatalf("create done issue: %v", err)
	}

	proc, err := model.ProcessBySlug("plan-implement-ship")
	if err != nil {
		t.Fatalf("ProcessBySlug: %v", err)
	}
	start := func(issueID int64, status model.JobStatus) {
		t.Helper()
		jobs, err := s.SetIssueProcess(issueID, proc)
		if err != nil {
			t.Fatalf("SetIssueProcess: %v", err)
		}
		if err := s.SetPipelineJobStatus(jobs[0].ID, status); err != nil {
			t.Fatalf("SetPipelineJobStatus %s: %v", status, err)
		}
	}
	start(iss.ID, model.JobRunning)
	start(doneIssue.ID, model.JobComplete)
	start(otherIssue.ID, model.JobRunning)

	got := activityByPrefix(t, s)
	if got[repo.Prefix].ActiveJobs != 1 {
		t.Errorf("%s active jobs = %d, want 1", repo.Prefix, got[repo.Prefix].ActiveJobs)
	}
	if got["OTHR"].ActiveJobs != 1 {
		t.Errorf("OTHR active jobs = %d, want 1 (its own running job)", got["OTHR"].ActiveJobs)
	}
}

// TestListRepoActivityEmptyRepo guards that a repo with no history, no
// issues, and no jobs still comes back — dropping it would erase it from
// the picker's ordering input.
func TestListRepoActivityEmptyRepo(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.CreateRepo("EMPT", "empty", t.TempDir(), ""); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	got := activityByPrefix(t, s)
	row, ok := got["EMPT"]
	if !ok {
		t.Fatal("empty repo missing from ListRepoActivity")
	}
	if row.LastActivityAt != nil {
		t.Errorf("last activity = %v, want nil", row.LastActivityAt)
	}
	if row.ActiveJobs != 0 {
		t.Errorf("active jobs = %d, want 0", row.ActiveJobs)
	}
}
