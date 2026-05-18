package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/model"
)

// questionOverlay is the bubbletea modal for answering a BACI-53
// `ask_user_question` row from inside the TUI. The agents view and
// board view each embed one; opening it stashes the focused row,
// rendering it gates on `open`, and Submit / Cancel flow through the
// supplied client so the audit log and auto-state-flip in
// localClient stay authoritative for both surfaces.
//
// Form model: each question in the payload is one fieldset with its
// own option cursor. `j/k` walks options within the focused
// question; `tab` / `shift+tab` jumps between questions. `space`
// toggles the focused option — for single-select questions the
// toggle replaces the prior pick; for multi-select it xor's the set.
// `enter` submits all selections; `d` dismisses (cancels) the row;
// `esc` closes the overlay without writing.
type questionOverlay struct {
	client     client.Client
	open       bool
	row        *model.SessionQuestion
	item       int                  // focused question index
	option     int                  // focused option within current question
	sel        []map[int]bool       // one selection set per question
	err        string
	submitting bool
}

// newQuestionOverlay wires the overlay against a borrowed client.
// The host view typically constructs one client up-front and reuses
// it for the program's lifetime — the overlay does no caching.
func newQuestionOverlay(c client.Client) *questionOverlay {
	return &questionOverlay{client: c}
}

// reset seeds the overlay against a freshly-loaded question row and
// pops `open` true so the host view starts rendering it.
func (q *questionOverlay) reset(row *model.SessionQuestion) {
	q.open = true
	q.row = row
	q.item = 0
	q.option = 0
	q.sel = make([]map[int]bool, len(row.Payload.Questions))
	for i := range q.sel {
		q.sel[i] = map[int]bool{}
	}
	q.err = ""
	q.submitting = false
}

// close hides the overlay and drops the loaded row so a stale
// payload can't leak into the next open.
func (q *questionOverlay) close() {
	q.open = false
	q.row = nil
	q.sel = nil
	q.err = ""
	q.submitting = false
}

func (q *questionOverlay) currentItem() *model.QuestionItem {
	if q.row == nil || q.item >= len(q.row.Payload.Questions) {
		return nil
	}
	return &q.row.Payload.Questions[q.item]
}

// update handles one keystroke. Returns a tea.Cmd for any work that
// should run off the UI thread (submit / cancel) so the bubbletea
// loop stays responsive while the round-trip is in flight.
func (q *questionOverlay) update(key tea.KeyMsg) tea.Cmd {
	if !q.open || q.row == nil || q.submitting {
		return nil
	}
	item := q.currentItem()
	if item == nil {
		return nil
	}
	switch key.String() {
	case "esc":
		q.close()
	case "tab", "right", "l":
		if q.item < len(q.row.Payload.Questions)-1 {
			q.item++
			q.option = 0
		}
	case "shift+tab", "left", "h":
		if q.item > 0 {
			q.item--
			q.option = 0
		}
	case "j", "down":
		if q.option < len(item.Options)-1 {
			q.option++
		}
	case "k", "up":
		if q.option > 0 {
			q.option--
		}
	case " ", "x":
		if item.MultiSelect {
			if q.sel[q.item][q.option] {
				delete(q.sel[q.item], q.option)
			} else {
				q.sel[q.item][q.option] = true
			}
		} else {
			q.sel[q.item] = map[int]bool{q.option: true}
		}
	case "enter":
		q.submitting = true
		return q.submitCmd
	case "d":
		q.submitting = true
		return q.cancelCmd
	}
	return nil
}

// submitCmd packs selections into a QuestionAnswers map and POSTs
// them via the client. Wrapped as a tea.Cmd so the bubbletea loop
// keeps ticking while the request is in flight.
func (q *questionOverlay) submitCmd() tea.Msg {
	answers := model.QuestionAnswers{}
	for i, item := range q.row.Payload.Questions {
		var picked []string
		for optIdx := range q.sel[i] {
			if optIdx < len(item.Options) {
				picked = append(picked, item.Options[optIdx].Label)
			}
		}
		if len(picked) == 0 {
			continue
		}
		if item.MultiSelect {
			answers[item.Question] = picked
		} else {
			answers[item.Question] = picked[0]
		}
	}
	_, err := q.client.AnswerSessionQuestion(context.Background(), q.row.ID, answers, false)
	return questionResultMsg{err: err}
}

