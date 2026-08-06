package store

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/mrgeoffrich/bacio/internal/identity"
	"github.com/mrgeoffrich/bacio/internal/model"
)

// Kanban columns are the per-repo lanes of the human-work board. They are
// a SEPARATE AXIS from issues.state: `state` stays the agent/pipeline
// lifecycle, kanban_column_id is the lane a human dragged the card into.
// A card appears on the Kanban if and only if its kanban_column_id is
// NOT NULL — that single rule is what stops a `todo` card rendering on
// both the Kanban and the Agentic Pipeline's Backlog.
//
// # Position policy: DENSE and 0-based
//
// `kanban_columns.position` is the left-to-right lane order and is kept
// **dense** — the columns of a repo always occupy exactly 0..n-1 with no
// gaps and no duplicates. Every mutator in this file that can perturb the
// order (create / delete / reorder) restores that invariant before it
// commits. Sparse positions were rejected deliberately: a repo has a
// handful of lanes, so a full renumber is a handful of UPDATEs, and dense
// positions mean the stored value IS the array index the UI renders at —
// no normalisation pass on the read side, and no drift to reconcile
// across sync. Reads still sort by `position` (tie-broken by id), so a
// gap introduced by an older binary or a hand-edited DB degrades to a
// stable order rather than a broken board.
//
// The same 0-based convention is used for `issues.kanban_position`
// (the top-to-bottom order within a lane) — see SetIssueKanbanColumn in
// issues.go, which renumbers a lane densely on every move.

// DefaultKanbanColumnNames is the starter board a repo is seeded with by
// BootstrapKanbanColumns: four lanes at positions 0-3. Deliberately
// generic — the set is per-repo and fully renameable, so this is a
// starting point rather than an enum.
var DefaultKanbanColumnNames = []string{"Backlog", "Doing", "Waiting", "Done"}

const kanbanColumnCols = `id, uuid, repo_id, name, position, created_at, updated_at`

