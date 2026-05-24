package tui

import (
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// tuiActor is the name stamped on board-initiated history rows. The
// TUI is always driven by a human at the keyboard, so it stamps the
// literal "user" placeholder until real auth lands — matching the
// CLI's actor() fallback for non-agent calls.
func tuiActor() string {
	return "user"
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
	_ = s.RecordHistory(e)
}
