package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// dispatchModeChoice is one row of the mode-picker step: the label
// shown, the slug stored, and the body so the picker preview can show
// what would be sent. Built today from the store's prompt_templates
// table; BACI-252 removed the per-state filter so every non-reserved
// template appears regardless of the focused issue's state. BACI-67:
// the picker renders the imperative Action ("Plan", "Design", …)
// instead of the gerund Label ("Planning", "Designing", …) on the
// dropdown row — Label stays for backwards-compat / legacy consumers
// (none in the TUI today, but keeps the struct shape stable for
// tests).
type dispatchModeChoice struct {
	Label  string
	Action string
	Mode   model.DispatchMode
	Desc   string
}

// maxDispatchNote bounds the free-form note typed in the picker. The
// store caps the composed payload at 8 KiB; this leaves generous room.
const maxDispatchNote = 1000

// openDispatchPicker starts the "send to agent" flow for the focused
// card. The mode-picker list lists every non-reserved template; if
// none are configured the keybind is a no-op with a footer hint.
func (b *boardView) openDispatchPicker() {
	iss := b.currentIssue()
	if iss == nil {
		return
	}
	if b.takenIssues[iss.ID] {
		b.err = fmt.Errorf("send to agent: %s is taken — an agent already holds it", iss.Key)
		return
	}
	if b.waitingIssues[iss.ID] {
		b.err = fmt.Errorf("send to agent: %s is already waiting for an agent to claim it", iss.Key)
		return
	}
	modes, err := allDispatchModes(b.store)
	if err != nil {
		b.err = err
		return
	}
	if len(modes) == 0 {
		b.err = fmt.Errorf("send to agent: no dispatch templates configured (add one in Settings)")
		return
	}
	allSessions, err := b.store.ListAgentSessions(store.AgentSessionFilter{
		RepoID:         &b.repo.ID,
		OnlyAlive:      true,
		RegisteredOnly: true, // stubs can't receive dispatches — no agent identity yet
	})
	if err != nil {
		b.err = err
		return
	}
	// Only offer sessions that have a live bacio channel — sessions
	// without one are interactive (the user hasn't granted channel
	// permission) and can't receive push dispatches.
	sessions := make([]*model.AgentSession, 0, len(allSessions))
	for _, s := range allSessions {
		if s.ChannelSeenAt != nil {
			sessions = append(sessions, s)
		}
	}
	// A busy session (one holding an open claim) isn't a valid dispatch
	// target — pull the repo's open claims once and mark each session.
	openClaims, err := b.store.OpenClaimsBySession(b.repo.ID)
	if err != nil {
		b.err = err
		return
	}
	busy := make([]string, len(sessions))
	for i, s := range sessions {
		if isBusy, issueKey := model.SessionBusy(openClaims[s.ID]); isBusy {
			busy[i] = issueKey
		}
	}
	b.dispatchIssue = iss
	b.dispatchSessions = sessions
	b.dispatchBusy = busy
	b.dispatchModes = modes
	b.dispatchPicker = true
	b.dispatchStep = 0
	b.dispatchRow = b.firstSelectableDispatchRow()
	b.dispatchAgentRow = 0
	b.dispatchMode = ""
	b.dispatchNote = ""
}

// allDispatchModes returns every non-reserved prompt template the
// user can dispatch the focused issue against. BACI-252: no per-state
// filtering — the user is offered every configured mode. Reserved
// slugs (the leading-underscore convention; `_dispatch_preamble`
// today) are filtered out so they don't appear in the picker.
func allDispatchModes(s *store.Store) ([]dispatchModeChoice, error) {
	tmpls, err := s.ListPromptTemplates()
	if err != nil {
		return nil, err
	}
	out := make([]dispatchModeChoice, 0, len(tmpls))
	for _, t := range tmpls {
		if strings.HasPrefix(t.Slug, "_") {
			continue
		}
		label := t.Name
		if label == "" {
			label = t.Slug
		}
		// BACI-67: prefer the imperative action_label override, fall
		// back to the gerund→imperative derivation on Name, and only
		// surface the raw Name if neither produced anything sensible.
		action := strings.TrimSpace(t.ActionLabel)
		if action == "" {
			action = model.DeriveActionLabel(label)
		}
		if action == "" {
			action = label
		}
		desc := strings.TrimSpace(t.Body)
		if i := strings.IndexAny(desc, "\r\n"); i > 0 {
			desc = desc[:i]
		}
		if len(desc) > 72 {
			desc = desc[:69] + "…"
		}
		out = append(out, dispatchModeChoice{
			Label:  label,
			Action: action,
			Mode:   model.DispatchMode(t.Slug),
			Desc:   desc,
		})
	}
	return out, nil
}

// firstSelectableDispatchRow returns the index of the first non-busy
// session, or 0 when every session is busy (the picker then refuses
// enter and shows the "all agents busy" message).
func (b *boardView) firstSelectableDispatchRow() int {
	for i, reason := range b.dispatchBusy {
		if reason == "" {
			return i
		}
	}
	return 0
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
		for r := b.dispatchRow + 1; r < n; r++ {
			if !b.dispatchRowBusy(r) {
				b.dispatchRow = r
				break
			}
		}
	case "k", "up":
		for r := b.dispatchRow - 1; r >= 0; r-- {
			if !b.dispatchRowBusy(r) {
				b.dispatchRow = r
				break
			}
		}
	case "g", "home":
		b.dispatchRow = b.firstSelectableDispatchRow()
	case "G", "end":
		for r := n - 1; r >= 0; r-- {
			if !b.dispatchRowBusy(r) {
				b.dispatchRow = r
				break
			}
		}
	case "enter", " ":
		if n == 0 || b.dispatchRowBusy(b.dispatchRow) {
			return
		}
		b.dispatchAgentRow = b.dispatchRow
		b.dispatchStep = 1
		b.dispatchRow = 0
	}
}

