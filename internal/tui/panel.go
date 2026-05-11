package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// scrollableBlock renders body inside a (width × height) viewport with a
// 1-column vertical scrollbar pinned to the right edge. *scroll is
// clamped in place against the body's natural height. The returned
// string is always exactly height rows — content longer than height is
// strictly clipped, content shorter is padded — so callers can drop it
// into JoinHorizontal/Vertical without the surrounding layout growing.
//
// Useful inside layouts that already have an outer border (e.g. the card
// overlay's top half). For a stand-alone bordered panel use markdownPanel.
// focused picks the scrollbar thumb colour (accent vs. muted).
func scrollableBlock(width, height int, body string, scroll *int, focused bool) string {
	if width < 12 {
		width = 12
	}
	if height < 1 {
		height = 1
	}
	contentWidth := width - 1 // 1 col reserved for the scrollbar

	total := totalLines(body)
	maxScroll := max(0, total-height)
	if scroll != nil {
		if *scroll > maxScroll {
			*scroll = maxScroll
		}
		if *scroll < 0 {
			*scroll = 0
		}
	}
	off := 0
	if scroll != nil {
		off = *scroll
	}

	visible := scrollLines(body, off, height)
	if missing := height - totalLines(visible); missing > 0 {
		visible += strings.Repeat("\n", missing)
	}
	// Pad each line out to contentWidth so the scrollbar lines up at the
	// same x-coordinate every row regardless of the content's width.
	// Width() can soft-wrap a too-long line and add rows, so re-clip
	// afterwards to keep the strict height guarantee.
	visible = lipgloss.NewStyle().Width(contentWidth).Render(visible)
	visible = scrollLines(visible, 0, height)

	bar := renderVerticalScrollbar(height, total, off, focused)
	return lipgloss.JoinHorizontal(lipgloss.Top, visible, bar)
}

// panelBox wraps an inner string in the standard rounded-border + padded
// outer style used by every card-overlay panel. Result is always
// exactly (width × height): inner content longer than the available
// area is clipped, shorter is padded. focused picks the border colour
// (accent vs. muted) so callers can signal which panel currently has
// focus.
func panelBox(width, height int, inner string, focused bool) string {
	borderFG := colBorderColor
	if focused {
		borderFG = colFocusBorder
	}
	// Pre-clip the inner content so the box can never exceed height
	// rows. lipgloss Height() pads short content but doesn't truncate
	// long content — a single overflowing inner would otherwise grow
	// the box and misalign whatever sits beside it in JoinHorizontal.
	if cap := height - 4; cap > 0 {
		inner = scrollLines(inner, 0, cap)
	}
	return lipgloss.NewStyle().
		Border(colBorder).
		BorderForeground(borderFG).
		Width(width-2).
		Height(height-2).
		Padding(1, 2).
		Render(inner)
}

// padCell renders content (each line at most cellW-4 visible cols)
// inside a horizontal-only padded block of width cellW, returning the
// resulting lines. Line count is preserved. Used by composite frames
// to produce per-cell content already padded to its slot width before
// the frame stitches everything together with shared borders.
func padCell(content string, cellW int) []string {
	if cellW < 1 {
		return nil
	}
	out := lipgloss.NewStyle().Width(cellW).Padding(0, 2).Render(content)
	return strings.Split(out, "\n")
}

// renderOverlayFrame composes the issue-overlay layout inside a single
// rounded outer border with shared horizontal/vertical dividers
// between sections. Inputs are pre-rendered content split into lines;
// each line must already be exactly its cell's width (no trailing
// newlines). The frame draws the borders only — caller bakes any
// horizontal padding into the cell content (typically via padCell).
//
//	╭──────────────────────────────╮
//	│ headerLines (innerW)         │
//	├──────────────────────────────┤
//	│ descLines                    │
//	├──────────────┬───────────────┤   T-down at the bottom split
//	│ leftLines    │ rightLines    │
//	╰──────────────┴───────────────╯   T-up matching the split
//
// leftW is the visible width of the left cell inside the outer borders;
// the right cell takes innerW - 1 - leftW (the -1 is the shared │
// between them).
func renderOverlayFrame(width int,
	headerLines, descLines, leftLines, rightLines []string,
	leftW int,
) string {
	if width < 4 {
		return ""
	}
	innerW := width - 2
	rightW := innerW - 1 - leftW
	if rightW < 0 {
		rightW = 0
		leftW = innerW - 1
	}

	border := lipgloss.NewStyle().Foreground(colBorderColor)
	v := border.Render("│")

	row := func(line string) string { return v + line + v }

	var out []string
	out = append(out, border.Render("╭"+strings.Repeat("─", innerW)+"╮"))
	for _, l := range headerLines {
		out = append(out, row(l))
	}
	out = append(out, border.Render("├"+strings.Repeat("─", innerW)+"┤"))
	for _, l := range descLines {
		out = append(out, row(l))
	}
	out = append(out, border.Render("├"+strings.Repeat("─", leftW)+"┬"+strings.Repeat("─", rightW)+"┤"))
	bottomH := len(leftLines)
	if len(rightLines) > bottomH {
		bottomH = len(rightLines)
	}
	for i := 0; i < bottomH; i++ {
		ll := strings.Repeat(" ", leftW)
		if i < len(leftLines) {
			ll = leftLines[i]
		}
		rl := strings.Repeat(" ", rightW)
		if i < len(rightLines) {
			rl = rightLines[i]
		}
		out = append(out, v+ll+v+rl+v)
	}
	out = append(out, border.Render("╰"+strings.Repeat("─", leftW)+"┴"+strings.Repeat("─", rightW)+"╯"))

	return strings.Join(out, "\n")
}

// markdownPanel wraps scrollableBlock with panelBox. Callers assemble
// body themselves (title, meta, headings, markdown, joined by "\n") so
// each view can keep its own structure. focused picks the border
// colour.
func markdownPanel(width, height int, body string, scroll *int, focused bool) string {
	innerWidth := width - 6 // 2 borders + 4 horizontal padding
	if innerWidth < 12 {
		innerWidth = 12
	}
	innerHeight := height - 4 // 2 borders + 2 vertical padding
	if innerHeight < 1 {
		innerHeight = 1
	}

	inner := scrollableBlock(innerWidth, innerHeight, body, scroll, focused)
	return panelBox(width, height, inner, focused)
}
