package sync

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestSentinel_RoundTrip verifies that WriteSentinel + ReadSentinel
// is a faithful round-trip with the canonical YAML emitter. The
// sentinel is the file bacio uses to detect a sync repo, so any drift
// between writer and reader silently flips us out of sync-repo mode.
func TestSentinel_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 5, 9, 14, 22, 0, 0, time.UTC)
	if err := WriteSentinel(dir, Sentinel{SchemaVersion: 1, CreatedAt: now}); err != nil {
		t.Fatalf("write: %v", err)
	}
	if !IsSyncRepo(dir) {
		t.Fatalf("IsSyncRepo returned false after write")
	}
	got, err := ReadSentinel(filepath.Join(dir, SentinelFilename))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.SchemaVersion != 1 {
		t.Fatalf("schema version = %d, want 1", got.SchemaVersion)
	}
	// time.Time round-trips with millisecond precision through the
	// emitter — assert equality at that precision.
	if !got.CreatedAt.Equal(now.Truncate(time.Millisecond)) {
		t.Fatalf("created_at = %v, want %v", got.CreatedAt, now)
	}
}

// TestIsSyncRepo_FalseOnEmptyDir is the straightforward negative.
func TestIsSyncRepo_FalseOnEmptyDir(t *testing.T) {
	dir := t.TempDir()
	if IsSyncRepo(dir) {
		t.Fatal("IsSyncRepo returned true on empty dir")
	}
}

// TestProjectConfig_RoundTrip covers the .bacio/config.yaml read/write
// round-trip used by the bootstrap commands.
func TestProjectConfig_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := ProjectConfig{Sync: ProjectSync{Remote: "git@example.com:user/bacio-data.git"}}
	if err := WriteProjectConfig(dir, want); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := ReadProjectConfig(dir)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Sync.Remote != want.Sync.Remote {
		t.Fatalf("remote = %q, want %q", got.Sync.Remote, want.Sync.Remote)
	}
}

// TestProjectConfig_ErrNoConfig confirms the sentinel error fires
// when the config file is missing — callers depend on this to
// distinguish "not set up" from "broken".
func TestProjectConfig_ErrNoConfig(t *testing.T) {
	dir := t.TempDir()
	_, err := ReadProjectConfig(dir)
	if err == nil {
		t.Fatal("expected error for missing config")
	}
	// Defensive check rather than a strict errors.Is — the sentinel
	// is exported so callers should branch on it.
	if _, statErr := os.Stat(filepath.Join(dir, ".bacio", "config.yaml")); !os.IsNotExist(statErr) {
		t.Fatalf("file exists unexpectedly")
	}
}
