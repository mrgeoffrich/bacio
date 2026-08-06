package sync

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// Import of the two CONTAINER record kinds introduced by the
// workspaces + Kanban pivot: doc folders (repos/<P>/folders/<uuid>/) and
// Kanban lanes (repos/<P>/kanban/<uuid>/).
//
// # Why containers, and why membership lives here
//
// Placement — "document D is in folder F at index 3", "issue I is in
// lane L at index 0" — is written on the CONTAINER, never on the
// member. That is not a stylistic choice: doc.yaml and issue.yaml are
// parsed by every bacio ever shipped with KnownFields(true), so a
// `folder_uuid:` key on either would hard-fail an older binary's entire
// `bacio sync` run and be silently stripped on its next export. Keeping
// membership on the container leaves doc.yaml and issue.yaml
// byte-identical to what an older binary writes, which is what makes
// the whole pivot two-way compatible. See the A0 rule in paths.go.
//
// The consequence for this file is that the ordered `documents:` /
// `issues:` sequence in the container manifest IS the tree/board order,
// and applying it is a separate pass from applying the container rows.
//
// # Pass order
//
//	1. applyDocFolders      — folder rows, parent-before-child
//	2. applyKanbanColumns   — lane rows
//	3. applyDocFolderMembership  — documents.folder_id / folder_position
//	4. applyKanbanMembership     — issues.kanban_column_id / kanban_position
//
// 3 and 4 run after applyDocuments / applyIssues so every member row
// exists. They are safe to run after those passes because the member
// UPDATE statements there list their columns explicitly and never
// mention folder_id / kanban_column_id — the two passes cannot fight.
//
// # Conflict rules
//
// Both container kinds use the same last-writer-wins gate as every
// other record (remote `updated_at` older than the local row ⇒ keep
// local, surface a skip). Two conflicts are specific to containers:
//
//   - Name collision. The record FOLDER is named by uuid, not by the
//     human name, so unlike features/issues/documents git cannot stop
//     two containers from claiming the same name. Resolved
//     deterministically — see resolveContainerNames.
//   - Membership collision. A bad three-way merge can list one document
//     uuid in two folder.yaml files. Resolved by last-writer-by-
//     container-`updated_at`, tie-broken by container uuid ascending —
//     see pickMembershipWinner.

// ---------------------------------------------------------------------
// Doc folders
// ---------------------------------------------------------------------

