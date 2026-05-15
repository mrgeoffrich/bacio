//go:build !js

package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// settingsView is the Settings tab: a view over bacio's global app
// settings. v1 covers the dispatch prompt configuration — the same
// per-stage template body + state-gate the desktop Settings panel and
// `bacio settings template` edit. It is structured so a future global
// setting can be added as another section.
//
// Two layers: a stage list (base layout) and a per-stage editor overlay
// with two panes — a textarea for the template body and a chip row for
// the state-gate. The view is store-backed (like every other tab) and
// records its own audit rows via recordTUIOp, mirroring the op names
// the localClient writes for the CLI/desktop paths.
//
// This is the native build. The wasm demo gets a read-only stub
// (settings_wasm.go) because bubbles/textarea has no js/wasm build —
// see settings_common.go for the rationale.
type settingsView struct {
	store *store.Store
	// repo is carried for constructor-signature parity with the other
	// views; prompt settings are global (app_settings) so it is unused
	// by the view's logic.
	repo  *model.Repo
	actor string

	stages []stageRow
	cursor int
	err    error

	// editor sub-state — active while editing is true.
	editing  bool
	editIdx  int
	editPane settingsPane
	ta       textarea.Model
	gateCur  int
	editErr  error
}

type settingsPane int

const (
	paneBody settingsPane = iota
	paneGate
)

func newSettingsView(s *store.Store, repo *model.Repo) *settingsView {
	v := &settingsView{store: s, repo: repo, actor: tuiActor()}
	v.reload()
	return v
}

// reload rebuilds the stage list from the store: effective body +
// state-gate per stage, in model.AllDispatchModes() lifecycle order.
func (s *settingsView) reload() {
	templates, err := s.store.AllPromptTemplates()
	if err != nil {
		s.err = err
		return
	}
	states, err := s.store.AllPromptStates()
	if err != nil {
		s.err = err
		return
	}
	s.err = nil
	s.stages = loadStageRows(templates, states)
	if s.cursor >= len(s.stages) {
		s.cursor = max(0, len(s.stages)-1)
	}
}

// refreshStage re-reads one stage from the store after a save, so the
// chips and the editor's persisted-body baseline stay accurate without
// a full reload.
func (s *settingsView) refreshStage(idx int) {
	if idx < 0 || idx >= len(s.stages) {
		return
	}
	mode := s.stages[idx].mode
	body, err := s.store.GetPromptTemplate(mode)
	if err != nil {
		s.editErr = err
		return
	}
	states, err := s.store.GetPromptStates(mode)
	if err != nil {
		s.editErr = err
		return
	}
	s.stages[idx].body = body
	s.stages[idx].states = states
	s.stages[idx].bodyIsDefault = body == model.DefaultPromptTemplate(mode)
	s.stages[idx].statesDefault = sameStates(states, model.DefaultPromptStates(mode))
}

func (s *settingsView) Init() tea.Cmd  { return nil }
func (s *settingsView) Status() string { return "" }

func (s *settingsView) HasOverlay() bool { return s.editing }

func (s *settingsView) CloseOverlay() {
	s.editing = false
	s.editErr = nil
	s.ta.Blur()
}

// CapturesInput is true only while the editor's body textarea is
// focused — that's the one window where the shell must yield `q` and
// the digit keys so they can be typed into the template. With the
// state-gate pane focused, q/digits behave normally (the editor is an
// overlay, so a digit-switch closes it cleanly via CloseOverlay).
func (s *settingsView) CapturesInput() bool {
	return s.editing && s.editPane == paneBody
}

func (s *settingsView) Breadcrumb() string {
	if s.editing && s.editIdx < len(s.stages) {
		return s.stages[s.editIdx].label
	}
	return ""
}

func (s *settingsView) Help() string {
	if !s.editing {
		return "j/k move · enter edit stage · r reload · q quit"
	}
	if s.editPane == paneBody {
		return "type to edit · ctrl+s save · ctrl+d reset · tab → state-gate · esc close"
	}
	return "h/l move · space toggle (saves) · ctrl+d reset · tab → body · esc close"
}

func (s *settingsView) Update(msg tea.Msg) tea.Cmd {
	if s.editing {
		return s.updateEditor(msg)
	}
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	switch key.String() {
	case "j", "down":
		if s.cursor < len(s.stages)-1 {
			s.cursor++
		}
	case "k", "up":
		if s.cursor > 0 {
			s.cursor--
		}
	case "g", "home":
		s.cursor = 0
	case "G", "end":
		if len(s.stages) > 0 {
			s.cursor = len(s.stages) - 1
		}
	case "enter":
		return s.openEditor(s.cursor)
	case "r":
		s.reload()
	}
	return nil
}

