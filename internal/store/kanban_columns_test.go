package store

import (
	"errors"
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// kanbanFixture spins up a store with one repo and returns both.
func kanbanFixture(t *testing.T) (*Store, *model.Repo) {
	t.Helper()
	s := newTestStore(t)
	repo, err := s.CreateRepo("KAN", "kanban", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	return s, repo
}

// columnNames returns the repo's lanes in board order, plus their stored
// positions, so a test can assert order and density in one shot.
func columnNames(t *testing.T, s *Store, repoID int64) ([]string, []int) {
	t.Helper()
	cols, err := s.ListKanbanColumns(repoID)
	if err != nil {
		t.Fatalf("list kanban columns: %v", err)
	}
	names := make([]string, len(cols))
	positions := make([]int, len(cols))
	for i, c := range cols {
		names[i] = c.Name
		positions[i] = c.Position
	}
	return names, positions
}

func assertDense(t *testing.T, positions []int) {
	t.Helper()
	for i, p := range positions {
		if p != i {
			t.Fatalf("positions not dense: %v (index %d has position %d)", positions, i, p)
		}
	}
}

// TestKanbanColumnCRUD walks create → read (id / uuid / name) → rename →
// delete, asserting the dense 0-based position invariant survives each
// step and that a delete renumbers the survivors.
func TestKanbanColumnCRUD(t *testing.T) {
	s, repo := kanbanFixture(t)

	if cols, err := s.ListKanbanColumns(repo.ID); err != nil {
		t.Fatalf("list on empty board: %v", err)
	} else if len(cols) != 0 {
		t.Fatalf("fresh repo has %d columns, want 0", len(cols))
	}

	for i, name := range []string{"Backlog", "Doing", "Done"} {
		col, err := s.CreateKanbanColumn(repo.ID, name)
		if err != nil {
			t.Fatalf("create %q: %v", name, err)
		}
		if col.Position != i {
			t.Fatalf("create %q position = %d, want %d (append at the right-hand end)", name, col.Position, i)
		}
		if col.UUID == "" {
			t.Fatalf("create %q: uuid not populated", name)
		}
		if col.RepoID != repo.ID {
			t.Fatalf("create %q repo_id = %d, want %d", name, col.RepoID, repo.ID)
		}
	}

	names, positions := columnNames(t, s, repo.ID)
	if strings.Join(names, ",") != "Backlog,Doing,Done" {
		t.Fatalf("board order = %v, want [Backlog Doing Done]", names)
	}
	assertDense(t, positions)

	doing, err := s.GetKanbanColumnByName(repo.ID, "Doing")
	if err != nil {
		t.Fatalf("get by name: %v", err)
	}
	byID, err := s.GetKanbanColumnByID(doing.ID)
	if err != nil {
		t.Fatalf("get by id: %v", err)
	}
	byUUID, err := s.GetKanbanColumnByUUID(doing.UUID)
	if err != nil {
		t.Fatalf("get by uuid: %v", err)
	}
	if byID.ID != doing.ID || byUUID.ID != doing.ID {
		t.Fatalf("three lookups disagree: name=%d id=%d uuid=%d", doing.ID, byID.ID, byUUID.ID)
	}

	// Names are trimmed/validated on the way in and on lookup, so a
	// padded lookup key resolves the same row... except that leading /
	// trailing whitespace is rejected outright by the validator.
	if _, err := s.GetKanbanColumnByName(repo.ID, " Doing "); err == nil {
		t.Fatal("expected padded name lookup to be rejected by the validator")
	}

	if err := s.RenameKanbanColumn(doing.ID, "In Progress"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	renamed, err := s.GetKanbanColumnByID(doing.ID)
	if err != nil {
		t.Fatalf("get after rename: %v", err)
	}
	if renamed.Name != "In Progress" {
		t.Fatalf("name after rename = %q, want %q", renamed.Name, "In Progress")
	}
	if renamed.Position != 1 {
		t.Fatalf("rename moved the lane: position = %d, want 1", renamed.Position)
	}
	if renamed.UpdatedAt.Before(renamed.CreatedAt) {
		t.Fatalf("updated_at went backwards on rename")
	}
	// The old name is free again.
	if _, err := s.GetKanbanColumnByName(repo.ID, "Doing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("lookup of the vacated name = %v, want ErrNotFound", err)
	}

	if err := s.DeleteKanbanColumn(doing.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	names, positions = columnNames(t, s, repo.ID)
	if strings.Join(names, ",") != "Backlog,Done" {
		t.Fatalf("board after delete = %v, want [Backlog Done]", names)
	}
	assertDense(t, positions)

	if _, err := s.GetKanbanColumnByID(doing.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get deleted column = %v, want ErrNotFound", err)
	}
	if err := s.DeleteKanbanColumn(doing.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete of a missing column = %v, want ErrNotFound", err)
	}
	if err := s.RenameKanbanColumn(doing.ID, "Nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rename of a missing column = %v, want ErrNotFound", err)
	}
	if err := s.ReorderKanbanColumn(doing.ID, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("reorder of a missing column = %v, want ErrNotFound", err)
	}
}

// TestKanbanColumnDuplicateName pins the uniq_kanban_columns_name index
// behaviour at the store boundary: a duplicate name in the SAME repo is
// refused with a readable message on both create and rename, while the
// same name in a DIFFERENT repo is fine (the index is per-repo).
func TestKanbanColumnDuplicateName(t *testing.T) {
	s, repo := kanbanFixture(t)
	if _, err := s.CreateKanbanColumn(repo.ID, "Doing"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	waiting, err := s.CreateKanbanColumn(repo.ID, "Waiting")
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	_, err = s.CreateKanbanColumn(repo.ID, "Doing")
	if err == nil {
		t.Fatal("expected duplicate-name create to fail")
	}
	if !strings.Contains(err.Error(), "already exists") || !strings.Contains(err.Error(), "Doing") {
		t.Fatalf("duplicate-name create error = %q, want a clear message naming the column", err)
	}
	// The failed create must not have left a half-written row or moved
	// the position counter.
	names, positions := columnNames(t, s, repo.ID)
	if len(names) != 2 {
		t.Fatalf("board after failed create = %v, want 2 lanes", names)
	}
	assertDense(t, positions)

	if err := s.RenameKanbanColumn(waiting.ID, "Doing"); err == nil {
		t.Fatal("expected duplicate-name rename to fail")
	} else if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate-name rename error = %q, want a clear message", err)
	}

	// Renaming a lane to the name it already has is allowed (the
	// exclude-self clause) and is not a collision.
	if err := s.RenameKanbanColumn(waiting.ID, "Waiting"); err != nil {
		t.Fatalf("self-rename: %v", err)
	}

	other, err := s.CreateRepo("OTH", "other", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create second repo: %v", err)
	}
	if _, err := s.CreateKanbanColumn(other.ID, "Doing"); err != nil {
		t.Fatalf("same name in a different repo must be allowed: %v", err)
	}
}

// TestKanbanColumnNameValidation confirms create and rename both funnel
// through ValidateKanbanColumnName rather than carrying a second copy of
// the rules.
func TestKanbanColumnNameValidation(t *testing.T) {
	s, repo := kanbanFixture(t)
	seed, err := s.CreateKanbanColumn(repo.ID, "Backlog")
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	cases := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"leading whitespace", " Doing"},
		{"trailing whitespace", "Doing "},
		{"slash", "Doing/Soon"},
		{"backslash", `Doing\Soon`},
		{"dot", "."},
		{"dotdot", ".."},
		{"control char", "Do\x01ing"},
		{"too long", strings.Repeat("x", 81)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.CreateKanbanColumn(repo.ID, tc.input); err == nil {
				t.Fatalf("CreateKanbanColumn(%q) succeeded, want a validation error", tc.input)
			}
			if err := s.RenameKanbanColumn(seed.ID, tc.input); err == nil {
				t.Fatalf("RenameKanbanColumn(%q) succeeded, want a validation error", tc.input)
			}
		})
	}
	// Nothing bogus landed.
	names, positions := columnNames(t, s, repo.ID)
	if strings.Join(names, ",") != "Backlog" {
		t.Fatalf("board after rejected writes = %v, want [Backlog]", names)
	}
	assertDense(t, positions)
}

