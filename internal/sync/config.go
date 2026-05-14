package sync

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mrgeoffrich/bacio/internal/git"
)

// ErrNoConfig is the sentinel error returned by ReadProjectConfig
// when .bacio/config.yaml is missing. Callers use this to distinguish
// "no sync configured here" (a setup-hint case) from "the config
// exists but is broken" (a real error).
var ErrNoConfig = errors.New("no .bacio/config.yaml in project repo")

// ProjectConfig is the parsed shape of .bacio/config.yaml — the
// reverse pointer that lets a project repo find its sync remote.
//
// This file is machine-local, NOT shared via git: it's written by
// `bacio sync init` / `bacio sync clone` on each machine and the
// `.bacio/` directory should be gitignored (it also holds the
// per-machine agent identity). Collaborators learn the sync remote
// out-of-band and pass it to `bacio sync clone --remote <url>`. The
// remote therefore only ever enters bacio through a trusted CLI flag,
// not through a checked-in file an attacker could poison.
type ProjectConfig struct {
	Sync ProjectSync `yaml:"sync"`
}

// ProjectSync holds the sync-specific configuration. v1 only
// carries the canonical remote URL; later we may add a `mode`
// field to flip in-repo vs external sync. Forward-compatible: an
// older bacio reading a future config with extra keys errors loudly
// thanks to KnownFields(true), so we'll bump SchemaVersion when the
// time comes.
type ProjectSync struct {
	Remote string `yaml:"remote"`
}

// ConfigPath returns the absolute path to a project's .bacio/config.yaml
// given the project's working-tree root.
func ConfigPath(projectRoot string) string {
	return filepath.Join(projectRoot, ".bacio", "config.yaml")
}

// ReadProjectConfig parses .bacio/config.yaml at projectRoot. Returns
// ErrNoConfig if the file is missing.
func ReadProjectConfig(projectRoot string) (*ProjectConfig, error) {
	abs := ConfigPath(projectRoot)
	b, err := os.ReadFile(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNoConfig
		}
		return nil, fmt.Errorf("read project config: %w", err)
	}
	var c ProjectConfig
	if err := strictDecode(b, &c); err != nil {
		return nil, fmt.Errorf("parse project config: %w", err)
	}
	// Defence in depth: an older bacio (or a hand-edited file) may
	// carry a remote that predates URL validation. Reject it loudly
	// here rather than letting it reach `git`.
	if c.Sync.Remote != "" {
		if err := git.ValidateRemoteURL(c.Sync.Remote); err != nil {
			return nil, fmt.Errorf("parse project config: invalid sync.remote: %w", err)
		}
	}
	return &c, nil
}

// WriteProjectConfig writes .bacio/config.yaml at projectRoot with the
// given config. Creates the .bacio directory if it doesn't yet exist.
// The output goes through the canonical emitter so it's byte-stable
// across runs.
func WriteProjectConfig(projectRoot string, c ProjectConfig) error {
	root := Map(
		Pair{"sync", Map(
			Pair{"remote", Str(c.Sync.Remote)},
		)},
	)
	bytes, err := Emit(root)
	if err != nil {
		return fmt.Errorf("emit project config: %w", err)
	}
	abs := ConfigPath(projectRoot)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return fmt.Errorf("mkdir .bacio: %w", err)
	}
	if err := os.WriteFile(abs, bytes, 0o644); err != nil {
		return fmt.Errorf("write project config: %w", err)
	}
	return nil
}