// openEditor opens the per-stage editor overlay on the given stage,
// seeding the textarea with the stage's effective body and focusing
// the body pane. The returned Cmd is the textarea's cursor-blink tick.
func (s *settingsView) openEditor(idx int) tea.Cmd {
	if idx < 0 || idx >= len(s.stages) {
		return nil
	}
	s.editing = true
	s.editIdx = idx
	s.editPane = paneBody
	s.editErr = nil
	s.gateCur = 0

	ta := textarea.New()
	ta.Prompt = ""
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	ta.MaxHeight = 0
	ta.SetValue(s.stages[idx].body)
	s.ta = ta
	return s.ta.Focus()
}

func (s *settingsView) updateEditor(msg tea.Msg) tea.Cmd {
	key, isKey := msg.(tea.KeyMsg)
	if !isKey {
		// Non-key messages (cursor blink) only matter to the textarea.
		if s.editPane == paneBody {
			var cmd tea.Cmd
			s.ta, cmd = s.ta.Update(msg)
			return cmd
		}
		return nil
	}

	// Editor-wide keys, handled before either pane sees them.
	switch key.String() {
	case "esc":
		s.CloseOverlay()
		return nil
	case "tab", "shift+tab":
		return s.togglePane()
	}

	if s.editPane == paneBody {
		// ctrl+s / ctrl+d are intercepted here so they never reach the
		// textarea — ctrl+d is the textarea's delete-forward binding, so
		// forwarding it would eat a character instead of resetting.
		switch key.String() {
		case "ctrl+s":
			s.saveBody()
			return nil
		case "ctrl+d":
			s.resetBody()
			return nil
		}
		var cmd tea.Cmd
		s.ta, cmd = s.ta.Update(msg)
		return cmd
	}

	// State-gate pane.
	switch key.String() {
	case "h", "left":
		if s.gateCur > 0 {
			s.gateCur--
		}
	case "l", "right":
		if s.gateCur < len(model.AllStates())-1 {
			s.gateCur++
		}
	case " ", "space":
		s.toggleState()
	case "ctrl+d":
		s.resetStates()
	}
	return nil
}

// togglePane flips focus between the body textarea and the state-gate
// chips, managing textarea focus (and returning its blink Cmd when the
// body pane gains focus).
func (s *settingsView) togglePane() tea.Cmd {
	if s.editPane == paneBody {
		s.editPane = paneGate
		s.ta.Blur()
		return nil
	}
	s.editPane = paneBody
	return s.ta.Focus()
}

// saveBody persists the textarea's current value as the stage's custom
// template. An empty body clears the override (the store's documented
// "reset" signal) — so saving an empty body and resetting converge.
func (s *settingsView) saveBody() {
	idx := s.editIdx
	mode := s.stages[idx].mode
	body := s.ta.Value()
	if err := s.store.SetPromptTemplate(mode, body); err != nil {
		s.editErr = err
		return
	}
	s.editErr = nil
	op := "prompt_template.update"
	if strings.TrimSpace(body) == "" {
		op = "prompt_template.reset"
	}
	s.recordSettingOp(op, "prompt_template."+string(mode), "stage="+string(mode))
	s.refreshStage(idx)
	// Re-seed the textarea with the resolved value so an empty save
	// shows the built-in default rather than a blank editor.
	s.ta.SetValue(s.stages[idx].body)
}

// resetBody clears the stage's custom template override and re-seeds
// the textarea with the built-in default.
func (s *settingsView) resetBody() {
	idx := s.editIdx
	mode := s.stages[idx].mode
	if err := s.store.SetPromptTemplate(mode, ""); err != nil {
		s.editErr = err
		return
	}
	s.editErr = nil
	s.recordSettingOp("prompt_template.reset", "prompt_template."+string(mode), "stage="+string(mode))
	s.refreshStage(idx)
	s.ta.SetValue(s.stages[idx].body)
}

// toggleState flips the focused state in/out of the stage's state-gate
// and saves immediately — matching the desktop panel, where the gate
// has no separate "save" step. The next set is rebuilt in canonical
// model.AllStates() order so the stored value stays stable.
//
// Note: the store reads an empty gate as "clear the override" — so
// toggling off the last state falls the stage back to its built-in
// default rather than persisting a literally-empty gate. That mirrors
// the store semantics every surface shares; it is not special-cased
// here.
func (s *settingsView) toggleState() {
	idx := s.editIdx
	mode := s.stages[idx].mode
	all := model.AllStates()
	if s.gateCur < 0 || s.gateCur >= len(all) {
		return
	}
	target := all[s.gateCur]
	have := make(map[model.State]bool, len(s.stages[idx].states))
	for _, st := range s.stages[idx].states {
		have[st] = true
	}
	have[target] = !have[target]
	next := make([]model.State, 0, len(all))
	for _, st := range all {
		if have[st] {
			next = append(next, st)
		}
	}
	if err := s.store.SetPromptStates(mode, next); err != nil {
		s.editErr = err
		return
	}
	s.editErr = nil
	s.recordSettingOp("prompt_states.update", "prompt_states."+string(mode), "stage="+string(mode))
	s.refreshStage(idx)
}

