// Package sync hosts the git-backed sync engine. The bulk of the
// engine (engine.go, import.go, export.go, etc.) lands across phases
// per docs/git-sync-implementation.md.
package sync

import "github.com/mrgeoffrich/bacio/internal/identity"

// New is a thin re-export of identity.New so callers inside the sync
// package don't have to import a second package just for uuid
// generation. The canonical home for the helper is internal/identity —
// it lives there so the store layer can consume it without importing
// internal/sync (which would create a cycle: store → sync → store).
func New() string { return identity.New() }