func (q *questionOverlay) cancelCmd() tea.Msg {
	_, err := q.client.CancelSessionQuestion(context.Background(), q.row.ID, false)
	return questionResultMsg{err: err}
}

// questionResultMsg carries the outcome of a submit or cancel back
// to the host view's Update loop. A nil err means the overlay
// closed cleanly; the host should reload its card data so the
// "? N" badge clears.
type questionResultMsg struct {
	err error
}

// handleResult lets the host view feed a questionResultMsg back
// through the overlay so the in-flight flag clears and (on success)
// the overlay hides itself.
func (q *questionOverlay) handleResult(m questionResultMsg) {
	q.submitting = false
	if m.err != nil {
		q.err = m.err.Error()
		return
	}
	q.close()
}

// view renders the overlay centred in the viewport. Mirrors the
// board_pickers.viewPicker frame so it visually slots in with the
// rest of the TUI's modals.
func (q *questionOverlay) view(width, height int) string {
	if !q.open || q.row == nil {
		return ""
	}
	innerWidth := 64
	if innerWidth > width-6 {
		innerWidth = max(36, width-6)
	}

	rows := []string{}
	title := fmt.Sprintf("Agent question %d/%d", q.item+1, len(q.row.Payload.Questions))
	if q.row.IssueKey != "" {
		title += "  " + q.row.IssueKey
	}
	rows = append(rows, boldStyle.Render(title), "")

	if q.err != "" {
		rows = append(rows, errorStyle.Render("error: "+q.err), "")
	}
	if q.submitting {
		rows = append(rows, mutedStyle.Render("submitting…"), "")
	}

	item := q.currentItem()
	if item != nil {
		header := ""
		if item.Header != "" {
			header = "[" + item.Header + "] "
		}
		rows = append(rows,
			lipgloss.NewStyle().Width(innerWidth).Render(header+item.Question),
			"")
		kind := "(single-select — space picks, replaces prior)"
		if item.MultiSelect {
			kind = "(multi-select — space toggles)"
		}
		rows = append(rows, mutedStyle.Render(kind))

		baseRow := lipgloss.NewStyle().Width(innerWidth).Padding(0, 1)
		selRow := baseRow.Background(cardSelectedBG).Foreground(lipgloss.Color("231"))
		for optIdx, opt := range item.Options {
			mark := "[ ]"
			if q.sel[q.item][optIdx] {
				mark = "[×]"
			}
			line := fmt.Sprintf("%s %s", mark, opt.Label)
			if opt.Description != "" {
				line += "  " + mutedStyle.Render(opt.Description)
			}
			if optIdx == q.option {
				rows = append(rows, selRow.Render(line))
			} else {
				rows = append(rows, baseRow.Render(line))
			}
		}

		// BACI-53: if the focused option carries a preview, render it
		// as a bordered monospace block beneath the option list. The
		// TUI stacks rather than going side-by-side (limited card
		// width); native renders side-by-side, but the supervisor
		// still gets the same content keyed off the cursor.
		if q.option < len(item.Options) {
			if pv := strings.TrimRight(item.Options[q.option].Preview, "\n"); pv != "" {
				rows = append(rows, "",
					mutedStyle.Render("preview · "+item.Options[q.option].Label))
				previewBox := lipgloss.NewStyle().
					Border(colBorder).BorderForeground(lipgloss.Color("240")).
					Padding(0, 1).Width(innerWidth - 2).
					Render(pv)
				rows = append(rows, previewBox)
			}
		}
	}

	help := "j/k move · space toggle · tab next q · enter submit · d dismiss · esc close"
	rows = append(rows, "", mutedStyle.Render(help))

	card := lipgloss.NewStyle().
		Border(colBorder).BorderForeground(colFocusBorder).
		Padding(1, 2).
		Render(strings.Join(rows, "\n"))

	return lipgloss.NewStyle().
		Width(width).Height(height).
		Align(lipgloss.Center, lipgloss.Center).
		Render(card)
}