// applyDocFolders inserts/updates one doc_folders row per scanned
// folder.yaml, parents before children so a `parent_uuid` always
// resolves to a row that already exists.
//
// Returns uuid → local row id for every folder this run is allowed to
// speak for: the ones it inserted or updated, excluding any the
// last-writer-wins gate skipped. applyDocFolderMembership consumes that
// map — a folder the local side won keeps its LOCAL membership, and a
// folder that exists only locally never appears at all, which is what
// stops a first import from emptying folders that were never exported.
func (e *Engine) applyDocFolders(tx *sql.Tx, sr *scannedRepo, repo *model.Repo, res *ImportResult) (map[string]int64, error) {
	applied := map[string]int64{}
	if len(sr.DocFolders) == 0 {
		return applied, nil
	}
	now := time.Now().UTC()

	// Parent-before-child. Depth is computed inside the scanned set: a
	// folder whose parent_uuid isn't in the set is depth 0 (it will be
	// re-rooted below if the parent doesn't resolve in the DB either).
	// depthOfScannedFolders is cycle-safe, so a hand-edited manifest
	// that points a folder at its own descendant still terminates.
	order := sortedFolderUUIDsByDepth(sr.DocFolders)

	// Name collisions between two INCOMING folders sharing a parent are
	// resolved up front and identically on every machine, so the peers
	// converge instead of taking turns renaming each other.
	desiredName := resolveContainerNames(sr.DocFolders)

	for _, uuid := range order {
		sf := sr.DocFolders[uuid]
		hash := contentHashDocFolder(sf)
		name := desiredName[uuid]

		parentID, err := e.resolveFolderParentTx(tx, repo, sf, res)
		if err != nil {
			return nil, err
		}

		var (
			existingID        int64
			existingName      string
			existingParent    sql.NullInt64
			existingPosition  int
			existingUpdatedAt time.Time
		)
		err = tx.QueryRow(
			`SELECT id, name, parent_id, position, updated_at FROM doc_folders WHERE uuid = ?`, uuid,
		).Scan(&existingID, &existingName, &existingParent, &existingPosition, &existingUpdatedAt)
		if errors.Is(err, sql.ErrNoRows) {
			name, err = e.freeDocFolderNameTx(tx, sr, repo.ID, parentID, name, uuid, res, now)
			if err != nil {
				return nil, err
			}
			ins, err := tx.Exec(
				`INSERT INTO doc_folders (uuid, repo_id, parent_id, name, position, created_at, updated_at)
				 VALUES (?, ?, ?, ?, ?, ?, ?)`,
				uuid, repo.ID, nullableInt64(parentID), name, sf.Parsed.Position,
				sqliteTimestamp(sf.Parsed.CreatedAt), sqliteTimestamp(sf.Parsed.UpdatedAt),
			)
			if err != nil {
				return nil, fmt.Errorf("insert doc folder %s: %w", name, err)
			}
			id, _ := ins.LastInsertId()
			applied[uuid] = id
			res.Inserted++
			if err := e.markSyncedTx(tx, uuid, store.SyncKindDocFolder, hash, now); err != nil {
				return nil, err
			}
			continue
		}
		if err != nil {
			return nil, err
		}
		// Last-writer-wins: the remote manifest is older than the local
		// row, so keep local (name, parent, position AND membership —
		// applyDocFolderMembership skips this folder too) and let this
		// run's export write the newer local content back out.
		if sf.Parsed.UpdatedAt.Before(existingUpdatedAt) {
			res.Skipped++
			res.SkippedStale = append(res.SkippedStale, SkippedStaleEntry{
				Kind:          store.SyncKindDocFolder,
				UUID:          uuid,
				Label:         existingName,
				LocalUpdated:  existingUpdatedAt.UTC().Format(time.RFC3339),
				RemoteUpdated: sf.Parsed.UpdatedAt.UTC().Format(time.RFC3339),
			})
			continue
		}
		name, err = e.freeDocFolderNameTx(tx, sr, repo.ID, parentID, name, uuid, res, now)
		if err != nil {
			return nil, err
		}
		applied[uuid] = existingID
		if existingName != name || !nullableEqualInt64(existingParent, parentID) || existingPosition != sf.Parsed.Position {
			if _, err := tx.Exec(
				`UPDATE doc_folders SET name = ?, parent_id = ?, position = ?, updated_at = ? WHERE id = ?`,
				name, nullableInt64(parentID), sf.Parsed.Position,
				sqliteTimestamp(sf.Parsed.UpdatedAt), existingID,
			); err != nil {
				return nil, fmt.Errorf("update doc folder %s: %w", name, err)
			}
			res.Updated++
		} else {
			res.NoOp++
		}
		if err := e.markSyncedTx(tx, uuid, store.SyncKindDocFolder, hash, now); err != nil {
			return nil, err
		}
	}
	return applied, nil
}

// resolveFolderParentTx maps a manifest's parent_uuid to a local
// doc_folders.id. An empty parent_uuid is a root folder. A parent that
// doesn't resolve (deleted on this machine, or a manifest that arrived
// before its parent) re-roots the folder and records a dangling ref
// rather than failing the import — the same stance the feature / doc
// link resolution takes.
func (e *Engine) resolveFolderParentTx(tx *sql.Tx, repo *model.Repo, sf *scannedDocFolder, res *ImportResult) (*int64, error) {
	if sf.Parsed.ParentUUID == "" {
		return nil, nil
	}
	var id int64
	err := tx.QueryRow(
		`SELECT id FROM doc_folders WHERE uuid = ? AND repo_id = ?`, sf.Parsed.ParentUUID, repo.ID,
	).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		res.Dangling = append(res.Dangling, DanglingRef{
			From:        sf.Parsed.Name,
			FromUUID:    sf.Parsed.UUID,
			Kind:        "doc_folder_parent",
			TargetLabel: "",
			TargetUUID:  sf.Parsed.ParentUUID,
		})
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &id, nil
}

