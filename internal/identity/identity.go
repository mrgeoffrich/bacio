// Package identity owns UUIDv7 generation. It lives in its own
// package (rather than under internal/sync) so the store layer can
// consume it without creating an import cycle: sync depends on store
// for read access, store depends on identity for record uuids, and
// identity depends on nothing in this codebase.
package identity

import "github.com/google/uuid"

// New returns a freshly-generated UUIDv7 string. UUIDv7 is time-ordered,
// which gives us roughly-chronological directory listings and stable
// ordering of comment filenames in the sync repo. The store layer
// calls this on every Create* path so each record carries an immutable
// identity from birth; the uuid backfill in migrate() calls it for
// rows that pre-date the column.
//
// Panics on uuid.NewV7 failure. The underlying call only returns an
// error if the system's crypto/rand fails — at which point the process
// has bigger problems than a missing uuid, and treating it as a
// programming error keeps every call site one-line.
func New() string {
	id, err := uuid.NewV7()
	if err != nil {
		panic("identity: uuid.NewV7 failed: " + err.Error())
	}
	return id.String()
}