// TestReorderKanbanColumn is table-driven over "move lane X to 0-based
// slot N" and asserts both the resulting order and that positions stay
// dense. Out-of-range slots clamp rather than error.
func TestReorderKanbanColumn(t *testing.T) {
	cases := []struct {
		name  string
		move  string
		to    int
		want  string
		start []string
	}{
		{name: "front to back", move: "A", to: 3, want: "B,C,D,A"},
		{name: "back to front", move: "D", to: 0, want: "D,A,B,C"},
		{name: "middle right one", move: "B", to: 2, want: "A,C,B,D"},
		{name: "middle left one", move: "C", to: 1, want: "A,C,B,D"},
		{name: "no-op self position", move: "C", to: 2, want: "A,B,C,D"},
		{name: "clamp above range", move: "A", to: 99, want: "B,C,D,A"},
		{name: "clamp below range", move: "D", to: -5, want: "D,A,B,C"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, repo := kanbanFixture(t)
			byName := map[string]int64{}
			for _, n := range []string{"A", "B", "C", "D"} {
				col, err := s.CreateKanbanColumn(repo.ID, n)
				if err != nil {
					t.Fatalf("seed %q: %v", n, err)
				}
				byName[n] = col.ID
			}
			if err := s.ReorderKanbanColumn(byName[tc.move], tc.to); err != nil {
				t.Fatalf("reorder: %v", err)
			}
			names, positions := columnNames(t, s, repo.ID)
			if got := strings.Join(names, ","); got != tc.want {
				t.Fatalf("order after moving %s to %d = %s, want %s", tc.move, tc.to, got, tc.want)
			}
			assertDense(t, positions)
		})
	}
}