// freeDocFolderNameTx makes `name` available at (repo, parent) for the
// record identified by `uuid`, and returns the name to actually write.
//
// uniq_doc_folders_child / uniq_doc_folders_root make (repo, parent,
// name) unique, so a bare INSERT would abort the whole import. The rule
// mirrors resolveCollisions' "already-in-git wins":
//
//   - A LOCAL-ONLY folder holding the name gives it up and takes the
//     first free "<name> (n)". Its updated_at is bumped so the rename
//     is what propagates on this run's export, rather than the peer
//     re-asserting the old name next tick.
//   - A holder that is itself an INCOMING record keeps its name (it was
//     already deduped by resolveContainerNames); the folder being
//     applied takes the suffix instead. Only reachable when two
//     manifests declare different parents that both fail to resolve and
//     collapse to the root.
func (e *Engine) freeDocFolderNameTx(
	tx *sql.Tx, sr *scannedRepo, repoID int64, parentID *int64, name, uuid string,
	res *ImportResult, now time.Time,
) (string, error) {
	for n := 0; n < containerNameProbeLimit; n++ {
		candidate := containerNameCandidate(name, n)
		holderID, holderUUID, err := e.docFolderNameHolderTx(tx, repoID, parentID, candidate, uuid)
		if err != nil {
			return "", err
		}
		if holderID == 0 {
			return candidate, nil // free
		}
		if _, incoming := sr.DocFolders[holderUUID]; incoming {
			continue // the holder keeps it; try the next candidate
		}
		// Local-only holder yields the name.
		freed, err := e.nextFreeDocFolderNameTx(tx, repoID, parentID, name, holderUUID)
		if err != nil {
			return "", err
		}
		if _, err := tx.Exec(
			`UPDATE doc_folders SET name = ?, updated_at = ? WHERE id = ?`,
			freed, sqliteTimestamp(now), holderID,
		); err != nil {
			return "", fmt.Errorf("free doc folder name %q: %w", candidate, err)
		}
		res.Renamed = append(res.Renamed, RenameEntry{
			Kind:   store.SyncKindDocFolder,
			Prefix: sr.Prefix,
			UUID:   holderUUID,
			Old:    candidate,
			New:    freed,
		})
		return candidate, nil
	}
	return name, nil
}

// nextFreeDocFolderNameTx returns the first "<base> (n)" (n >= 2) not
// taken at (repo, parent), ignoring the row identified by excludeUUID.
func (e *Engine) nextFreeDocFolderNameTx(tx *sql.Tx, repoID int64, parentID *int64, base, excludeUUID string) (string, error) {
	for n := 1; n < containerNameProbeLimit; n++ {
		candidate := containerNameCandidate(base, n)
		holderID, _, err := e.docFolderNameHolderTx(tx, repoID, parentID, candidate, excludeUUID)
		if err != nil {
			return "", err
		}
		if holderID == 0 {
			return candidate, nil
		}
	}
	return base, nil
}

// docFolderNameHolderTx returns the (id, uuid) of the row holding
// `name` at (repo, parent), excluding `excludeUUID`. Returns (0, "",
// nil) when the slot is free. parent_id IS NULL needs its own SQL form
// — `= NULL` never matches in SQLite.
func (e *Engine) docFolderNameHolderTx(tx *sql.Tx, repoID int64, parentID *int64, name, excludeUUID string) (int64, string, error) {
	q := `SELECT id, uuid FROM doc_folders WHERE repo_id = ? AND name = ? AND uuid != ? AND parent_id `
	args := []any{repoID, name, excludeUUID}
	if parentID == nil {
		q += `IS NULL`
	} else {
		q += `= ?`
		args = append(args, *parentID)
	}
	var (
		id   int64
		uuid string
	)
	err := tx.QueryRow(q, args...).Scan(&id, &uuid)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", nil
	}
	if err != nil {
		return 0, "", err
	}
	return id, uuid, nil
}

