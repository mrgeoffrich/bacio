package sync

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ReleaseFunc releases an acquired sync lock. Idempotent — safe to
// call more than once; subsequent calls are no-ops.
type ReleaseFunc func() error

// LockTimeoutSeconds bounds the time AcquireSyncLock will wait before
// giving up. 30 s matches the design doc; a `bacio sync` invocation
// should complete well within that window, so anything longer means
// either a stuck sync from a crashed process or a developer-side
// debugging stall.
const LockTimeoutSeconds = 30

// ErrLockTimeout is returned when AcquireSyncLock can't get the
// lock within LockTimeoutSeconds.
var ErrLockTimeout = errors.New("sync: timed out waiting for sync lock; another bacio sync may be running")

// AcquireSyncLock takes an exclusive flock-style lock on
// <dbPath>.sync.lock. Returns a release function the caller must
// invoke (typically via defer) to drop the lock. On Windows the
// implementation is a best-effort no-op with a warning to stderr —
// per the implementation doc's "v1 might accept no lock on Windows"
// note. Switching to LockFileEx is a follow-up.
//
// The lock file is created on first acquire and left in place
// afterwards (lifetime tied to the DB, not the sync run). Removing
// it would require a release-time race with anyone waiting for the
// lock; the cost of one stale empty file is minimal.
func AcquireSyncLock(dbPath string) (ReleaseFunc, error) {
	if dbPath == "" {
		return nil, fmt.Errorf("AcquireSyncLock: empty dbPath")
	}
	lockPath := dbPath + ".sync.lock"
	// Ensure the parent directory exists — on a fresh machine (or a CI
	// runner) ~/.bacio/ may not have been created yet.
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return nil, fmt.Errorf("create sync lock dir %q: %w", filepath.Dir(lockPath), err)
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open sync lock %q: %w", lockPath, err)
	}
	if err := acquireFlock(f); err != nil {
		_ = f.Close()
		return nil, err
	}
	released := false
	return func() error {
		if released {
			return nil
		}
		released = true
		// Best-effort unlock then close. Failures here only affect
		// the next acquire, and OS-level fd close releases any held
		// flock as a backstop.
		_ = releaseFlock(f)
		return f.Close()
	}, nil
}
