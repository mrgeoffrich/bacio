package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// CRUD over the sync_state table. The schema lives in schema.sql
// (added in Phase 1); this file is the Go-side surface area used by
// the import / export pipelines.
//
// The presence of a row in sync_state is the "this record has
// participated in a sync" flag. Absence means "local-only, never
// exported". The case table in docs/git-sync-design.md uses that
// distinction to disambiguate "new local" from "deleted remotely".

// Allowed sync_state.kind values, mirroring the CHECK constraint in
// schema.sql. Exposed here so callers don't have to remember the
// magic strings.
const (
	SyncKindIssue    = "issue"
	SyncKindFeature  = "feature"
	SyncKindDocument = "document"
	SyncKindComment  = "comment"
	SyncKindRepo     = "repo"
)

// MarkSynced upserts a sync_state row with last_synced_at = now and
// the given hash. Used by both export (after writing the record to
// disk) and import (after applying disk content into the DB).
//
// `tx` may be nil — when it is, we use s.DB directly. This lets the
// import pipeline batch all sync_state writes inside one outer
// transaction without requiring every helper to thread a *Tx.
func (s *Store) MarkSynced(uuid, kind, hash string) error {
	return markSyncedExec(s.DB, uuid, kind, hash)
}

// MarkSyncedTx is the transaction-aware variant. The import pipeline
// uses this so the sync_state writes commit atomically with the rest
// of the per-record work.
func (s *Store) MarkSyncedTx(tx *sql.Tx, uuid, kind, hash string) error {
	return markSyncedExec(tx, uuid, kind, hash)
}

// execer abstracts over *sql.DB and *sql.Tx so MarkSynced can serve
// both call sites without duplicating the SQL.
type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

func markSyncedExec(e execer, uuid, kind, hash string) error {
	if uuid == "" || kind == "" {
		return fmt.Errorf("MarkSynced: uuid and kind are required")
	}
	if !validSyncKind(kind) {
		return fmt.Errorf("MarkSynced: invalid kind %q", kind)
	}
	now := time.Now().UTC().Format("2006-01-02 15:04:05.000")
	_, err := e.Exec(`
		INSERT INTO sync_state (uuid, kind, last_synced_at, last_synced_hash)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(uuid) DO UPDATE SET
			kind = excluded.kind,
			last_synced_at = excluded.last_synced_at,
			last_synced_hash = excluded.last_synced_hash`,
		uuid, kind, now, hash,
	)
	return err
}

// GetSyncState returns the sync_state row for a uuid, or ErrNotFound
// if no row exists. Callers use the (presence, absence) split to
// reason about the case table from the design doc.
func (s *Store) GetSyncState(uuid string) (*model.SyncState, error) {
	var st model.SyncState
	err := s.DB.QueryRow(
		`SELECT uuid, kind, last_synced_at, last_synced_hash FROM sync_state WHERE uuid = ?`,
		uuid,
	).Scan(&st.UUID, &st.Kind, &st.LastSyncedAt, &st.LastSyncedHash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan sync_state: %w", err)
	}
	return &st, nil
}

// DeleteSyncState removes the sync_state row for a uuid. Idempotent —
// no-op if the row was never present.
func (s *Store) DeleteSyncState(uuid string) error {
	return deleteSyncStateExec(s.DB, uuid)
}

// DeleteSyncStateTx is the transaction-aware variant.
func (s *Store) DeleteSyncStateTx(tx *sql.Tx, uuid string) error {
	return deleteSyncStateExec(tx, uuid)
}

func deleteSyncStateExec(e execer, uuid string) error {
	_, err := e.Exec(`DELETE FROM sync_state WHERE uuid = ?`, uuid)
	return err
}

// ListSyncedUUIDs returns every uuid in sync_state of a given kind.
// Used by import's deletion-detection phase: iterate the result and
// drop any uuid that wasn't seen in the on-disk scan.
func (s *Store) ListSyncedUUIDs(kind string) ([]string, error) {
	if !validSyncKind(kind) {
		return nil, fmt.Errorf("ListSyncedUUIDs: invalid kind %q", kind)
	}
	rows, err := s.DB.Query(`SELECT uuid FROM sync_state WHERE kind = ?`, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// validSyncKind keeps the "magic strings" allowlist in lockstep with
// schema.sql's CHECK constraint, so a typo at a call site fails the
// Go-side validation rather than the SQLite-side write.
func validSyncKind(kind string) bool {
	switch kind {
	case SyncKindIssue, SyncKindFeature, SyncKindDocument, SyncKindComment, SyncKindRepo:
		return true
	}
	return false
}
