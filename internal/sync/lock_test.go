package sync

import (
	"path/filepath"
	"testing"
	"time"
)

// TestSyncLock_AcquireAndRelease covers the happy-path: acquire,
// release, re-acquire. Uses a temp file so it doesn't trample on
// any real DB lock.
func TestSyncLock_AcquireAndRelease(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "fake.sqlite")
	release1, err := AcquireSyncLock(dbPath)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if err := release1(); err != nil {
		t.Fatalf("first release: %v", err)
	}
	// Re-acquire should succeed once the first is released.
	release2, err := AcquireSyncLock(dbPath)
	if err != nil {
		t.Fatalf("second acquire: %v", err)
	}
	if err := release2(); err != nil {
		t.Fatalf("second release: %v", err)
	}
}

// TestSyncLock_DoubleReleaseSafe ensures the release closure is
// idempotent — a defer cleanup that double-fires shouldn't panic
// or surface an error to the caller.
func TestSyncLock_DoubleReleaseSafe(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "fake.sqlite")
	release, err := AcquireSyncLock(dbPath)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if err := release(); err != nil {
		t.Fatalf("first release: %v", err)
	}
	if err := release(); err != nil {
		t.Fatalf("second release: %v", err)
	}
}

// timeNow is here to keep the time import used; trims unused-import
// drift if the test set ever shrinks.
var _ = time.Now
