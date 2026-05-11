package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// historyView shows the audit log for the current repo, newest first. Each
// row is a single line; the user can scroll with j/k or page up/down.
type historyView struct {
	store *store.Store
	repo  *model.Repo

	entries []*model.HistoryEntry
	row     int
	scroll  int
	err     error
}

func newHistoryView(s *store.Store, repo *model.Repo) *historyView {
	h := &historyView{store: s, repo: repo}
	h.reload()
	return h
}

func (h *historyView) reload() {
	entries, err := h.store.ListHistory(store.HistoryFilter{
		RepoID: &h.repo.ID,
		Limit:  500,
	})
	if err != nil {
		h.err = err
		return
	}
	h.err = nil
	h.entries = entries
	if h.row >= len(entries) {
		h.row = max(0, len(entries)-1)
	}
}

func (h *historyView) Init() tea.Cmd    { return nil }
func (h *historyView) Status() string   { return "" }
func (h *historyView) HasOverlay() bool { return false }
func (h *historyView) CloseOverlay()    {}
func (h *historyView) Breadcrumb() string { return "" }

func (h *historyView) Help() string {
	return "j/k scroll · g/G top/bottom · r reload · q quit"
}

func (h *historyView) Update(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	switch key.String() {
	case "j", "down":
		if h.row < len(h.entries)-1 {
			h.row++
		}
	case "k", "up":
		if h.row > 0 {
			h.row--
		}
	case "g", "home":
		h.row = 0
		h.scroll = 0
	case "G", "end":
		if len(h.entries) > 0 {
			h.row = len(h.entries) - 1
		}
	case "pgdown", " ":
		h.row += 10
		if h.row >= len(h.entries) {
			h.row = max(0, len(h.entries)-1)
		}
	case "pgup":
		h.row -= 10
		if h.row < 0 {
			h.row = 0
		}
	case "r":
		h.reload()
	}
	return nil
}

func (h *historyView) View(width, height int) string {
	if width == 0 || height == 0 {
		return ""
	}
	innerWidth := width - 2
	if innerWidth < 40 {
		innerWidth = 40
	}
	innerHeight := height - 2
	if innerHeight < 5 {
		innerHeight = 5
	}

	box := lipgloss.NewStyle().
		Border(colBorder).BorderForeground(colFocusBorder).
		Width(innerWidth).Height(innerHeight)

	if h.err != nil {
		return box.Render(errorStyle.Render(h.err.Error()))
	}
	if len(h.entries) == 0 {
		return box.Render(mutedStyle.Padding(1, 2).Render("(no history yet)"))
	}

	titleBar := lipgloss.NewStyle().
		Bold(true).Foreground(lipgloss.Color("231")).Background(colHeaderFocus).
		Width(innerWidth).Padding(0, 1).
		Render(fmt.Sprintf("History · %d entries (newest first)", len(h.entries)))

	// Reserve rows: title bar (1) + column header (1) + horizontal rule (1).
	rowsHeight := innerHeight - 3
	if rowsHeight < 1 {
		rowsHeight = 1
	}

	// Keep the cursor in view: scroll so the row sits within [scroll, scroll+rowsHeight).
	if h.row < h.scroll {
		h.scroll = h.row
	}
	if h.row >= h.scroll+rowsHeight {
		h.scroll = h.row - rowsHeight + 1
	}
	if h.scroll < 0 {
		h.scroll = 0
	}
	end := min(h.scroll+rowsHeight, len(h.entries))

	// Auto-size widths from the visible entries (with sensible caps so a
	// runaway long actor/op doesn't squeeze the details column to nothing).
	const (
		whenW     = 12
		actorMin  = 6
		actorCap  = 16
		opMin     = 6
		opCap     = 22
		targetMin = 6
		targetCap = 14
	)
	actorW, opW, targetW := actorMin, opMin, targetMin
	for i := h.scroll; i < end; i++ {
		e := h.entries[i]
		if l := lipgloss.Width(e.Actor); l > actorW {
			actorW = l
		}
		if l := lipgloss.Width(e.Op); l > opW {
			opW = l
		}
		// Target labels render bracketed, so reserve 2 extra columns when
		// the label is non-empty.
		tw := lipgloss.Width(e.TargetLabel)
		if tw > 0 {
			tw += 2
		}
		if tw > targetW {
			targetW = tw
		}
	}
	if actorW > actorCap {
		actorW = actorCap
	}
	if opW > opCap {
		opW = opCap
	}
	if targetW > targetCap {
		targetW = targetCap
	}

	// Layout: " when │ actor │ op │ target │ details ". Each cell carries 1
	// column of left+right padding inside the separators. Five cells, four
	// separators.
	const sep = "│"
	cellPad := 2 // 1 left + 1 right
	fixedW := whenW + actorW + opW + targetW + 5*cellPad + 4*lipgloss.Width(sep)
	detailsW := innerWidth - fixedW
	if detailsW < 8 {
		detailsW = 8
	}

	cell := func(text string, w int) string {
		t := truncate(text, w)
		// Manual right-pad — using lipgloss.Width handles ANSI in pre-styled
		// inputs, but here all inputs are plain text.
		return " " + t + strings.Repeat(" ", w-lipgloss.Width(t)) + " "
	}
	headerCell := func(text string, w int) string {
		return " " + boldStyle.Render(truncate(text, w)) +
			strings.Repeat(" ", w-lipgloss.Width(truncate(text, w))) + " "
	}

	colHeader := headerCell("when", whenW) + sep +
		headerCell("actor", actorW) + sep +
		headerCell("op", opW) + sep +
		headerCell("target", targetW) + sep +
		headerCell("details", detailsW)

	// Horizontal rule under the header — a `─` run with `┼` at each column
	// boundary so the table has visible joints.
	rule := strings.Repeat("─", whenW+cellPad) + "┼" +
		strings.Repeat("─", actorW+cellPad) + "┼" +
		strings.Repeat("─", opW+cellPad) + "┼" +
		strings.Repeat("─", targetW+cellPad) + "┼" +
		strings.Repeat("─", detailsW+cellPad)
	rule = mutedStyle.Render(rule)

	var rows []string
	for i := h.scroll; i < end; i++ {
		e := h.entries[i]
		target := e.TargetLabel
		if target != "" {
			target = "[" + target + "]"
		}
		line := cell(formatRelative(e.CreatedAt), whenW) + sep +
			cell(e.Actor, actorW) + sep +
			cell(e.Op, opW) + sep +
			cell(target, targetW) + sep +
			cell(oneLine(e.Details), detailsW)

		styled := lipgloss.NewStyle().Width(innerWidth)
		if i == h.row {
			styled = styled.Background(cardSelectedBG).Foreground(lipgloss.Color("231"))
		}
		rows = append(rows, styled.Render(line))
	}

	body := strings.Join(rows, "\n")
	return box.Render(lipgloss.JoinVertical(lipgloss.Left, titleBar, colHeader, rule, body))
}

// formatRelative returns short relatively-friendly timestamps. We render
// absolute timestamps for old entries because "247 days ago" is annoying.
func formatRelative(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return t.Format("2006-01-02")
	}
}
