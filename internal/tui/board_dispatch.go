package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// dispatchModeChoices is the step-1 list: the label shown and the mode
// it maps to, plus a one-line description of what that mode tells the
// agent to do.
var dispatchModeChoices = []struct {
	Label string
	Mode  model.DispatchMode
	Desc  string
}{
	{"Plan", model.DispatchModePlan, "produce an implementation plan, don't write code"},
	{"Implement", model.DispatchModeImplement, "build the issue end-to-end"},
}

// maxDispatchNote bounds the free-form note typed in the picker. The
// store caps the composed payload at 8 KiB; this leaves generous room.
const maxDispatchNote = 1000

// openDispatchPicker starts the "send to agent" flow for the focused
// card. It only acts on todo issues — the keybind is otherwise a no-op
// with a one-line footer hint.
func (b *boardView) openDispatchPicker() {
	iss := b.currentIssue()
	if iss == nil {
		return
	}
	if iss.State != model.StateTodo {
		b.err = fmt.Errorf("send to agent: only todo issues can be dispatched")
		return
	}
	sessions, err := b.store.ListAgentSessions(store.AgentSessionFilter{
		RepoID:    &b.repo.ID,
		OnlyAlive: true,
	})
	if err != nil {
		b.err = err
		return
	}
	b.dispatchIssue = iss
	b.dispatchSessions = sessions
	b.dispatchPicker = true
	b.dispatchStep = 0
	b.dispatchRow = 0
	b.dispatchAgentRow = 0
	b.dispatchMode = ""
	b.dispatchNote = ""
}

// updateDispatchPicker drives the three-step picker: agent -> mode ->
// optional note. esc closes the whole picker from any step.
func (b *boardView) updateDispatchPicker(key tea.KeyMsg) {
	if key.Type == tea.KeyEsc {
		b.dispatchPicker = false
		return
	}
	switch b.dispatchStep {
	case 0:
		b.updateDispatchAgentStep(key)
	case 1:
		b.updateDispatchModeStep(key)
	case 2:
		b.updateDispatchNoteStep(key)
	}
}

func (b *boardView) updateDispatchAgentStep(key tea.KeyMsg) {
	n := len(b.dispatchSessions)
	switch key.String() {
	case "j", "down":
		if b.dispatchRow < n-1 {
			b.dispatchRow++
		}
	case "k", "up":
		if b.dispatchRow > 0 {
			b.dispatchRow--
		}
	case "g", "home":
		b.dispatchRow = 0
	case "G", "end":
		if n > 0 {
			b.dispatchRow = n - 1
		}
	case "enter", " ":
		if n == 0 {
			return
		}
		b.dispatchAgentRow = b.dispatchRow
		b.dispatchStep = 1
		b.dispatchRow = 0
	}
}

func (b *boardView) updateDispatchModeStep(key tea.KeyMsg) {
	switch key.String() {
	case "j", "down":
		if b.dispatchRow < len(dispatchModeChoices)-1 {
			b.dispatchRow++
		}
	case "k", "up":
		if b.dispatchRow > 0 {
			b.dispatchRow--
		}
	case "h", "left", "shift+tab":
		b.dispatchStep = 0
		b.dispatchRow = b.dispatchAgentRow
	case "enter", " ":
		b.dispatchMode = dispatchModeChoices[b.dispatchRow].Mode
		b.dispatchStep = 2
		b.dispatchRow = 0
	}
}

// updateDispatchNoteStep captures a single-line free-form note. Printable
// runes append; backspace trims; enter confirms (an empty note is fine);
// shift+tab steps back. `h`/`left` are NOT "back" here — they're text.
func (b *boardView) updateDispatchNoteStep(key tea.KeyMsg) {
	switch key.Type {
	case tea.KeyEnter:
		b.confirmDispatch()
		b.dispatchPicker = false
	case tea.KeyShiftTab:
		b.dispatchStep = 1
		b.dispatchRow = 0
	case tea.KeyBackspace:
		if r := []rune(b.dispatchNote); len(r) > 0 {
			b.dispatchNote = string(r[:len(r)-1])
		}
	case tea.KeySpace:
		if len(b.dispatchNote) < maxDispatchNote {
			b.dispatchNote += " "
		}
	case tea.KeyRunes:
		if len(b.dispatchNote) < maxDispatchNote {
			b.dispatchNote += string(key.Runes)
		}
	}
}

