package store

import (
	"errors"
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// laneOrder returns the titles of the issues in a lane, ordered by the
// stored kanban_position, plus the raw positions so density can be
// asserted.
func laneOrder(t *testing.T, s *Store, columnID int64) ([]string, []int) {
	t.Helper()
	rows, err := s.DB.Query(
		`SELECT title, kanban_position FROM issues WHERE kanban_column_id = ? ORDER BY kanban_position ASC, number ASC`,
		columnID,
	)
	if err != nil {
		t.Fatalf("lane query: %v", err)
	}
	defer rows.Close()
	var titles []string
	var positions []int
	for rows.Next() {
		var title string
		var pos int
		if err := rows.Scan(&title, &pos); err != nil {
			t.Fatalf("lane scan: %v", err)
		}
		titles = append(titles, title)
		positions = append(positions, pos)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("lane rows: %v", err)
	}
	return titles, positions
}

// TestSetIssueKanbanColumnOnAndOff walks a card onto the board, around
// the board, and off it again, checking the NULL rule and the dense
// per-lane ordering at every step.
func TestSetIssueKanbanColumnOnAndOff(t *testing.T) {
	s, repo := kanbanFixture(t)
	if err := s.BootstrapKanbanColumns(repo.ID); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	cols, err := s.ListKanbanColumns(repo.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	backlog, doing := cols[0], cols[1]

	iss, err := s.CreateIssue(repo.ID, nil, "card", "", model.StateTodo, nil, "", "")
	if err != nil {
		t.Fatalf("create issue: %v", err)
	}
	// A git repo's new issue starts OFF the board — that NULL is the
	// whole double-render defence.
	if iss.KanbanColumnID != nil {
		t.Fatalf("new issue landed on lane %d, want off the board (NULL)", *iss.KanbanColumnID)
	}
	if iss.KanbanPosition != 0 {
		t.Fatalf("new issue kanban_position = %d, want 0", iss.KanbanPosition)
	}

	if err := s.SetIssueKanbanColumn(iss.ID, &backlog.ID, 0); err != nil {
		t.Fatalf("onto the board: %v", err)
	}
	got, err := s.GetIssueByID(iss.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.KanbanColumnID == nil || *got.KanbanColumnID != backlog.ID {
		t.Fatalf("KanbanColumnID = %v, want %d", got.KanbanColumnID, backlog.ID)
	}
	if got.KanbanPosition != 0 {
		t.Fatalf("KanbanPosition = %d, want 0", got.KanbanPosition)
	}

	// Moving between lanes clears the old lane and lands in the new.
	if err := s.SetIssueKanbanColumn(iss.ID, &doing.ID, 0); err != nil {
		t.Fatalf("between lanes: %v", err)
	}
	if titles, _ := laneOrder(t, s, backlog.ID); len(titles) != 0 {
		t.Fatalf("source lane still holds %v", titles)
	}
	if titles, positions := laneOrder(t, s, doing.ID); strings.Join(titles, ",") != "card" || positions[0] != 0 {
		t.Fatalf("target lane = %v at %v", titles, positions)
	}

	// nil takes it off the board, and resets the in-lane index.
	if err := s.SetIssueKanbanColumn(iss.ID, nil, 0); err != nil {
		t.Fatalf("off the board: %v", err)
	}
	got, err = s.GetIssueByID(iss.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.KanbanColumnID != nil {
		t.Fatalf("still on lane %d after nil move", *got.KanbanColumnID)
	}
	if got.KanbanPosition != 0 {
		t.Fatalf("KanbanPosition = %d after leaving the board, want 0", got.KanbanPosition)
	}
	// Taking an already-off card off again is a no-op, not an error.
	if err := s.SetIssueKanbanColumn(iss.ID, nil, 0); err != nil {
		t.Fatalf("idempotent off-board move: %v", err)
	}

	// The issue itself survived every move.
	if got.Title != "card" || got.State != model.StateTodo {
		t.Fatalf("board moves mutated the issue: %+v", got)
	}
}

// TestSetIssueKanbanColumnOrdering is table-driven over "insert the new
// card at 0-based slot N of a three-card lane", asserting the splice and
// the dense renumber.
func TestSetIssueKanbanColumnOrdering(t *testing.T) {
	cases := []struct {
		name string
		at   int
		want string
	}{
		{"top", 0, "new,a,b,c"},
		{"second", 1, "a,new,b,c"},
		{"third", 2, "a,b,new,c"},
		{"append exact", 3, "a,b,c,new"},
		{"append clamped", 99, "a,b,c,new"},
		{"negative clamped to top", -4, "new,a,b,c"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, repo := kanbanFixture(t)
			lane, err := s.CreateKanbanColumn(repo.ID, "Doing")
			if err != nil {
				t.Fatalf("seed lane: %v", err)
			}
			for _, title := range []string{"a", "b", "c"} {
				iss, err := s.CreateIssue(repo.ID, nil, title, "", model.StateTodo, nil, "", "")
				if err != nil {
					t.Fatalf("create %q: %v", title, err)
				}
				if err := s.SetIssueKanbanColumn(iss.ID, &lane.ID, 99); err != nil {
					t.Fatalf("append %q: %v", title, err)
				}
			}
			if titles, positions := laneOrder(t, s, lane.ID); strings.Join(titles, ",") != "a,b,c" {
				t.Fatalf("seeded lane = %v at %v", titles, positions)
			}
			fresh, err := s.CreateIssue(repo.ID, nil, "new", "", model.StateTodo, nil, "", "")
			if err != nil {
				t.Fatalf("create new: %v", err)
			}
			if err := s.SetIssueKanbanColumn(fresh.ID, &lane.ID, tc.at); err != nil {
				t.Fatalf("insert at %d: %v", tc.at, err)
			}
			titles, positions := laneOrder(t, s, lane.ID)
			if got := strings.Join(titles, ","); got != tc.want {
				t.Fatalf("lane after insert at %d = %s, want %s", tc.at, got, tc.want)
			}
			for i, p := range positions {
				if p != i {
					t.Fatalf("lane positions not dense: %v", positions)
				}
			}
		})
	}
}

// TestSetIssueKanbanColumnRenumbersSourceLane checks that pulling a card
// out of the middle of a lane closes the gap it left behind.
func TestSetIssueKanbanColumnRenumbersSourceLane(t *testing.T) {
	s, repo := kanbanFixture(t)
	src, err := s.CreateKanbanColumn(repo.ID, "Doing")
	if err != nil {
		t.Fatalf("seed lane: %v", err)
	}
	dst, err := s.CreateKanbanColumn(repo.ID, "Done")
	if err != nil {
		t.Fatalf("seed lane: %v", err)
	}
	byTitle := map[string]int64{}
	for _, title := range []string{"a", "b", "c", "d"} {
		iss, err := s.CreateIssue(repo.ID, nil, title, "", model.StateTodo, nil, "", "")
		if err != nil {
			t.Fatalf("create %q: %v", title, err)
		}
		if err := s.SetIssueKanbanColumn(iss.ID, &src.ID, 99); err != nil {
			t.Fatalf("append %q: %v", title, err)
		}
		byTitle[title] = iss.ID
	}
	if err := s.SetIssueKanbanColumn(byTitle["b"], &dst.ID, 0); err != nil {
		t.Fatalf("move b across: %v", err)
	}
	titles, positions := laneOrder(t, s, src.ID)
	if strings.Join(titles, ",") != "a,c,d" {
		t.Fatalf("source lane = %v", titles)
	}
	for i, p := range positions {
		if p != i {
			t.Fatalf("source lane left a gap: %v", positions)
		}
	}
	if titles, positions = laneOrder(t, s, dst.ID); strings.Join(titles, ",") != "b" || positions[0] != 0 {
		t.Fatalf("target lane = %v at %v", titles, positions)
	}
}

// TestSetIssueKanbanColumnValidation pins the store-boundary refusals: a
// missing issue, a missing column, and a column belonging to a different
// repo than the issue (which the bare FK would happily accept).
func TestSetIssueKanbanColumnValidation(t *testing.T) {
	s, repo := kanbanFixture(t)
	lane, err := s.CreateKanbanColumn(repo.ID, "Doing")
	if err != nil {
		t.Fatalf("seed lane: %v", err)
	}
	other, err := s.CreateRepo("OTH", "other", t.TempDir(), "")
	if err != nil {
		t.Fatalf("second repo: %v", err)
	}
	otherLane, err := s.CreateKanbanColumn(other.ID, "Doing")
	if err != nil {
		t.Fatalf("seed other lane: %v", err)
	}
	iss, err := s.CreateIssue(repo.ID, nil, "card", "", model.StateTodo, nil, "", "")
	if err != nil {
		t.Fatalf("create issue: %v", err)
	}

	if err := s.SetIssueKanbanColumn(999999, &lane.ID, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing issue = %v, want ErrNotFound", err)
	}
	missing := int64(999999)
	if err := s.SetIssueKanbanColumn(iss.ID, &missing, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing column = %v, want ErrNotFound", err)
	}
	err = s.SetIssueKanbanColumn(iss.ID, &otherLane.ID, 0)
	if err == nil {
		t.Fatal("expected a cross-repo lane move to be refused")
	}
	if !strings.Contains(err.Error(), "different repo") {
		t.Fatalf("cross-repo error = %q, want a message naming the mismatch", err)
	}
	// None of the refusals put the card on a board.
	if got, gerr := s.GetIssueByID(iss.ID); gerr != nil {
		t.Fatalf("get: %v", gerr)
	} else if got.KanbanColumnID != nil {
		t.Fatalf("a refused move still placed the card on lane %d", *got.KanbanColumnID)
	}
}

// TestIssueFilterOnKanban exercises the three states of the OnKanban
// filter: nil (no constraint — every pre-existing caller), true (on the
// board) and false (off it).
func TestIssueFilterOnKanban(t *testing.T) {
	s, repo := kanbanFixture(t)
	lane, err := s.CreateKanbanColumn(repo.ID, "Doing")
	if err != nil {
		t.Fatalf("seed lane: %v", err)
	}
	for _, title := range []string{"on-1", "on-2"} {
		iss, err := s.CreateIssue(repo.ID, nil, title, "", model.StateTodo, nil, "", "")
		if err != nil {
			t.Fatalf("create %q: %v", title, err)
		}
		if err := s.SetIssueKanbanColumn(iss.ID, &lane.ID, 99); err != nil {
			t.Fatalf("onto the board: %v", err)
		}
	}
	for _, title := range []string{"off-1", "off-2", "off-3"} {
		if _, err := s.CreateIssue(repo.ID, nil, title, "", model.StateTodo, nil, "", ""); err != nil {
			t.Fatalf("create %q: %v", title, err)
		}
	}

	yes, no := true, false
	cases := []struct {
		name   string
		filter *bool
		want   []string
	}{
		{"nil is unconstrained", nil, []string{"on-1", "on-2", "off-1", "off-2", "off-3"}},
		{"true is on the board", &yes, []string{"on-1", "on-2"}},
		{"false is off the board", &no, []string{"off-1", "off-2", "off-3"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := s.ListIssues(IssueFilter{RepoID: &repo.ID, OnKanban: tc.filter})
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			if len(got) != len(tc.want) {
				titles := make([]string, len(got))
				for i, iss := range got {
					titles[i] = iss.Title
				}
				t.Fatalf("got %v, want %v", titles, tc.want)
			}
			seen := map[string]bool{}
			for _, iss := range got {
				seen[iss.Title] = true
			}
			for _, want := range tc.want {
				if !seen[want] {
					t.Fatalf("missing %q from the %s result", want, tc.name)
				}
			}
		})
	}

	// The filter composes with the existing axes rather than replacing
	// them — state is untouched by any of this.
	got, err := s.ListIssues(IssueFilter{
		RepoID:   &repo.ID,
		States:   []model.State{model.StateTodo},
		OnKanban: &yes,
	})
	if err != nil {
		t.Fatalf("composed list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("state+kanban filter returned %d rows, want 2", len(got))
	}
}

// TestIssueKanbanFieldsRoundTrip checks the new columns actually reach
// every issue read path (issueSelect powers GetIssueByID / ByKey /
// ByUUID / ListIssues alike).
func TestIssueKanbanFieldsRoundTrip(t *testing.T) {
	s, repo := kanbanFixture(t)
	lane, err := s.CreateKanbanColumn(repo.ID, "Doing")
	if err != nil {
		t.Fatalf("seed lane: %v", err)
	}
	first, err := s.CreateIssue(repo.ID, nil, "first", "", model.StateTodo, nil, "", "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	second, err := s.CreateIssue(repo.ID, nil, "second", "", model.StateTodo, nil, "", "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.SetIssueKanbanColumn(first.ID, &lane.ID, 0); err != nil {
		t.Fatalf("place first: %v", err)
	}
	if err := s.SetIssueKanbanColumn(second.ID, &lane.ID, 0); err != nil {
		t.Fatalf("place second above first: %v", err)
	}

	check := func(what string, iss *model.Issue) {
		t.Helper()
		if iss.KanbanColumnID == nil || *iss.KanbanColumnID != lane.ID {
			t.Fatalf("%s: KanbanColumnID = %v, want %d", what, iss.KanbanColumnID, lane.ID)
		}
		if iss.KanbanPosition != 0 {
			t.Fatalf("%s: KanbanPosition = %d, want 0", what, iss.KanbanPosition)
		}
	}
	byID, err := s.GetIssueByID(second.ID)
	if err != nil {
		t.Fatalf("by id: %v", err)
	}
	check("GetIssueByID", byID)
	byKey, err := s.GetIssueByKey("KAN", second.Number)
	if err != nil {
		t.Fatalf("by key: %v", err)
	}
	check("GetIssueByKey", byKey)
	byUUID, err := s.GetIssueByUUID(second.UUID)
	if err != nil {
		t.Fatalf("by uuid: %v", err)
	}
	check("GetIssueByUUID", byUUID)

	listed, err := s.ListIssues(IssueFilter{RepoID: &repo.ID})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, iss := range listed {
		if iss.ID == second.ID {
			check("ListIssues", iss)
		}
		if iss.ID == first.ID && iss.KanbanPosition != 1 {
			t.Fatalf("ListIssues: first card position = %d, want 1 (pushed down)", iss.KanbanPosition)
		}
	}
}

// TestSetIssueKanbanColumnDoesNotChurnIssueUpdatedAt pins the sync
// contract: board membership does not serialise into issue.yaml, so a
// board move must NOT bump issues.updated_at (which is the sync
// last-writer-wins key). The affected LANE's updated_at is bumped
// instead — membership lives on the container record.
func TestSetIssueKanbanColumnDoesNotChurnIssueUpdatedAt(t *testing.T) {
	s, repo := kanbanFixture(t)
	lane, err := s.CreateKanbanColumn(repo.ID, "Doing")
	if err != nil {
		t.Fatalf("seed lane: %v", err)
	}
	iss, err := s.CreateIssue(repo.ID, nil, "card", "", model.StateTodo, nil, "", "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Push both updated_at stamps into the past so any bump is visible
	// even at SQLite's one-second CURRENT_TIMESTAMP resolution.
	if _, err := s.DB.Exec(`UPDATE issues SET updated_at = '2020-01-01 00:00:00' WHERE id = ?`, iss.ID); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	if _, err := s.DB.Exec(`UPDATE kanban_columns SET updated_at = '2020-01-01 00:00:00' WHERE id = ?`, lane.ID); err != nil {
		t.Fatalf("backdate lane: %v", err)
	}

	if err := s.SetIssueKanbanColumn(iss.ID, &lane.ID, 0); err != nil {
		t.Fatalf("place: %v", err)
	}
	var issUpdated string
	if err := s.DB.QueryRow(`SELECT updated_at FROM issues WHERE id = ?`, iss.ID).Scan(&issUpdated); err != nil {
		t.Fatalf("read issue updated_at: %v", err)
	}
	if !strings.HasPrefix(issUpdated, "2020-01-01") {
		t.Fatalf("issues.updated_at was bumped by a board move (%s) — that churns the sync LWW gate for a field that never reaches issue.yaml", issUpdated)
	}
	var laneUpdated string
	if err := s.DB.QueryRow(`SELECT updated_at FROM kanban_columns WHERE id = ?`, lane.ID).Scan(&laneUpdated); err != nil {
		t.Fatalf("read lane updated_at: %v", err)
	}
	if strings.HasPrefix(laneUpdated, "2020-01-01") {
		t.Fatal("the lane's updated_at was NOT bumped — sync resolves membership by the container's updated_at")
	}
}