// CreateKanbanColumn appends a new lane at the right-hand end of the
// repo's board. The name is validated (and trimmed) through the shared
// store-boundary validator; a name already used by another lane in the
// same repo is rejected with a clear error rather than surfacing the raw
// `UNIQUE constraint failed: kanban_columns.repo_id, kanban_columns.name`
// from the uniq_kanban_columns_name index.
func (s *Store) CreateKanbanColumn(repoID int64, name string) (*model.KanbanColumn, error) {
	clean, err := ValidateKanbanColumnName(name)
	if err != nil {
		return nil, err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if err := assertKanbanNameFreeTx(tx, repoID, clean, 0); err != nil {
		return nil, err
	}
	// MAX(position)+1 rather than COUNT(*): identical under the dense
	// invariant, but collision-free even if an older binary or a
	// hand-edited DB left a gap behind.
	var next int
	if err := tx.QueryRow(
		`SELECT COALESCE(MAX(position), -1) + 1 FROM kanban_columns WHERE repo_id = ?`, repoID,
	).Scan(&next); err != nil {
		return nil, err
	}
	res, err := tx.Exec(
		`INSERT INTO kanban_columns (uuid, repo_id, name, position) VALUES (?, ?, ?, ?)`,
		identity.New(), repoID, clean, next,
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetKanbanColumnByID(id)
}

// CreateKanbanColumnFromSync inserts a lane with a caller-supplied
// uuid, position and timestamps, for the sync import path. Mirrors
// CreateFeatureFromSync / CreateDocFolderFromSync: same name
// validation, no auto-uuid, no auto-position.
//
// The position is written verbatim rather than appended at MAX+1: lane
// order is synced content, and the peer that authored the column.yaml
// already holds the authoritative dense 0..n-1 sequence. Re-deriving it
// here would fight the incoming data.
func (s *Store) CreateKanbanColumnFromSync(repoID int64, uuid, name string, position int, createdAt, updatedAt sql.NullTime) (*model.KanbanColumn, error) {
	if uuid == "" {
		return nil, fmt.Errorf("CreateKanbanColumnFromSync: uuid is required")
	}
	clean, err := ValidateKanbanColumnName(name)
	if err != nil {
		return nil, err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if err := assertKanbanNameFreeTx(tx, repoID, clean, 0); err != nil {
		return nil, err
	}
	q := `INSERT INTO kanban_columns (uuid, repo_id, name, position`
	vals := `?, ?, ?, ?`
	args := []any{uuid, repoID, clean, position}
	if createdAt.Valid {
		q += `, created_at`
		vals += `, ?`
		args = append(args, createdAt.Time)
	}
	if updatedAt.Valid {
		q += `, updated_at`
		vals += `, ?`
		args = append(args, updatedAt.Time)
	}
	q += `) VALUES (` + vals + `)`
	res, err := tx.Exec(q, args...)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetKanbanColumnByID(id)
}

func (s *Store) GetKanbanColumnByID(id int64) (*model.KanbanColumn, error) {
	return scanKanbanColumn(s.DB.QueryRow(`SELECT `+kanbanColumnCols+` FROM kanban_columns WHERE id = ?`, id))
}

// GetKanbanColumnByUUID is the sync-side lookup: import maps the
// canonical uuid in a column record back to a DB row, ignoring rename
// churn. Mirrors GetFeatureByUUID.
func (s *Store) GetKanbanColumnByUUID(uuid string) (*model.KanbanColumn, error) {
	return scanKanbanColumn(s.DB.QueryRow(`SELECT `+kanbanColumnCols+` FROM kanban_columns WHERE uuid = ?`, uuid))
}

// GetKanbanColumnByName resolves a lane by its (repo-unique) name — the
// lookup behind `bacio kanban move BACI-42 --column Doing`, where the
// user names the lane rather than its id. The name is validated the same
// way a write would validate it so a lookup and a create agree on what
// the canonical form of a name is.
func (s *Store) GetKanbanColumnByName(repoID int64, name string) (*model.KanbanColumn, error) {
	clean, err := ValidateKanbanColumnName(name)
	if err != nil {
		return nil, err
	}
	return scanKanbanColumn(s.DB.QueryRow(
		`SELECT `+kanbanColumnCols+` FROM kanban_columns WHERE repo_id = ? AND name = ?`, repoID, clean,
	))
}

// ListKanbanColumns returns the repo's lanes in board order. Ordered by
// `position` with `id` as a stable tie-break, so a duplicate position
// (only reachable via a hand-edited DB) still yields a deterministic
// board rather than a flickering one.
func (s *Store) ListKanbanColumns(repoID int64) ([]*model.KanbanColumn, error) {
	rows, err := s.DB.Query(
		`SELECT `+kanbanColumnCols+` FROM kanban_columns WHERE repo_id = ? ORDER BY position ASC, id ASC`, repoID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.KanbanColumn
	for rows.Next() {
		col, err := scanKanbanColumn(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, col)
	}
	return out, rows.Err()
}

// RenameKanbanColumn renames a lane in place. Validates through the same
// store-boundary validator as create and rejects a collision with
// another lane in the same repo. Renaming a lane to its current name is
// allowed (the exclude-self clause) and still bumps updated_at.
//
// updated_at is bumped because the lane record is synced content (its
// name and membership live in the column record, not in issue.yaml) and
// the import-side conflict rule is last-writer-by-updated_at.
func (s *Store) RenameKanbanColumn(id int64, name string) error {
	clean, err := ValidateKanbanColumnName(name)
	if err != nil {
		return err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var repoID int64
	if err := tx.QueryRow(`SELECT repo_id FROM kanban_columns WHERE id = ?`, id).Scan(&repoID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if err := assertKanbanNameFreeTx(tx, repoID, clean, id); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`UPDATE kanban_columns SET name = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, clean, id,
	); err != nil {
		return err
	}
	return tx.Commit()
}

// ReorderKanbanColumn moves a lane to `position` within its repo's board
// and renumbers the rest back to a dense 0..n-1 sequence.
//
// `position` is 0-BASED — the same space `model.KanbanColumn.Position`
// reports, so reading a column's Position and passing it straight back
// is a no-op. (Note this differs from ReorderIssue, whose `position` is
// 1-based over the Pipeline priority band; that one is left alone.)
// Out-of-range values are clamped to [0, n-1] rather than rejected, so a
// drag past either end of the board lands where the user aimed.
//
// Only rows whose position actually changes have updated_at bumped —
// lane order is synced content, but an untouched lane must not churn the
// last-writer-wins gate for a move that never affected it.
func (s *Store) ReorderKanbanColumn(id int64, position int) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var repoID int64
	if err := tx.QueryRow(`SELECT repo_id FROM kanban_columns WHERE id = ?`, id).Scan(&repoID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	others, err := kanbanColumnIDsTx(tx, repoID, id)
	if err != nil {
		return err
	}
	idx := position
	if idx < 0 {
		idx = 0
	}
	if idx > len(others) {
		idx = len(others)
	}
	ordered := make([]int64, 0, len(others)+1)
	ordered = append(ordered, others[:idx]...)
	ordered = append(ordered, id)
	ordered = append(ordered, others[idx:]...)
	return commitKanbanOrderTx(tx, ordered)
}

// DeleteKanbanColumn removes a lane and takes its cards off the board —
// the issues themselves are never deleted, only their board membership.
//
// The nulling is done explicitly here rather than left to the schema's
// `ON DELETE SET NULL`. The FK *is* live — bacio opens every store with
// `PRAGMA foreign_keys(1)` (store.go Open) / `PRAGMA foreign_keys = ON`
// (OpenMemory), and the cascade was verified to fire on both a fresh
// schema.sql database and one that acquired the column through
// migrate()'s ALTER TABLE. The explicit UPDATE is belt-and-braces so
// this function's contract holds on any connection regardless of pragma
// state, and it is also the only way to reset kanban_position (the FK
// clears the column id but would leave a stale in-lane index behind on a
// card that is no longer in any lane).
//
// Remaining lanes are renumbered back to a dense 0..n-1.
func (s *Store) DeleteKanbanColumn(id int64) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var repoID int64
	if err := tx.QueryRow(`SELECT repo_id FROM kanban_columns WHERE id = ?`, id).Scan(&repoID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	// Take the member cards off the board. Deliberately does NOT bump
	// issues.updated_at: board membership does not serialise into
	// issue.yaml, so churning the sync last-writer-wins gate here would
	// be pure noise (same rationale as SetIssueKanbanColumn).
	if _, err := tx.Exec(
		`UPDATE issues SET kanban_column_id = NULL, kanban_position = 0 WHERE kanban_column_id = ?`, id,
	); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM kanban_columns WHERE id = ?`, id); err != nil {
		return err
	}
	remaining, err := kanbanColumnIDsTx(tx, repoID, id)
	if err != nil {
		return err
	}
	return commitKanbanOrderTx(tx, remaining)
}

// DeleteKanbanColumnByUUID is the sync-side delete: a lane whose
// column.yaml disappeared from the sync repo is gone everywhere.
// Resolves the uuid to a row and defers to DeleteKanbanColumn, so the
// cards come off the board (kanban_column_id NULL, kanban_position
// reset) and the remaining lanes renumber densely — exactly as a local
// delete would. Mirrors DeleteDocFolderByUUID.
//
// Returns ErrNotFound when no lane carries that uuid.
func (s *Store) DeleteKanbanColumnByUUID(uuid string) error {
	if uuid == "" {
		return fmt.Errorf("DeleteKanbanColumnByUUID: uuid is required")
	}
	var id int64
	if err := s.DB.QueryRow(`SELECT id FROM kanban_columns WHERE uuid = ?`, uuid).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	return s.DeleteKanbanColumn(id)
}

// BootstrapKanbanColumns seeds a repo's starter board —
// Backlog / Doing / Waiting / Done at positions 0-3.
//
// Idempotent: a repo that already has ANY kanban column is left exactly
// as it is, including one whose lanes were renamed or deleted down to a
// single column. That is the same "never force defaults onto a repo that
// has its own workflow" rule BootstrapRepoDefaults applies to the
// catch-all features.
//
// NOTE (Phase 2 hand-off): this is deliberately a standalone,
// independently-callable function. It still needs wiring into
// Store.BootstrapRepoDefaults (bootstrap.go) and into the workspace
// creation path so a new workspace opens with a populated board.
func (s *Store) BootstrapKanbanColumns(repoID int64) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM kanban_columns WHERE repo_id = ?`, repoID).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	for i, name := range DefaultKanbanColumnNames {
		if _, err := tx.Exec(
			`INSERT INTO kanban_columns (uuid, repo_id, name, position) VALUES (?, ?, ?, ?)`,
			identity.New(), repoID, name, i,
		); err != nil {
			return s.bootstrapRaceOrErr(repoID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return s.bootstrapRaceOrErr(repoID, err)
	}
	return nil
}

// bootstrapRaceOrErr swallows the error from a losing BootstrapKanbanColumns
// racer. The shared store is written by several processes at once (TUI,
// desktop, `bacio channel` MCP, `bacio web`) and BootstrapRepoDefaults sits
// on the repo-resolve hot path, so two callers can both observe an empty
// board before either commits; the loser then trips
// uniq_kanban_columns_name. Re-reading the count is a cheaper and more
// portable test than sniffing the driver's constraint-error string: if the
// board is populated now, the other racer did our job and there is nothing
// to report.
func (s *Store) bootstrapRaceOrErr(repoID int64, cause error) error {
	var n int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM kanban_columns WHERE repo_id = ?`, repoID).Scan(&n); err == nil && n > 0 {
		return nil
	}
	return cause
}

// assertKanbanNameFreeTx rejects a name already used by another lane in
// the same repo. excludeID (0 for a create) lets a rename keep its own
// name. This is a pre-check for a better error message — the
// uniq_kanban_columns_name index is still the actual guarantee.
func assertKanbanNameFreeTx(tx *sql.Tx, repoID int64, name string, excludeID int64) error {
	var collide int64
	err := tx.QueryRow(
		`SELECT id FROM kanban_columns WHERE repo_id = ? AND name = ? AND id <> ?`, repoID, name, excludeID,
	).Scan(&collide)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("kanban column %q already exists in this repo", name)
}

// kanbanColumnIDsTx returns the repo's column ids in current board order,
// omitting excludeID (pass 0 to omit nothing).
func kanbanColumnIDsTx(tx *sql.Tx, repoID, excludeID int64) ([]int64, error) {
	rows, err := tx.Query(
		`SELECT id FROM kanban_columns WHERE repo_id = ? ORDER BY position ASC, id ASC`, repoID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		if id != excludeID {
			out = append(out, id)
		}
	}
	return out, rows.Err()
}

// commitKanbanOrderTx writes `ordered` back as dense positions 0..n-1 and
// commits. The `position <> ?` guard means an unmoved lane is not touched
// at all, so its updated_at (the sync last-writer-wins key) stays put.
func commitKanbanOrderTx(tx *sql.Tx, ordered []int64) error {
	for i, id := range ordered {
		if _, err := tx.Exec(
			`UPDATE kanban_columns SET position = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ? AND position <> ?`,
			i, id, i,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func scanKanbanColumn(row rowScanner) (*model.KanbanColumn, error) {
	var c model.KanbanColumn
	err := row.Scan(&c.ID, &c.UUID, &c.RepoID, &c.Name, &c.Position, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan kanban column: %w", err)
	}
	return &c, nil
}