// TestReorderKanbanColumnRoundTrip pins the 0-based contract: reading a
// lane's Position and handing it straight back must be a no-op. (This is
// the axis where ReorderKanbanColumn deliberately differs from
// ReorderIssue, whose position argument is 1-based.)
func TestReorderKanbanColumnRoundTrip(t *testing.T) {
	s, repo := kanbanFixture(t)
	for _, n := range []string{"A", "B", "C"} {
		if _, err := s.CreateKanbanColumn(repo.ID, n); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	cols, err := s.ListKanbanColumns(repo.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, c := range cols {
		if err := s.ReorderKanbanColumn(c.ID, c.Position); err != nil {
			t.Fatalf("round-trip reorder of %q: %v", c.Name, err)
		}
	}
	names, positions := columnNames(t, s, repo.ID)
	if strings.Join(names, ",") != "A,B,C" {
		t.Fatalf("round-trip reorder changed the board: %v", names)
	}
	assertDense(t, positions)
}

// TestBootstrapKanbanColumns covers the seeded starter board and the
// idempotency contract BootstrapRepoDefaults depends on — it sits on the
// repo-resolve hot path and is called on every resolve.
func TestBootstrapKanbanColumns(t *testing.T) {
	s, repo := kanbanFixture(t)

	if err := s.BootstrapKanbanColumns(repo.ID); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	names, positions := columnNames(t, s, repo.ID)
	if got := strings.Join(names, ","); got != "Backlog,Doing,Waiting,Done" {
		t.Fatalf("seeded board = %s, want Backlog,Doing,Waiting,Done", got)
	}
	assertDense(t, positions)

	// Second call is a no-op — same ids, same names, same positions.
	before, err := s.ListKanbanColumns(repo.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if err := s.BootstrapKanbanColumns(repo.ID); err != nil {
		t.Fatalf("second bootstrap: %v", err)
	}
	after, err := s.ListKanbanColumns(repo.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("second bootstrap changed the board size: %d -> %d", len(before), len(after))
	}
	for i := range before {
		if before[i].ID != after[i].ID || before[i].Name != after[i].Name || before[i].Position != after[i].Position {
			t.Fatalf("second bootstrap mutated lane %d: %+v -> %+v", i, before[i], after[i])
		}
	}

	// A repo whose board the user has customised must not have the
	// defaults forced back on. Rename one and delete the rest, then
	// bootstrap again.
	for _, c := range after[1:] {
		if err := s.DeleteKanbanColumn(c.ID); err != nil {
			t.Fatalf("delete: %v", err)
		}
	}
	if err := s.RenameKanbanColumn(after[0].ID, "Someday"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if err := s.BootstrapKanbanColumns(repo.ID); err != nil {
		t.Fatalf("bootstrap on a customised board: %v", err)
	}
	names, positions = columnNames(t, s, repo.ID)
	if strings.Join(names, ",") != "Someday" {
		t.Fatalf("bootstrap clobbered a customised board: %v", names)
	}
	assertDense(t, positions)

	// Bootstrapping a second repo is independent.
	other, err := s.CreateRepo("OTH", "other", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create second repo: %v", err)
	}
	if err := s.BootstrapKanbanColumns(other.ID); err != nil {
		t.Fatalf("bootstrap second repo: %v", err)
	}
	names, _ = columnNames(t, s, other.ID)
	if strings.Join(names, ",") != "Backlog,Doing,Waiting,Done" {
		t.Fatalf("second repo board = %v", names)
	}
}

// TestDeleteKanbanColumnTakesCardsOffBoard is the load-bearing one: a
// lane delete must drop its cards off the board WITHOUT deleting the
// issues. It also pins the finding that the schema's ON DELETE SET NULL
// really is enforced (bacio opens every store with foreign_keys on), by
// deleting a second lane behind the store API's back.
func TestDeleteKanbanColumnTakesCardsOffBoard(t *testing.T) {
	s, repo := kanbanFixture(t)
	doing, err := s.CreateKanbanColumn(repo.ID, "Doing")
	if err != nil {
		t.Fatalf("seed lane: %v", err)
	}
	waiting, err := s.CreateKanbanColumn(repo.ID, "Waiting")
	if err != nil {
		t.Fatalf("seed lane: %v", err)
	}

	var onDoing []*model.Issue
	for _, title := range []string{"one", "two", "three"} {
		iss, err := s.CreateIssue(repo.ID, nil, title, "", model.StateTodo, nil, "", "")
		if err != nil {
			t.Fatalf("create issue: %v", err)
		}
		if err := s.SetIssueKanbanColumn(iss.ID, &doing.ID, 99); err != nil {
			t.Fatalf("put %q on the board: %v", title, err)
		}
		onDoing = append(onDoing, iss)
	}
	parked, err := s.CreateIssue(repo.ID, nil, "parked", "", model.StateTodo, nil, "", "")
	if err != nil {
		t.Fatalf("create issue: %v", err)
	}
	if err := s.SetIssueKanbanColumn(parked.ID, &waiting.ID, 0); err != nil {
		t.Fatalf("put parked on the board: %v", err)
	}

	if err := s.DeleteKanbanColumn(doing.ID); err != nil {
		t.Fatalf("delete lane: %v", err)
	}

	for _, iss := range onDoing {
		got, err := s.GetIssueByID(iss.ID)
		if err != nil {
			t.Fatalf("issue %d vanished with its lane: %v", iss.ID, err)
		}
		if got.KanbanColumnID != nil {
			t.Fatalf("issue %d still on lane %d after the lane was deleted", iss.ID, *got.KanbanColumnID)
		}
		if got.KanbanPosition != 0 {
			t.Fatalf("issue %d kept a stale kanban_position %d after leaving the board", iss.ID, got.KanbanPosition)
		}
	}
	// A card in another lane is untouched.
	if got, err := s.GetIssueByID(parked.ID); err != nil {
		t.Fatalf("get parked: %v", err)
	} else if got.KanbanColumnID == nil || *got.KanbanColumnID != waiting.ID {
		t.Fatalf("deleting one lane disturbed another lane's card")
	}

	// FINDING PIN: the FK cascade is live. Delete the surviving lane
	// with raw SQL — bypassing DeleteKanbanColumn's explicit nulling —
	// and the schema's ON DELETE SET NULL must still take the card off
	// the board rather than delete it.
	if _, err := s.DB.Exec(`DELETE FROM kanban_columns WHERE id = ?`, waiting.ID); err != nil {
		t.Fatalf("raw lane delete: %v", err)
	}
	got, err := s.GetIssueByID(parked.ID)
	if err != nil {
		t.Fatalf("ON DELETE SET NULL deleted the issue instead of clearing the FK: %v", err)
	}
	if got.KanbanColumnID != nil {
		t.Fatalf("ON DELETE SET NULL did not fire — PRAGMA foreign_keys is off on this connection")
	}
}
