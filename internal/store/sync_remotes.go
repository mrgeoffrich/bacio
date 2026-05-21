package store

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// CRUD on the sync_remotes table — the per-DB record of where each
// canonical remote URL has been cloned locally. Used by the steady-
// state `bacio sync` to find the local working tree for the remote
// declared in .bacio/config.yaml; populated by `bacio sync init` and
// `bacio sync clone`.
//
// remote_url is the primary key. Multiple project repos can share
// one sync remote (and they'll all read the same row out of this
// table on this machine).

// UpsertSyncRemote records that this machine has the given remote
// cloned at localPath. Idempotent — if a row already exists, the
// local_path is updated and cloned_at is left unchanged. Used both
// by the bootstrap path (first clone) and by a hypothetical "re-
// clone" flow.
//
// Empty remoteURL or localPath is rejected — these are the
// invariants every caller relies on.
func (s *Store) UpsertSyncRemote(remoteURL, localPath string) error {
	if remoteURL == "" || localPath == "" {
		return fmt.Errorf("UpsertSyncRemote: remoteURL and localPath are required")
	}
	_, err := s.DB.Exec(`
		INSERT INTO sync_remotes (remote_url, local_path)
		VALUES (?, ?)
		ON CONFLICT(remote_url) DO UPDATE SET local_path = excluded.local_path`,
		remoteURL, localPath,
	)
	return err
}

// GetSyncRemote returns the row for remoteURL, or ErrNotFound if no
// such row exists. Used by `bacio sync` to resolve the local clone
// path for the remote declared in the project's .bacio/config.yaml.
func (s *Store) GetSyncRemote(remoteURL string) (*model.SyncRemote, error) {
	var (
		r          model.SyncRemote
		lastSyncAt sql.NullTime
		lastErr    sql.NullString
	)
	err := s.DB.QueryRow(
		`SELECT remote_url, local_path, cloned_at, last_sync_at, last_sync_error FROM sync_remotes WHERE remote_url = ?`,
		remoteURL,
	).Scan(&r.RemoteURL, &r.LocalPath, &r.ClonedAt, &lastSyncAt, &lastErr)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan sync_remote: %w", err)
	}
	if lastSyncAt.Valid {
		t := lastSyncAt.Time
		r.LastSyncAt = &t
	}
	if lastErr.Valid && lastErr.String != "" {
		msg := lastErr.String
		r.LastSyncError = &msg
	}
	return &r, nil
}

// MarkSyncCompleted bumps last_sync_at on the row for remoteURL to
// the current time and clears last_sync_error (BACI-89). Called once
// per successful `bacio sync` at the very end of the pipeline, after
// the push (or after the local commit, when push is skipped). No-op
// (silent) if the row is missing — better to drop a stat update than
// fail the user-visible command for what amounts to bookkeeping.
func (s *Store) MarkSyncCompleted(remoteURL string) error {
	if remoteURL == "" {
		return fmt.Errorf("MarkSyncCompleted: remoteURL is required")
	}
	_, err := s.DB.Exec(
		`UPDATE sync_remotes SET last_sync_at = CURRENT_TIMESTAMP, last_sync_error = NULL WHERE remote_url = ?`,
		remoteURL,
	)
	return err
}

// MarkSyncFailed records the failure message from a failed sync run
// on the row for remoteURL (BACI-89). last_sync_at is left untouched
// so the UI keeps showing when the mirror was last known-good. No-op
// (silent) if the row is missing — same bookkeeping-tolerant contract
// as MarkSyncCompleted.
func (s *Store) MarkSyncFailed(remoteURL, errMsg string) error {
	if remoteURL == "" {
		return fmt.Errorf("MarkSyncFailed: remoteURL is required")
	}
	if errMsg == "" {
		errMsg = "sync failed"
	}
	_, err := s.DB.Exec(
		`UPDATE sync_remotes SET last_sync_error = ? WHERE remote_url = ?`,
		errMsg, remoteURL,
	)
	return err
}