// resetStates clears the stage's custom state-gate override, falling
// back to the built-in default.
func (s *settingsView) resetStates() {
	idx := s.editIdx
	mode := s.stages[idx].mode
	if err := s.store.SetPromptStates(mode, nil); err != nil {
		s.editErr = err
		return
	}
	s.editErr = nil
	s.recordSettingOp("prompt_states.reset", "prompt_states."+string(mode), "stage="+string(mode))
	s.refreshStage(idx)
}

// recordSettingOp writes an audit row for a settings mutation, mirroring
// the shape the localClient uses for the CLI/desktop paths: Kind
// "app_setting", no RepoID (these settings are global).
func (s *settingsView) recordSettingOp(op, targetLabel, details string) {
	recordTUIOp(s.store, model.HistoryEntry{
		Actor:       s.actor,
		Op:          op,
		Kind:        "app_setting",
		TargetLabel: targetLabel,
		Details:     details,
	})
}

func (s *settingsView) View(width, height int) string {
	if width == 0 || height == 0 {
		return ""
	}
	if s.editing {
		return s.viewEditor(width, height)
	}
	return renderSettingsList(width, height, s.stages, s.cursor, s.err)
}

func (s *settingsView) viewEditor(width, height int) string {
	innerWidth := width - 2
	if innerWidth < 40 {
		innerWidth = 40
	}
	innerHeight := height - 2
	if innerHeight < 8 {
		innerHeight = 8
	}
	box := lipgloss.NewStyle().
		Border(colBorder).BorderForeground(colFocusBorder).
		Width(innerWidth).Height(innerHeight)

	st := s.stages[s.editIdx]

	// Header: stage label + a modified marker when the draft differs
	// from the persisted body.
	header := boldStyle.Render(st.label + " — prompt template")
	if s.ta.Value() != st.body {
		header += "  " + lipgloss.NewStyle().Foreground(takenColor).Render("● modified")
	}
	headerBar := lipgloss.NewStyle().Width(innerWidth).Padding(0, 1).Render(header)

	// Budget: header(1) + blank(1) + body-label(1) + textarea(N) +
	// blank(1) + gate-label(1) + gate(1) + err(0/1) + hint(1).
	reserved := 7
	if s.editErr != nil {
		reserved++
	}
	taHeight := innerHeight - reserved - 2 // -2 for the box border padding rows
	if taHeight < 3 {
		taHeight = 3
	}
	s.ta.SetWidth(innerWidth - 2)
	s.ta.SetHeight(taHeight)

	bodyLabel := paneLabel("Template body", s.editPane == paneBody)
	gateLabel := paneLabel("Valid from states", s.editPane == paneGate)

	// State-gate chip row.
	have := make(map[model.State]bool, len(st.states))
	for _, x := range st.states {
		have[x] = true
	}
	var chips []string
	for i, state := range model.AllStates() {
		on := have[state]
		label := stateLabel(state)
		cs := lipgloss.NewStyle().Padding(0, 1)
		switch {
		case on:
			cs = cs.Foreground(lipgloss.Color("231")).Background(lipgloss.Color("76"))
		default:
			cs = cs.Foreground(mutedColor)
		}
		if s.editPane == paneGate && i == s.gateCur {
			cs = cs.Underline(true).Bold(true)
		}
		chips = append(chips, cs.Render(label))
	}
	gateRow := lipgloss.NewStyle().Width(innerWidth).Padding(0, 1).
		Render(lipgloss.JoinHorizontal(lipgloss.Top, chips...))

	parts := []string{
		headerBar,
		"",
		bodyLabel,
		lipgloss.NewStyle().Padding(0, 1).Render(s.ta.View()),
		"",
		gateLabel,
		gateRow,
	}
	if s.editErr != nil {
		parts = append(parts, errorStyle.Render(s.editErr.Error()))
	}
	parts = append(parts, mutedStyle.Padding(0, 1).Render("Placeholders: "+placeholderTokens()))

	return box.Render(lipgloss.JoinVertical(lipgloss.Left, parts...))
}

// paneLabel renders an editor pane's heading, accented when focused.
func paneLabel(text string, focused bool) string {
	st := mutedStyle.Padding(0, 1)
	if focused {
		st = lipgloss.NewStyle().Padding(0, 1).Bold(true).Foreground(colFocusBorder)
	}
	return st.Render(text)
}