// confirmDispatch writes the dispatch and records the audit row. Errors
// surface in the footer rather than crashing the loop.
func (b *boardView) confirmDispatch() {
	if b.dispatchIssue == nil || b.dispatchAgentRow >= len(b.dispatchSessions) {
		return
	}
	sess := b.dispatchSessions[b.dispatchAgentRow]
	issueID := b.dispatchIssue.ID
	d, err := b.store.AddDispatch(store.AddDispatchIn{
		RepoID: b.repo.ID,
		// Pass both targets: the session id is always reliable, and
		// AgentID may be nil for pre-identity sessions.
		TargetAgentID:   sess.AgentID,
		TargetSessionID: sess.SessionID,
		IssueID:         &issueID,
		Mode:            b.dispatchMode,
		Payload:         model.ComposeDispatchPayload(b.dispatchMode, b.dispatchNote),
		CreatedBy:       b.actor,
	})
	if err != nil {
		b.err = err
		return
	}
	b.err = nil
	recordTUIOp(b.store, model.HistoryEntry{
		RepoID:      &b.repo.ID,
		RepoPrefix:  b.repo.Prefix,
		Actor:       b.actor,
		Op:          "agent.dispatch",
		Kind:        "agent",
		TargetID:    &d.ID,
		TargetLabel: dispatchTargetLabelForBoard(sess),
		Details:     fmt.Sprintf("issue=%s mode=%s", b.dispatchIssue.Key, dashIfEmpty(string(b.dispatchMode))),
	})
}

// dispatchTargetLabelForBoard is the audit-row label for a dispatch
// target: the agent identity slug if there is one, else the session id.
func dispatchTargetLabelForBoard(s *model.AgentSession) string {
	if s.AgentName != "" {
		return s.AgentName
	}
	return s.SessionID
}

func (b *boardView) dispatchPickerHelp() string {
	switch b.dispatchStep {
	case 0:
		return "j/k move · enter pick agent · esc cancel"
	case 1:
		return "j/k move · enter pick mode · h back · esc cancel"
	default:
		return "type a note (optional) · enter send · shift+tab back · esc cancel"
	}
}

// viewDispatchPicker renders the three-step picker as a centered card,
// following the column/feature picker layout.
func (b *boardView) viewDispatchPicker(width, height int) string {
	innerWidth := 52
	if innerWidth > width-6 {
		innerWidth = max(28, width-6)
	}
	rowStyle := lipgloss.NewStyle().Width(innerWidth).Padding(0, 1)
	selStyle := lipgloss.NewStyle().Width(innerWidth).Padding(0, 1).
		Background(cardSelectedBG).Foreground(lipgloss.Color("231"))

	issueKey := ""
	if b.dispatchIssue != nil {
		issueKey = b.dispatchIssue.Key
	}

	var rows []string
	switch b.dispatchStep {
	case 0:
		rows = append(rows, boldStyle.Render("Send "+issueKey+" → pick an agent"), "")
		if len(b.dispatchSessions) == 0 {
			rows = append(rows, mutedStyle.Italic(true).Render("(no live agent sessions — esc to close)"))
		}
		for i, s := range b.dispatchSessions {
			label := fmt.Sprintf("%-22s %-16s %s",
				truncate(agentLabel(s), 22),
				truncate(dashIfEmpty(s.Model), 16),
				truncate(dashIfEmpty(s.Branch), 12))
			if i == b.dispatchRow {
				rows = append(rows, selStyle.Render(label))
			} else {
				rows = append(rows, rowStyle.Render(label))
			}
		}
	case 1:
		agent := ""
		if b.dispatchAgentRow < len(b.dispatchSessions) {
			agent = agentLabel(b.dispatchSessions[b.dispatchAgentRow])
		}
		rows = append(rows, boldStyle.Render("Send "+issueKey+" to "+agent+" → pick a mode"), "")
		for i, c := range dispatchModeChoices {
			label := fmt.Sprintf("%-12s %s", c.Label, mutedStyle.Render(c.Desc))
			if i == b.dispatchRow {
				label = fmt.Sprintf("%-12s %s", c.Label, c.Desc)
				rows = append(rows, selStyle.Render(label))
			} else {
				rows = append(rows, rowStyle.Render(label))
			}
		}
	default:
		rows = append(rows, boldStyle.Render("Send "+issueKey+" ("+string(b.dispatchMode)+") → add a note"), "")
		rows = append(rows, mutedStyle.Render("Optional — the agent already gets the issue and the mode instruction."))
		rows = append(rows, "")
		note := b.dispatchNote + "▌"
		rows = append(rows, rowStyle.Render(truncate("> "+note, innerWidth-2)))
	}
	rows = append(rows, "", mutedStyle.Render(b.dispatchPickerHelp()))

	card := lipgloss.NewStyle().
		Border(colBorder).BorderForeground(colFocusBorder).
		Padding(1, 2).
		Render(strings.Join(rows, "\n"))

	return lipgloss.NewStyle().
		Width(width).Height(height).
		Align(lipgloss.Center, lipgloss.Center).
		Render(card)
}
