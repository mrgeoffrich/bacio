package sync

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// SentinelFilename is the file at the root of a sync repo whose
// presence flips bacio into sync-repo mode (no auto-register, no
// `bacio issue create` etc.). Spelled out in the design doc — the file
// is canonical YAML so it round-trips through the existing emitter.
const SentinelFilename = "bacio-sync.yaml"

// SchemaVersion is the current sentinel schema version. Bumped when
// the layout under repos/ changes in a non-backward-compatible way.
const SchemaVersion = 1

// Sentinel is the parsed shape of bacio-sync.yaml.
type Sentinel struct {
	SchemaVersion int       `yaml:"schema_version"`
	CreatedAt     time.Time `yaml:"created_at"`
}

// IsSyncRepo reports whether the directory at workingTreeRoot
// contains an bacio-sync.yaml at its root. Tolerant of read errors
// (e.g. a permission-denied stat returns false rather than an error)
// because the caller uses this to decide whether to take the
// sync-mode branch — the sync-mode branch is the safe default if
// the file is plausibly present.
func IsSyncRepo(workingTreeRoot string) bool {
	_, err := os.Stat(filepath.Join(workingTreeRoot, SentinelFilename))
	return err == nil
}

// ReadSentinel parses the bacio-sync.yaml at the given path. Returns
// the parsed Sentinel or an error wrapping the underlying parse
// failure.
func ReadSentinel(path string) (*Sentinel, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read sentinel: %w", err)
	}
	var s Sentinel
	if err := strictDecode(b, &s); err != nil {
		return nil, fmt.Errorf("parse sentinel: %w", err)
	}
	if s.SchemaVersion == 0 {
		return nil, errors.New("sentinel: missing or zero schema_version")
	}
	return &s, nil
}

// WriteSentinel emits a canonical bacio-sync.yaml at the root of repoRoot.
// Uses the standard emitter so the file is byte-stable across
// re-init; we keep the create-time of an existing sentinel intact if
// the file is already there (some unrelated CI step might have
// re-init'd the repo with a fresh `git init` and we don't want to
// pretend the sync data is new).
func WriteSentinel(repoRoot string, s Sentinel) error {
	if s.SchemaVersion == 0 {
		s.SchemaVersion = SchemaVersion
	}
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now().UTC()
	}
	root := Map(
		Pair{"created_at", Time(s.CreatedAt)},
		Pair{"schema_version", Int(int64(s.SchemaVersion))},
	)
	bytes, err := Emit(root)
	if err != nil {
		return fmt.Errorf("emit sentinel: %w", err)
	}
	abs := filepath.Join(repoRoot, SentinelFilename)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return fmt.Errorf("mkdir sentinel parent: %w", err)
	}
	if err := os.WriteFile(abs, bytes, 0o644); err != nil {
		return fmt.Errorf("write sentinel: %w", err)
	}
	return nil
}