// dispatchRowBusy reports whether the session at row r is busy (and so
// not a valid dispatch target).
func (b *boardView) dispatchRowBusy(r int) bool {
	return r >= 0 && r < len(b.dispatchBusy) && b.dispatchBusy[r] != ""
}

func (b *boardView) updateDispatchModeStep(key tea.KeyMsg) {
	switch key.String() {
	case "j", "down":
		if b.dispatchRow < len(b.dispatchModes)-1 {
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
		if b.dispatchRow < 0 || b.dispatchRow >= len(b.dispatchModes) {
			return
		}
		b.dispatchMode = b.dispatchModes[b.dispatchRow].Mode
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
	// Defensive guard: the chosen session may have gone busy while the
	// user was navigating the mode/note steps. Re-check before writing.
	if b.dispatchRowBusy(b.dispatchAgentRow) {
		sess := b.dispatchSessions[b.dispatchAgentRow]
		b.err = fmt.Errorf("send to agent: %s is now busy (working %s) — pick another", agentLabel(sess), b.dispatchBusy[b.dispatchAgentRow])
		return
	}
	sess := b.dispatchSessions[b.dispatchAgentRow]
	issueID := b.dispatchIssue.ID
	// BACI-76: the dispatch payload is the rewritten preamble plus a
	// tiny stub (ticket / mode / subagent type) — the per-mode brief is
	// the subagent's system prompt now (`bacio install-agent`). Same
	// shape the client's CreateDispatch produces for the desktop path.
	preamble, err := b.store.GetDispatchPreamble()
	if err != nil {
		b.err = err
		return
	}
	// BACI-226: resolve <base_branch> for the stub so the worker sees
	// the right PR target in its Task prompt. Mirrors the client's
	// CreateDispatch path; same fallback to "main" via ResolveBaseBranch.
	var feat *model.Feature
	if b.dispatchIssue.FeatureID != nil {
		feat, err = b.store.GetFeatureByID(*b.dispatchIssue.FeatureID)
		if err != nil {
			b.err = err
			return
		}
	}
	baseBranch := model.ResolveBaseBranch(b.dispatchIssue, feat)
	stub := model.DispatchStub{IssueKey: b.dispatchIssue.Key, Mode: string(b.dispatchMode), BaseBranch: baseBranch}
	if b.dispatchMode != "" {
		stub.SubagentType = model.SubagentTypeForTemplate(string(b.dispatchMode))
	}
	payload := model.ComposeDispatchPayload(preamble, stub, b.dispatchNote)
	d, err := b.store.AddDispatch(store.AddDispatchIn{
		RepoID: b.repo.ID,
		// Pass both targets: the session id is always reliable, and
		// AgentID may be nil for pre-identity sessions.
		TargetAgentID:   sess.AgentID,
		TargetSessionID: sess.SessionID,
		IssueID:         &issueID,
		Mode:            b.dispatchMode,
		Payload:         payload,
		CreatedBy:       b.actor,
	})
	if err != nil {
		b.err = err
		return
	}
	b.err = nil
	// AddDispatch flipped waiting_for_claim on the issue row; reflect it
	// in the board's local set immediately so the spinner shows without
	// waiting for the next ~10s reload. The spinner tick-chain is armed
	// by the caller (Update's dispatch-picker branch).
	b.waitingIssues[issueID] = true
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
		} else if b.firstSelectableDispatchRow() == 0 && b.dispatchRowBusy(0) {
			rows = append(rows, mutedStyle.Italic(true).Render("(all agents are busy — no available dispatch targets)"))
			rows = append(rows, "")
		}
		for i, s := range b.dispatchSessions {
			label := fmt.Sprintf("%-22s %-16s %s",
				truncate(agentLabel(s), 22),
				truncate(dashIfEmpty(s.Model), 16),
				truncate(dashIfEmpty(s.Branch), 12))
			switch {
			case b.dispatchRowBusy(i):
				busyLabel := fmt.Sprintf("%-22s busy · working %s",
					truncate(agentLabel(s), 22), b.dispatchBusy[i])
				rows = append(rows, rowStyle.Render(mutedStyle.Render(busyLabel)))
			case i == b.dispatchRow:
				rows = append(rows, selStyle.Render(label))
			default:
				rows = append(rows, rowStyle.Render(label))
			}
		}
	case 1:
		agent := ""
		if b.dispatchAgentRow < len(b.dispatchSessions) {
			agent = agentLabel(b.dispatchSessions[b.dispatchAgentRow])
		}
		rows = append(rows, boldStyle.Render("Send "+issueKey+" to "+agent+" → pick a template"), "")
		if len(b.dispatchModes) == 0 {
			rows = append(rows, mutedStyle.Italic(true).Render("(no dispatch templates configured — esc to close)"))
		}
		for i, c := range b.dispatchModes {
			// BACI-67: render the imperative Action ("Plan", "Design")
			// instead of the gerund Label ("Planning", "Designing")
			// so the picker row reads as a call to action.
			verb := c.Action
			if verb == "" {
				verb = c.Label
			}
			label := fmt.Sprintf("%-16s %s", truncate(verb, 16), mutedStyle.Render(c.Desc))
			if i == b.dispatchRow {
				label = fmt.Sprintf("%-16s %s", truncate(verb, 16), c.Desc)
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
