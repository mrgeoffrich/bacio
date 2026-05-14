package tui

import (
	osuser "os/user"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// tuiActor resolves the actor name stamped on board-initiated history
// rows: the OS username, validated through store.ValidateActor, with an
// "unknown" fallback. The TUI has no --user flag (unlike the CLI), so
// this is the only source. On platforms where os/user is unsupported
// (js/wasm) Current() errors and we fall back cleanly.
func tuiActor() string {
	if u, err := osuser.Current(); err == nil && u.Username != "" {
		if clean, err := store.ValidateActor(u.Username); err == nil {
			return clean
		}
	}
	return "unknown"
}

// recordTUIOp writes an audit-log row for a TUI-initiated mutation,
// mirroring the CLI's recordOp / the client's recordOp. It differs in
// one way: write failures are swallowed silently rather than printed to
// stderr — the TUI owns the alt-screen, and a stray stderr line would
// corrupt the display. Losing one history row is the lesser evil.
func recordTUIOp(s *store.Store, e model.HistoryEntry) {
	if e.Actor == "" {
		e.Actor = tuiActor()
	}
	if e.Actor == "" {
		e.Actor = "unknown"
	}
	_ = s.RecordHistory(e)
}
