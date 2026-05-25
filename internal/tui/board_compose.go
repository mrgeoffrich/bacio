//go:build !js

package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/client"
)

// composeOverlay is the BACI-168 "+ from prompt" composer: a single
// focused bubbles/textarea on the board view. Submit (ctrl+s) creates
// an issue with a synthesised title + the typed description, then
// queues a `scope` dispatch so the worker rewrites it into a
// triage-ready ticket. esc cancels with no writes.
//
// Lives on boardView as a pointer field next to questionOlay; the
// shell's CapturesInput / HasOverlay / CloseOverlay each grow a
// "|| b.composeOverlay != nil" clause so the textarea keeps q and the
// digit keys while focused — see the cookbook's CapturesInput contract.
//
// The native (!js) build pulls bubbles/textarea; the wasm build
// (board_compose_wasm.go) stubs this out — the wasm demo is read-only,
// same as Settings (settings_wasm.go).
type composeOverlay struct {
	ta         textarea.Model
	err        error
	submitting bool
}

// composeTitleMaxCells is the cap on the synthesised title, measured in
// terminal cells (not runes). 60 keeps the title legible in a kanban
// card while leaving the scope worker freedom to rewrite to something
// cleaner downstream.
const composeTitleMaxCells = 60

// composeOverlayHeight is the textarea's row height. Six rows comfortably
// fits a one-line idea plus 2-3 wrap lines without dominating the board
// behind it.
const composeOverlayHeight = 6

// composeOverlayInnerWidth is the requested textarea width before
// View-time clamping by the parent layout. Matches the syncLinkOverlay's
// 60-col textinput width so the overlay reads as kin to the existing
// pattern.
const composeOverlayInnerWidth = 60

// openComposeOverlay constructs the composer state and assigns it to
// b.composeOverlay. The textarea starts focused and empty; the
// placeholder advertises the worker contract so the user knows the
// description doesn't need to be polished.
func (b *boardView) openComposeOverlay() tea.Cmd {
	ta := textarea.New()
	ta.Placeholder = "Describe the issue you want scoped (one line is fine — the worker will flesh it out)"
	ta.Prompt = ""
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	ta.MaxHeight = 0
	ta.SetWidth(composeOverlayInnerWidth)
	ta.SetHeight(composeOverlayHeight)
	b.composeOverlay = &composeOverlay{ta: ta}
	return b.composeOverlay.ta.Focus()
}

// Update routes keystrokes while the composer is open. ctrl+s submits,
// esc closes without a write; every other key forwards to the textarea
// so enter inserts a newline and q / 1-9 are typed literally. While
// submitting (the create + dispatch pair is mid-flight) ctrl+s and esc
// are swallowed so a double-press can't fire a second create.
func (o *composeOverlay) Update(b *boardView, msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		// Non-key messages (e.g. cursor-blink ticks) still flow to the
		// textarea so it can re-arm its blinker.
		var cmd tea.Cmd
		o.ta, cmd = o.ta.Update(msg)
		return cmd
	}
	if o.submitting {
		// Submit is synchronous against the local store today, but the
		// guard is cheap defence-in-depth against a future async path.
		return nil
	}
	switch key.String() {
	case "esc":
		b.composeOverlay = nil
		return nil
	case "ctrl+s":
		o.submit(b)
		return nil
	}
	var cmd tea.Cmd
	o.ta, cmd = o.ta.Update(msg)
	return cmd
}

// submit drives the BACI-168 create + scope-dispatch chain. Empty
// descriptions are a quiet no-op (the placeholder is the affordance,
// no error message needed). Create-errors keep the overlay open with
// the typed text intact so the user can correct and retry. A
// dispatch-error after a successful create is the orphan-issue case
// the Phase 3 design committed to: close the overlay, jump the cursor
// to the new card, and stash a "created but couldn't queue scope"
// message in b.err so the footer surfaces it.
func (o *composeOverlay) submit(b *boardView) {
	desc := strings.TrimSpace(o.ta.Value())
	if desc == "" {
		// No-op on empty — the placeholder makes the affordance clear,
		// and surfacing an error here would feel punitive.
		return
	}
	o.submitting = true
	defer func() { o.submitting = false }()

	c := client.NewLocalFromStore(b.store, b.actor)
	in := inputs.IssueAddInput{
		Title:       titleFromDescription(desc),
		Description: desc,
	}
	iss, err := c.CreateIssue(context.Background(), b.repo, in, false)
	if err != nil {
		o.err = err
		return
	}
	// Create succeeded — the overlay closes regardless of what the
	// dispatch step does so the user sees the new card on the board.
	b.composeOverlay = nil
	_, derr := c.AutoDispatchIssue(context.Background(), b.repo, iss.Key, "scope", false)
	// Refresh the board so the new card appears, then jump the cursor
	// onto it. Both failures are non-fatal: the worst case is the new
	// card lives on the board with the cursor in its previous slot.
	if rerr := b.reload(); rerr != nil {
		b.err = rerr
	} else {
		b.err = nil
	}
	if ferr := b.focusOnKey(iss.Key); ferr != nil {
		// Couldn't find the new key in any visible column (e.g. a feature
		// filter is hiding it). Leave the cursor alone — the new card is
		// in the store, just not in view.
		_ = ferr
	}
	if derr != nil {
		b.err = fmt.Errorf("issue %s created, scope dispatch couldn't be queued: %w", iss.Key, derr)
	}
}