// applyDocFolderMembership writes documents.folder_id /
// folder_position from the ordered `documents:` sequence of every
// folder manifest that this run treated as authoritative.
//
// "Authoritative" means: seen on disk AND not skipped by the
// last-writer-wins gate. A folder whose local row won the LWW gate
// keeps its LOCAL membership, and a folder that exists only locally
// (never exported) is untouched — otherwise the first import would
// empty every not-yet-shared folder.
func (e *Engine) applyDocFolderMembership(tx *sql.Tx, sr *scannedRepo, repo *model.Repo, authoritative map[string]int64, res *ImportResult) error {
	if len(authoritative) == 0 {
		return nil
	}

	claims := map[string]membershipClaim{}
	for _, uuid := range sortedKeys(sr.DocFolders) {
		folderID, ok := authoritative[uuid]
		if !ok {
			continue
		}
		sf := sr.DocFolders[uuid]
		for i, docUUID := range dedupeOrdered(sf.Parsed.Documents) {
			claimMembership(claims, docUUID, membershipClaim{
				containerUUID: uuid,
				containerID:   folderID,
				updatedAt:     sf.Parsed.UpdatedAt,
				position:      i,
			})
		}
	}

	rows, err := tx.Query(`SELECT id, uuid, folder_id, folder_position FROM documents WHERE repo_id = ?`, repo.ID)
	if err != nil {
		return err
	}
	type docRow struct {
		id       int64
		uuid     string
		folderID sql.NullInt64
		position int
	}
	var docs []docRow
	for rows.Next() {
		var d docRow
		if err := rows.Scan(&d.id, &d.uuid, &d.folderID, &d.position); err != nil {
			rows.Close()
			return err
		}
		docs = append(docs, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	seenDocs := make(map[string]bool, len(docs))
	for _, d := range docs {
		seenDocs[d.uuid] = true
		// documents.updated_at is deliberately never bumped here: folder
		// placement doesn't serialise into doc.yaml, so churning the LWW
		// gate for a move would be pure noise (the same rule
		// store.SetDocumentFolder follows).
		if claim, ok := claims[d.uuid]; ok {
			if d.folderID.Valid && d.folderID.Int64 == claim.containerID && d.position == claim.position {
				continue
			}
			if _, err := tx.Exec(
				`UPDATE documents SET folder_id = ?, folder_position = ? WHERE id = ?`,
				claim.containerID, claim.position, d.id,
			); err != nil {
				return fmt.Errorf("place document %s: %w", d.uuid, err)
			}
			continue
		}
		if d.folderID.Valid && containsID(authoritative, d.folderID.Int64) {
			if _, err := tx.Exec(
				`UPDATE documents SET folder_id = NULL, folder_position = 0 WHERE id = ?`, d.id,
			); err != nil {
				return fmt.Errorf("unplace document %s: %w", d.uuid, err)
			}
		}
	}
	// A manifest listing a document this machine has never seen is a
	// dangling reference, not a failure — the doc may arrive on a later
	// pull, or may have been deleted here.
	for _, docUUID := range sortedClaimKeys(claims) {
		if seenDocs[docUUID] {
			continue
		}
		res.Dangling = append(res.Dangling, DanglingRef{
			From:       claims[docUUID].containerUUID,
			FromUUID:   claims[docUUID].containerUUID,
			Kind:       "doc_folder_member",
			TargetUUID: docUUID,
		})
	}
	return nil
}

// ---------------------------------------------------------------------
// Kanban columns
// ---------------------------------------------------------------------

// applyKanbanColumns inserts/updates one kanban_columns row per scanned
// column.yaml. Lanes are flat (no parent), so the ordering concern that
// applyDocFolders has doesn't apply — uuid order is enough.
// Returns uuid → local row id for the lanes this run may speak for,
// on the same terms as applyDocFolders.
func (e *Engine) applyKanbanColumns(tx *sql.Tx, sr *scannedRepo, repo *model.Repo, res *ImportResult) (map[string]int64, error) {
	applied := map[string]int64{}
	if len(sr.KanbanColumns) == 0 {
		return applied, nil
	}
	now := time.Now().UTC()
	desiredName := resolveContainerNames(sr.KanbanColumns)

	for _, uuid := range sortedKeys(sr.KanbanColumns) {
		sc := sr.KanbanColumns[uuid]
		hash := contentHashKanbanColumn(sc)
		name := desiredName[uuid]

		var (
			existingID        int64
			existingName      string
			existingPosition  int
			existingUpdatedAt time.Time
		)
		err := tx.QueryRow(
			`SELECT id, name, position, updated_at FROM kanban_columns WHERE uuid = ?`, uuid,
		).Scan(&existingID, &existingName, &existingPosition, &existingUpdatedAt)
		if errors.Is(err, sql.ErrNoRows) {
			name, err = e.freeKanbanColumnNameTx(tx, sr, repo.ID, name, uuid, res, now)
			if err != nil {
				return nil, err
			}
			ins, err := tx.Exec(
				`INSERT INTO kanban_columns (uuid, repo_id, name, position, created_at, updated_at)
				 VALUES (?, ?, ?, ?, ?, ?)`,
				uuid, repo.ID, name, sc.Parsed.Position,
				sqliteTimestamp(sc.Parsed.CreatedAt), sqliteTimestamp(sc.Parsed.UpdatedAt),
			)
			if err != nil {
				return nil, fmt.Errorf("insert kanban column %s: %w", name, err)
			}
			id, _ := ins.LastInsertId()
			applied[uuid] = id
			res.Inserted++
			if err := e.markSyncedTx(tx, uuid, store.SyncKindKanbanColumn, hash, now); err != nil {
				return nil, err
			}
			continue
		}
		if err != nil {
			return nil, err
		}
		if sc.Parsed.UpdatedAt.Before(existingUpdatedAt) {
			res.Skipped++
			res.SkippedStale = append(res.SkippedStale, SkippedStaleEntry{
				Kind:          store.SyncKindKanbanColumn,
				UUID:          uuid,
				Label:         existingName,
				LocalUpdated:  existingUpdatedAt.UTC().Format(time.RFC3339),
				RemoteUpdated: sc.Parsed.UpdatedAt.UTC().Format(time.RFC3339),
			})
			continue
		}
		name, err = e.freeKanbanColumnNameTx(tx, sr, repo.ID, name, uuid, res, now)
		if err != nil {
			return nil, err
		}
		applied[uuid] = existingID
		if existingName != name || existingPosition != sc.Parsed.Position {
			if _, err := tx.Exec(
				`UPDATE kanban_columns SET name = ?, position = ?, updated_at = ? WHERE id = ?`,
				name, sc.Parsed.Position, sqliteTimestamp(sc.Parsed.UpdatedAt), existingID,
			); err != nil {
				return nil, fmt.Errorf("update kanban column %s: %w", name, err)
			}
			res.Updated++
		} else {
			res.NoOp++
		}
		if err := e.markSyncedTx(tx, uuid, store.SyncKindKanbanColumn, hash, now); err != nil {
			return nil, err
		}
	}
	return applied, nil
}

// freeKanbanColumnNameTx is freeDocFolderNameTx for lanes:
// uniq_kanban_columns_name makes (repo, name) unique, and the same
// "already-in-git wins, local-only holder yields" rule applies.
//
// This one bites in practice, not just in theory:
// BootstrapKanbanColumns seeds every repo with the same four lane names
// (Backlog / Doing / Waiting / Done) with machine-local uuids, so the
// first sync between two machines that both bootstrapped the same repo
// arrives with four guaranteed name collisions.
func (e *Engine) freeKanbanColumnNameTx(
	tx *sql.Tx, sr *scannedRepo, repoID int64, name, uuid string,
	res *ImportResult, now time.Time,
) (string, error) {
	for n := 0; n < containerNameProbeLimit; n++ {
		candidate := containerNameCandidate(name, n)
		holderID, holderUUID, err := e.kanbanNameHolderTx(tx, repoID, candidate, uuid)
		if err != nil {
			return "", err
		}
		if holderID == 0 {
			return candidate, nil
		}
		if _, incoming := sr.KanbanColumns[holderUUID]; incoming {
			continue
		}
		freed, err := e.nextFreeKanbanColumnNameTx(tx, repoID, name, holderUUID)
		if err != nil {
			return "", err
		}
		if _, err := tx.Exec(
			`UPDATE kanban_columns SET name = ?, updated_at = ? WHERE id = ?`,
			freed, sqliteTimestamp(now), holderID,
		); err != nil {
			return "", fmt.Errorf("free kanban column name %q: %w", candidate, err)
		}
		res.Renamed = append(res.Renamed, RenameEntry{
			Kind:   store.SyncKindKanbanColumn,
			Prefix: sr.Prefix,
			UUID:   holderUUID,
			Old:    candidate,
			New:    freed,
		})
		return candidate, nil
	}
	return name, nil
}

func (e *Engine) nextFreeKanbanColumnNameTx(tx *sql.Tx, repoID int64, base, excludeUUID string) (string, error) {
	for n := 1; n < containerNameProbeLimit; n++ {
		candidate := containerNameCandidate(base, n)
		holderID, _, err := e.kanbanNameHolderTx(tx, repoID, candidate, excludeUUID)
		if err != nil {
			return "", err
		}
		if holderID == 0 {
			return candidate, nil
		}
	}
	return base, nil
}

func (e *Engine) kanbanNameHolderTx(tx *sql.Tx, repoID int64, name, excludeUUID string) (int64, string, error) {
	var (
		id   int64
		uuid string
	)
	err := tx.QueryRow(
		`SELECT id, uuid FROM kanban_columns WHERE repo_id = ? AND name = ? AND uuid != ?`,
		repoID, name, excludeUUID,
	).Scan(&id, &uuid)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", nil
	}
	if err != nil {
		return 0, "", err
	}
	return id, uuid, nil
}

// applyKanbanMembership is applyDocFolderMembership for lanes: it
// writes issues.kanban_column_id / kanban_position from the ordered
// `issues:` sequence of every authoritative column manifest.
//
// A card that no authoritative lane claims, but which currently sits in
// one, comes OFF the board (kanban_column_id NULL) — that NULL is the
// whole "is this card on the Kanban?" rule.
func (e *Engine) applyKanbanMembership(tx *sql.Tx, sr *scannedRepo, repo *model.Repo, authoritative map[string]int64, res *ImportResult) error {
	if len(authoritative) == 0 {
		return nil
	}

	claims := map[string]membershipClaim{}
	for _, uuid := range sortedKeys(sr.KanbanColumns) {
		columnID, ok := authoritative[uuid]
		if !ok {
			continue
		}
		sc := sr.KanbanColumns[uuid]
		for i, issueUUID := range dedupeOrdered(sc.Parsed.Issues) {
			claimMembership(claims, issueUUID, membershipClaim{
				containerUUID: uuid,
				containerID:   columnID,
				updatedAt:     sc.Parsed.UpdatedAt,
				position:      i,
			})
		}
	}

	rows, err := tx.Query(`SELECT id, uuid, kanban_column_id, kanban_position FROM issues WHERE repo_id = ?`, repo.ID)
	if err != nil {
		return err
	}
	type issueRow struct {
		id       int64
		uuid     string
		columnID sql.NullInt64
		position int
	}
	var issues []issueRow
	for rows.Next() {
		var i issueRow
		if err := rows.Scan(&i.id, &i.uuid, &i.columnID, &i.position); err != nil {
			rows.Close()
			return err
		}
		issues = append(issues, i)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	seenIssues := make(map[string]bool, len(issues))
	for _, iss := range issues {
		seenIssues[iss.uuid] = true
		// issues.updated_at is deliberately never bumped — same rule as
		// store.SetIssueKanbanColumn, and pinned there by
		// TestSetIssueKanbanColumnDoesNotChurnIssueUpdatedAt.
		if claim, ok := claims[iss.uuid]; ok {
			if iss.columnID.Valid && iss.columnID.Int64 == claim.containerID && iss.position == claim.position {
				continue
			}
			if _, err := tx.Exec(
				`UPDATE issues SET kanban_column_id = ?, kanban_position = ? WHERE id = ?`,
				claim.containerID, claim.position, iss.id,
			); err != nil {
				return fmt.Errorf("place issue %s: %w", iss.uuid, err)
			}
			continue
		}
		if iss.columnID.Valid && containsID(authoritative, iss.columnID.Int64) {
			if _, err := tx.Exec(
				`UPDATE issues SET kanban_column_id = NULL, kanban_position = 0 WHERE id = ?`, iss.id,
			); err != nil {
				return fmt.Errorf("unplace issue %s: %w", iss.uuid, err)
			}
		}
	}
	for _, issueUUID := range sortedClaimKeys(claims) {
		if seenIssues[issueUUID] {
			continue
		}
		res.Dangling = append(res.Dangling, DanglingRef{
			From:       claims[issueUUID].containerUUID,
			FromUUID:   claims[issueUUID].containerUUID,
			Kind:       "kanban_member",
			TargetUUID: issueUUID,
		})
	}
	return nil
}

// ---------------------------------------------------------------------
// Shared container helpers
// ---------------------------------------------------------------------

// containerRecord is the little bit of shape applyDocFolders and
// applyKanbanColumns share: enough to sort deterministically and to
// resolve a name collision the same way on every machine.
type containerRecord interface {
	containerUUID() string
	containerName() string
	containerParentUUID() string
	containerCreatedAt() time.Time
}

func (s *scannedDocFolder) containerUUID() string         { return s.Parsed.UUID }
func (s *scannedDocFolder) containerName() string         { return s.Parsed.Name }
func (s *scannedDocFolder) containerParentUUID() string   { return s.Parsed.ParentUUID }
func (s *scannedDocFolder) containerCreatedAt() time.Time { return s.Parsed.CreatedAt }

func (s *scannedKanbanColumn) containerUUID() string       { return s.Parsed.UUID }
func (s *scannedKanbanColumn) containerName() string       { return s.Parsed.Name }
func (s *scannedKanbanColumn) containerParentUUID() string { return "" }
func (s *scannedKanbanColumn) containerCreatedAt() time.Time {
	return s.Parsed.CreatedAt
}

// membershipClaim is one container's claim on one member, used to
// resolve the "same uuid listed in two manifests" merge artefact.
type membershipClaim struct {
	containerUUID string
	containerID   int64
	updatedAt     time.Time
	position      int
}

// claimMembership records `next` as the winner for `member` unless an
// existing claim beats it.
//
// THE DEDUPE RULE, stated once: **last writer by container
// `updated_at`, tie-broken by container uuid ascending**. Sort the
// competing claims ascending by (updated_at, uuid) and the last one
// wins — i.e. the newest manifest, and on an exact timestamp tie the
// highest uuid. Both inputs come straight off disk, so every machine
// that sees the same merge result picks the same winner and the peers
// converge on the next export instead of ping-ponging the document
// between two folders.
func claimMembership(claims map[string]membershipClaim, member string, next membershipClaim) {
	prev, ok := claims[member]
	if !ok || claimBeats(next, prev) {
		claims[member] = next
	}
}

func claimBeats(a, b membershipClaim) bool {
	if !a.updatedAt.Equal(b.updatedAt) {
		return a.updatedAt.After(b.updatedAt)
	}
	return a.containerUUID > b.containerUUID
}

func containsID(m map[string]int64, id int64) bool {
	for _, v := range m {
		if v == id {
			return true
		}
	}
	return false
}

// sortedFolderUUIDsByDepth orders scanned folders parent-before-child.
// Depth is measured inside the scanned set; a parent_uuid that isn't in
// the set counts as depth 0 (the folder will be re-rooted if the parent
// doesn't resolve in the DB either). The walk is capped at the set size
// so a hand-edited cycle terminates instead of hanging the import.
func sortedFolderUUIDsByDepth(folders map[string]*scannedDocFolder) []string {
	uuids := sortedKeys(folders)
	depth := make(map[string]int, len(folders))
	for _, uuid := range uuids {
		d := 0
		seen := map[string]bool{uuid: true}
		cur := folders[uuid].Parsed.ParentUUID
		for cur != "" && d <= len(folders) {
			parent, ok := folders[cur]
			if !ok || seen[cur] {
				break
			}
			seen[cur] = true
			d++
			cur = parent.Parsed.ParentUUID
		}
		depth[uuid] = d
	}
	sort.SliceStable(uuids, func(i, j int) bool {
		if depth[uuids[i]] != depth[uuids[j]] {
			return depth[uuids[i]] < depth[uuids[j]]
		}
		return uuids[i] < uuids[j]
	})
	return uuids
}

// resolveContainerNames assigns each scanned container the name it
// should actually be written under, deduplicating collisions between
// two INCOMING records that share a parent.
//
// The record folder on disk is named by uuid, so — unlike features,
// issues and documents, whose folder name IS their label — git cannot
// prevent two containers from claiming the same name. Ordering the
// contenders by (created_at ASC, uuid ASC) makes the winner a pure
// function of the files, so every machine independently reaches the
// same answer and no rename ping-pong develops. The losers get
// "<name> (2)", "<name> (3)", … in that same order.
func resolveContainerNames[T containerRecord](scanned map[string]T) map[string]string {
	type key struct{ parent, name string }
	groups := map[key][]T{}
	for _, uuid := range sortedKeys(scanned) {
		rec := scanned[uuid]
		k := key{parent: rec.containerParentUUID(), name: rec.containerName()}
		groups[k] = append(groups[k], rec)
	}
	out := make(map[string]string, len(scanned))
	for k, members := range groups {
		if len(members) == 1 {
			out[members[0].containerUUID()] = k.name
			continue
		}
		sort.SliceStable(members, func(i, j int) bool {
			ci, cj := members[i].containerCreatedAt(), members[j].containerCreatedAt()
			if !ci.Equal(cj) {
				return ci.Before(cj)
			}
			return members[i].containerUUID() < members[j].containerUUID()
		})
		for i, m := range members {
			if i == 0 {
				out[m.containerUUID()] = k.name
				continue
			}
			out[m.containerUUID()] = fmt.Sprintf("%s (%d)", k.name, i+1)
		}
	}
	return out
}

// containerNameCandidate returns the n'th candidate name for `base`:
// n == 0 is the bare name, n >= 1 is "<base> (n+1)" — so the first
// fallback reads "Design (2)", matching what a user would type.
func containerNameCandidate(base string, n int) string {
	if n == 0 {
		return base
	}
	return fmt.Sprintf("%s (%d)", base, n+1)
}

// containerNameProbeLimit bounds the suffix search. A repo would need
// hundreds of same-named siblings to reach it; the cap exists so a
// pathological (or hand-forged) sync repo can't spin the importer
// forever. On exhaustion the caller keeps the colliding name and lets
// the UNIQUE index surface the problem as a loud import error.
const containerNameProbeLimit = 1000

// dedupeOrdered drops repeat entries from a membership sequence,
// keeping the first occurrence (and therefore its index as the
// position). A manifest listing the same uuid twice is a merge
// artefact; silently collapsing it is better than writing two
// conflicting positions for one member.
func dedupeOrdered(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	out := mapKeys(m)
	sort.Strings(out)
	return out
}

func sortedClaimKeys(m map[string]membershipClaim) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------------
// Content hashes
// ---------------------------------------------------------------------

// contentHashDocFolder folds the folder's own fields AND its membership
// sequence into the sync_state hash, so a pure reorder of the documents
// inside a folder still shows up as an `updated` rather than a silent
// no-op on the next pull.
func contentHashDocFolder(sf *scannedDocFolder) string {
	return ContentHash([]byte(fmt.Sprintf("doc_folder|%s|%s|%s|%d|%s",
		sf.Parsed.UUID, sf.Parsed.Name, sf.Parsed.ParentUUID, sf.Parsed.Position,
		strings.Join(sf.Parsed.Documents, ","))))
}

// contentHashKanbanColumn mirrors contentHashDocFolder for lanes.
func contentHashKanbanColumn(sc *scannedKanbanColumn) string {
	return ContentHash([]byte(fmt.Sprintf("kanban_column|%s|%s|%d|%s",
		sc.Parsed.UUID, sc.Parsed.Name, sc.Parsed.Position,
		strings.Join(sc.Parsed.Issues, ","))))
}
