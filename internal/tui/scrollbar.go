package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// renderVerticalScrollbar produces a height-row, single-column-wide string
// representing the scroll position of a content region. The track always
// renders in the muted colour. When focused is true the thumb takes the
// focus accent; when false it stays muted so an unfocused panel's
// scrollbar fades into the chrome. Thumb size is proportional to
// (visible / total); thumb position is proportional to (offset /
// maxScroll). When the content fits in the visible region the thumb
// stretches to the full height — the column always reserves the same
// space, so toggling between scrollable and not doesn't shift the
// surrounding layout.
func renderVerticalScrollbar(height, totalLines, scrollOffset int, focused bool) string {
	if height <= 0 {
		return ""
	}

	thumbH := height
	thumbTop := 0
	if totalLines > height {
		thumbH = height * height / totalLines
		if thumbH < 1 {
			thumbH = 1
		}
		if thumbH > height {
			thumbH = height
		}
		maxScroll := totalLines - height
		if maxScroll > 0 {
			thumbTop = scrollOffset * (height - thumbH) / maxScroll
		}
		if thumbTop < 0 {
			thumbTop = 0
		}
		if thumbTop > height-thumbH {
			thumbTop = height - thumbH
		}
	}

	trackStyle := lipgloss.NewStyle().Foreground(mutedColor)
	thumbColor := mutedColor
	if focused {
		thumbColor = colFocusBorder
	}
	thumbStyle := lipgloss.NewStyle().Foreground(thumbColor)

	lines := make([]string, height)
	for i := 0; i < height; i++ {
		if i >= thumbTop && i < thumbTop+thumbH {
			lines[i] = thumbStyle.Render("█")
		} else {
			// Dashed vertical (not solid │) so the track is visually
			// distinct from frame borders that may sit beside it.
			lines[i] = trackStyle.Render("┊")
		}
	}
	return strings.Join(lines, "\n")
}
