package store

import (
	"database/sql"
	"fmt"
	"time"
)

const (
	// UILeaderHeartbeatInterval is how often each UI process ticks its
	// election loop — renewing when leader, trying to acquire when standby.
	UILeaderHeartbeatInterval = 10 * time.Second

	// UILeaderStaleSeconds is the staleness threshold for the ACQUIRE
	// conditional UPDATE. A lease whose heartbeat_at is older than this
	// many seconds is considered abandoned and can be taken over.
	// The 18x ratio vs the heartbeat interval means a GC pause or slow
	// write won't trigger a false takeover.
	UILeaderStaleSeconds = 180
)

// LeaderInfo is the current state of the ui_leader row, returned by
// CurrentLeader for display purposes (e.g. "standby — {Label} has control").
type LeaderInfo struct {
	Token      string
	Label      string
	AcquiredAt time.Time
}

// TryAcquireLeader attempts to take the lease. It succeeds only if the
// current heartbeat_at is older than UILeaderStaleSeconds — i.e. the
// previous holder has died or released. Returns (true, nil) on success.
func (s *Store) TryAcquireLeader(token, label string) (bool, error) {
	modifier := fmt.Sprintf("-%d seconds", UILeaderStaleSeconds)
	res, err := s.DB.Exec(`
		UPDATE ui_leader
		   SET holder_token = ?, holder_label = ?,
		       acquired_at  = datetime('now'),
		       heartbeat_at = datetime('now')
		 WHERE id = 1 AND heartbeat_at < datetime('now', ?)`,
		token, label, modifier)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// RenewLeader bumps heartbeat_at, gated only on holder_token matching.
// Returns (false, nil) when another process has taken the lease (the holder's
// token no longer matches) — the caller must demote to standby immediately.
func (s *Store) RenewLeader(token string) (bool, error) {
	res, err := s.DB.Exec(`
		UPDATE ui_leader SET heartbeat_at = datetime('now')
		 WHERE id = 1 AND holder_token = ?`, token)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// ReleaseLeader is a best-effort graceful release: it zeroes heartbeat_at so
// the next ACQUIRE succeeds immediately rather than waiting out the 3-minute
// stale window. Only succeeds when the caller still holds the token; a no-op
// (and not an error) when the lease has already been taken over.
func (s *Store) ReleaseLeader(token string) error {
	_, err := s.DB.Exec(`
		UPDATE ui_leader SET heartbeat_at = 0
		 WHERE id = 1 AND holder_token = ?`, token)
	return err
}

// CurrentLeader returns the current holder's token and label for display.
// AcquiredAt is zero when the lease has never been acquired.
func (s *Store) CurrentLeader() (LeaderInfo, error) {
	var info LeaderInfo
	var acquiredAt sql.NullTime
	err := s.DB.QueryRow(`
		SELECT holder_token, holder_label, acquired_at
		  FROM ui_leader WHERE id = 1`).Scan(
		&info.Token, &info.Label, &acquiredAt)
	if err != nil {
		return LeaderInfo{}, err
	}
	if acquiredAt.Valid {
		info.AcquiredAt = acquiredAt.Time
	}
	return info, nil
}
