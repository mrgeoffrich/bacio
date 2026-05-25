//go:build js

package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// composeOverlay (wasm build) is a no-op stub. The native build
// (board_compose.go) backs the BACI-168 "+ from prompt" composer with
// a bubbles/textarea, which transitively imports atotto/clipboard —
// no js/wasm build available. The wasm demo is read-only by design
// (cmd/bacio-wasm has no save loop), same as the Settings tab, so a
// "composer doesn't open" branch is the right behaviour here.
type composeOverlay struct{}

// openComposeOverlay is a no-op on the wasm build: the board.go key
// handler still calls it but the field stays nil, HasOverlay /
// CapturesInput keep returning their non-composer defaults, and the
// keystroke falls through to the normal `n`/`N` handling.
func (b *boardView) openComposeOverlay() tea.Cmd { return nil }

// Update / View are kept on the wasm stub so any straggling reference
// outside the build-tagged code paths compiles cleanly. They never
// actually run on wasm because b.composeOverlay is always nil there.
func (o *composeOverlay) Update(b *boardView, msg tea.Msg) tea.Cmd { return nil }
func (o *composeOverlay) View(innerWidth int) string               { return "" }

// viewComposeOverlay is also a no-op on wasm — the board.View() guard
// gates entry to it on b.composeOverlay != nil, which never holds on
// the wasm build because openComposeOverlay is a no-op. Kept so the
// platform-neutral board.go compiles in both build configurations.
func (b *boardView) viewComposeOverlay(width, height int) string { return "" }
