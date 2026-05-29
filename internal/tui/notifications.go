package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// notificationsView (BACI-287) shows the global agent→user notification
// list, newest first. Like the desktop / web bell it is cross-repo — it
// lists notifications from every repo, each row labelled with its repo
// prefix. `u` toggles unread-only / all; `enter`/`m` marks the focused row
// read; `M` marks every unread row read. Reads go through the borrowed
// store; mark mutations go through the audited client.
type notificationsView struct {
	store  *store.Store
	client client.Client

	entries    []*model.Notification
	unreadOnly bool
	row        int
	scroll     int
	err        error
}

func newNotificationsView(s *store.Store, actor string) *notificationsView {
	v := &notificationsView{
		store:      s,
		client:     client.NewLocalFromStore(s, actor),
		unreadOnly: true,
	}
	v.reload()
	return v
}

func (v *notificationsView) reload() {
	entries, err := v.store.ListNotifications(store.NotificationFilter{
		UnreadOnly: v.unreadOnly,
		Limit:      500,
	})
	if err != nil {
		v.err = err
		return
	}
	v.err = nil
	v.entries = entries
	if v.row >= len(entries) {
		v.row = max(0, len(entries)-1)
	}
}

func (v *notificationsView) Init() tea.Cmd       { return nil }
func (v *notificationsView) HasOverlay() bool    { return false }
func (v *notificationsView) CloseOverlay()       {}
func (v *notificationsView) Breadcrumb() string  { return "" }
func (v *notificationsView) CapturesInput() bool { return false }

// Status renders the unread-count chip in the shell footer when the
// Notifications tab is active — the TUI analogue of the bell badge.
func (v *notificationsView) Status() string {
	n, err := v.store.CountUnreadNotifications(nil)
	if err != nil || n == 0 {
		return ""
	}
	return fmt.Sprintf("● %d unread", n)
}

func (v *notificationsView) Help() string {
	scope := "unread"
	if !v.unreadOnly {
		scope = "all"
	}
	return fmt.Sprintf("j/k scroll · u toggle [%s] · enter/m mark read · M mark all · r reload · q quit", scope)
}

func (v *notificationsView) Update(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	switch key.String() {
	case "j", "down":
		if v.row < len(v.entries)-1 {
			v.row++
		}
	case "k", "up":
		if v.row > 0 {
			v.row--
		}
	case "g", "home":
		v.row = 0
		v.scroll = 0
	case "G", "end":
		if len(v.entries) > 0 {
			v.row = len(v.entries) - 1
		}
	case "u":
		// Toggle unread-only / all. Reset the cursor so it can't strand
		// past the (possibly shorter/longer) re-filtered list.
		v.unreadOnly = !v.unreadOnly
		v.row = 0
		v.scroll = 0
		v.reload()
	case "enter", "m":
		v.markFocusedRead()
	case "M":
		v.markAllRead()
	case "r":
		v.reload()
	}
	return nil
}

func (v *notificationsView) markFocusedRead() {
	if v.row < 0 || v.row >= len(v.entries) {
		return
	}
	id := v.entries[v.row].ID
	if _, err := v.client.MarkNotificationRead(context.Background(), id, false); err != nil {
		v.err = err
		return
	}
	v.reload()
}

func (v *notificationsView) markAllRead() {
	if _, err := v.client.MarkAllNotificationsRead(context.Background(), nil, false); err != nil {
		v.err = err
		return
	}
	v.reload()
}

func (v *notificationsView) View(width, height int) string {
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

	if v.err != nil {
		return box.Render(errorStyle.Render(v.err.Error()))
	}

	scope := "unread"
	if !v.unreadOnly {
		scope = "all"
	}
	titleBar := lipgloss.NewStyle().
		Bold(true).Foreground(lipgloss.Color("231")).Background(colHeaderFocus).
		Width(innerWidth).Padding(0, 1).
		Render(fmt.Sprintf("Notifications · %d (%s)", len(v.entries), scope))

	if len(v.entries) == 0 {
		empty := "(no unread notifications)"
		if !v.unreadOnly {
			empty = "(no notifications yet)"
		}
		body := mutedStyle.Padding(1, 2).Render(empty)
		return box.Render(lipgloss.JoinVertical(lipgloss.Left, titleBar, body))
	}

	// Reserve rows: title bar (1) + column header (1) + horizontal rule (1).
	rowsHeight := innerHeight - 3
	if rowsHeight < 1 {
		rowsHeight = 1
	}

	// Keep the cursor in view.
	if v.row < v.scroll {
		v.scroll = v.row
	}
	if v.row >= v.scroll+rowsHeight {
		v.scroll = v.row - rowsHeight + 1
	}
	if v.scroll < 0 {
		v.scroll = 0
	}
	end := min(v.scroll+rowsHeight, len(v.entries))

	// Layout: " when │ repo │ state │ body ". Fixed columns are tight; the
	// body column takes the remainder.
	const (
		whenW  = 12
		repoW  = 8
		stateW = 3 // "●" unread marker / blank when read
		sep    = "│"
	)
	cellPad := 2 // 1 left + 1 right
	fixedW := whenW + repoW + stateW + 4*cellPad + 3*lipgloss.Width(sep)
	bodyW := innerWidth - fixedW
	if bodyW < 12 {
		bodyW = 12
	}

	cell := func(text string, w int) string {
		t := truncate(text, w)
		return " " + t + strings.Repeat(" ", w-lipgloss.Width(t)) + " "
	}
	headerCell := func(text string, w int) string {
		t := truncate(text, w)
		return " " + boldStyle.Render(t) + strings.Repeat(" ", w-lipgloss.Width(t)) + " "
	}

	colHeader := headerCell("when", whenW) + sep +
		headerCell("repo", repoW) + sep +
		headerCell("", stateW) + sep +
		headerCell("notification", bodyW)

	rule := mutedStyle.Render(
		strings.Repeat("─", whenW+cellPad) + "┼" +
			strings.Repeat("─", repoW+cellPad) + "┼" +
			strings.Repeat("─", stateW+cellPad) + "┼" +
			strings.Repeat("─", bodyW+cellPad))

	var rows []string
	for i := v.scroll; i < end; i++ {
		e := v.entries[i]
		marker := ""
		if e.ReadAt == nil {
			marker = "●"
		}
		// A linked notification shows its issue key inline with the body so
		// the deep-link target is visible even without click-through.
		bodyText := oneLine(e.Body)
		if e.IssueKey != "" {
			bodyText = e.IssueKey + " " + bodyText
		}
		line := cell(formatRelative(e.CreatedAt), whenW) + sep +
			cell(e.RepoPrefix, repoW) + sep +
			cell(marker, stateW) + sep +
			cell(bodyText, bodyW)

		styled := lipgloss.NewStyle().Width(innerWidth)
		if i == v.row {
			styled = styled.Background(cardSelectedBG).Foreground(lipgloss.Color("231"))
		} else if e.ReadAt != nil {
			styled = styled.Foreground(mutedColor)
		}
		rows = append(rows, styled.Render(line))
	}

	body := strings.Join(rows, "\n")
	return box.Render(lipgloss.JoinVertical(lipgloss.Left, titleBar, colHeader, rule, body))
}
