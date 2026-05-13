package sync

import "github.com/mrgeoffrich/bacio/internal/store"

// Engine is the top-level coordinator for the sync layer. Phase 2
// shipped Export; Phase 3 adds Import; Phase 4 will add the
// pull/commit/push shell as Run().
//
// Actor is recorded against any audit op the sync engine writes
// (none directly; the cli layer wraps each phase with its own
// recordOp call, but the field is here so a future internal audit
// path doesn't have to thread it in twice).
//
// DryRun is honoured (where it makes sense) by every method on
// Engine. Export honours it by short-circuiting before any
// filesystem write; Import honours it by rolling back the outer
// transaction before commit.
type Engine struct {
	// Store is the live SQLite-backed *store.Store. Phase 2 used a
	// narrow read-only interface (StoreReader) to avoid an import
	// cycle that no longer exists — once Phase 1's identity helper
	// moved into internal/identity, sync was free to depend on store
	// directly. Tests use store.Open(":memory:") for the real schema.
	Store  *store.Store
	Actor  string
	DryRun bool

	// SkipPropagateDeletes disables Import phase 4 (the "rows in
	// sync_state but not seen on disk → delete" pass). Set by
	// bootstrap flows (sync init attach, sync clone) where
	// sync_state is not a trustworthy "what was last shared"
	// snapshot, so missing-on-disk does not imply
	// deleted-elsewhere. Steady-state bacio sync leaves this
	// false — that's where propagateDeletes is correct.
	SkipPropagateDeletes bool
}