// View renders the composer overlay chrome — title bar, textarea body,
// optional error line, footer hint — inside a bordered block sized to
// innerWidth. The chrome mirrors renderSyncLinkOverlay so the surface
// reads as kin to the existing path-entry overlay.
func (o *composeOverlay) View(innerWidth int) string {
	if o == nil {
		return ""
	}
	if innerWidth < 24 {
		innerWidth = 24
	}
	contentWidth := innerWidth - 4 // 2-col padding each side + 2 for the border
	if contentWidth < 10 {
		contentWidth = 10
	}
	// Resize the textarea to fit the parent width so wrapping looks
	// consistent regardless of terminal width.
	o.ta.SetWidth(contentWidth)

	header := boldStyle.Render("New issue → scope")
	hint := mutedStyle.Render("  (ctrl+s creates the issue + queues a scope dispatch)")
	lines := []string{header + hint, "", o.ta.View()}
	if o.err != nil {
		lines = append(lines, "", errorStyle.Render(truncate(o.err.Error(), contentWidth)))
	}
	footer := "ctrl+s create & scope · esc cancel"
	if o.submitting {
		footer = "working…"
	}
	lines = append(lines, "", mutedStyle.Render(footer))

	return lipgloss.NewStyle().
		Border(colBorder).BorderForeground(colFocusBorder).
		Width(innerWidth - 2).Padding(0, 1).
		Render(strings.Join(lines, "\n"))
}

// viewComposeOverlay paints the composer centred on the board area.
// The overlay is intentionally small — the surrounding columns stay
// faintly visible behind it via lipgloss.Place so the user keeps
// spatial context. innerWidth caps the overlay at ~70 cells; on narrow
// terminals the overlay shrinks to fit.
func (b *boardView) viewComposeOverlay(width, height int) string {
	if b.composeOverlay == nil {
		// Defence: View can be called between key handling and the next
		// model tick where the overlay is set to nil; return empty so
		// we don't crash on a nil deref.
		return ""
	}
	innerWidth := 70
	if innerWidth > width-4 {
		innerWidth = width - 4
	}
	if innerWidth < 30 {
		innerWidth = 30
		if innerWidth > width {
			innerWidth = width
		}
	}
	body := b.composeOverlay.View(innerWidth)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, body)
}

// titleFromDescription synthesises a kanban title from the typed
// description: trim whitespace, take the first non-empty line, then
// clip to composeTitleMaxCells *cells* (not runes — emoji and CJK are
// double-wide). lipgloss.Width is the existing cell-aware measurement
// used elsewhere in the TUI, so we lean on it here rather than adding
// a direct go-runewidth import.
//
// An all-whitespace input returns the literal "(scope)" so the
// downstream CreateIssue "title is required" check can't trip on a
// description that's only newlines — composeOverlay.submit also
// rejects empty input, so this is defence-in-depth.
func titleFromDescription(desc string) string {
	trimmed := strings.TrimSpace(desc)
	if trimmed == "" {
		return "(scope)"
	}
	// First non-empty line.
	line := trimmed
	if idx := strings.IndexByte(line, '\n'); idx >= 0 {
		line = line[:idx]
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return "(scope)"
	}
	return truncateCells(line, composeTitleMaxCells)
}

// truncateCells clips s to at most max display cells, walking runes and
// measuring each via lipgloss.Width so emoji / CJK are counted as the
// 2-cell glyphs they render as. No ellipsis is appended — the scope
// worker rewrites the title anyway, so an extra char of signal beats
// a "…" suffix.
func truncateCells(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= max {
		return s
	}
	// Walk runes, accumulating cells until adding the next rune would
	// exceed max. Built rune-by-rune rather than per-byte so wide runes
	// are atomic.
	var b strings.Builder
	cells := 0
	for _, r := range s {
		w := lipgloss.Width(string(r))
		if cells+w > max {
			break
		}
		b.WriteRune(r)
		cells += w
	}
	return b.String()
}
