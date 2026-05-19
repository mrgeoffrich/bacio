//go:build !js

package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// settingsView is the Settings tab: the user's editable set of dispatch
// prompt templates. Each row is one template (slug + name + body +
// state-gate); the user can add, rename, delete, and restore the
// built-in defaults.
//
// Layers:
//   - Base list (stage list + chips).
//   - Per-template editor overlay (body textarea + state-gate chips).
//   - Add-template overlay (slug + name + body + state-gate).
//   - Rename overlay (slug + name).
//   - Delete confirm + restore-defaults confirm overlays.
//
// The view is store-backed (like every other tab) and records its own
// audit rows via recordTUIOp, mirroring the op names the localClient
// writes (template.create / .delete / .rename / .restore_defaults plus
// the existing prompt_template.update / .reset and
// prompt_states.update / .reset).
//
// This is the native build. The wasm demo gets a read-only stub
// (settings_wasm.go) because bubbles/textarea has no js/wasm build —
// see settings_common.go for the rationale.
type settingsView struct {
	store *store.Store
	// repo is carried for constructor-signature parity with the other
	// views; templates are global (the prompt_templates table is local-
	// only and has no per-repo scope) so it is unused by the view's
	// logic.
	repo  *model.Repo
	actor string

	stages []stageRow
	cursor int
	err    error

	// One of the *Mode flags below is set while an overlay is open.
	editing       bool
	adding        bool
	renaming      bool
	confirmDelete bool
	confirmReset  bool

	// Per-overlay sub-state.
	editIdx          int
	editPane         settingsPane
	ta               textarea.Model
	slugInput        textinput.Model
	nameInput        textinput.Model
	actionLabelInput textinput.Model
	gateStates       map[model.State]bool
	gateCur          int
	addFocus         addField
	editErr          error
}

type settingsPane int

const (
	paneBody settingsPane = iota
	paneGate
)

// addField labels which field has focus on the add-template overlay.
type addField int

const (
	addFieldSlug addField = iota
	addFieldName
	addFieldActionLabel
	addFieldBody
	addFieldGate
)

func newSettingsView(s *store.Store, repo *model.Repo) *settingsView {
	v := &settingsView{store: s, repo: repo, actor: tuiActor()}
	v.reload()
	return v
}

// reload rebuilds the stage list from the store's canonical template
// iteration order.
func (s *settingsView) reload() {
	tmpls, err := s.store.ListPromptTemplates()
	if err != nil {
		s.err = err
		return
	}
	s.err = nil
	s.stages = loadStageRowsFromTemplates(tmpls)
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
	slug := s.stages[idx].slug
	t, err := s.store.GetPromptTemplateBySlug(slug)
	if err != nil {
		s.editErr = err
		return
	}
	label := t.Name
	if label == "" {
		label = t.Slug
	}
	defAction := model.BuiltinTemplateActionLabel(t.Slug)
	s.stages[idx] = stageRow{
		slug:            t.Slug,
		label:           label,
		body:            t.Body,
		actionLabel:     t.ActionLabel,
		states:          append([]model.State(nil), t.AllowedStates...),
		bodyIsDefault:   t.IsBuiltin && t.Body == model.DefaultPromptBodyForBuiltinSlug(t.Slug),
		statesDefault:   t.IsBuiltin && sameStates(t.AllowedStates, model.DefaultPromptStatesForBuiltinSlug(t.Slug)),
		actionIsDefault: t.IsBuiltin && t.ActionLabel == defAction,
		isBuiltin:       t.IsBuiltin,
	}
}

func (s *settingsView) Init() tea.Cmd  { return nil }
func (s *settingsView) Status() string { return "" }

func (s *settingsView) HasOverlay() bool {
	return s.editing || s.adding || s.renaming || s.confirmDelete || s.confirmReset
}

func (s *settingsView) CloseOverlay() {
	s.editing = false
	s.adding = false
	s.renaming = false
	s.confirmDelete = false
	s.confirmReset = false
	s.editErr = nil
	s.ta.Blur()
	s.slugInput.Blur()
	s.nameInput.Blur()
	s.actionLabelInput.Blur()
}

// CapturesInput is true only while a focused textarea / textinput is
// active — those are the windows where the shell must yield `q` and the
// digit keys so they can be typed. Confirmation overlays don't capture
// input (they only watch for y/n/esc).
func (s *settingsView) CapturesInput() bool {
	switch {
	case s.editing && s.editPane == paneBody:
		return true
	case s.adding:
		return s.addFocus == addFieldSlug || s.addFocus == addFieldName ||
			s.addFocus == addFieldActionLabel || s.addFocus == addFieldBody
	case s.renaming:
		return true
	default:
		return false
	}
}

func (s *settingsView) Breadcrumb() string {
	switch {
	case s.editing && s.editIdx < len(s.stages):
		return s.stages[s.editIdx].label
	case s.adding:
		return "New template"
	case s.renaming && s.editIdx < len(s.stages):
		return "Rename " + s.stages[s.editIdx].label
	case s.confirmDelete && s.editIdx < len(s.stages):
		return "Delete " + s.stages[s.editIdx].label + "?"
	case s.confirmReset:
		return "Restore built-in templates?"
	}
	return ""
}

func (s *settingsView) Help() string {
	switch {
	case s.editing && s.editPane == paneBody:
		return "type to edit · ctrl+s save · ctrl+r reset built-in · tab → state-gate · esc close"
	case s.editing:
		return "h/l move · space toggle (saves) · ctrl+r reset built-in · tab → body · esc close"
	case s.adding:
		return "tab cycle fields · ctrl+s save · esc cancel"
	case s.renaming:
		return "tab toggle slug/name · ctrl+s save · esc cancel"
	case s.confirmDelete:
		return "y delete · n cancel · esc cancel"
	case s.confirmReset:
		return "y restore · n cancel · esc cancel"
	}
	return "j/k move · enter edit · a add · r rename · d delete · R restore built-ins · q quit"
}

func (s *settingsView) Update(msg tea.Msg) tea.Cmd {
	if s.HasOverlay() {
		return s.updateOverlay(msg)
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
	case "a":
		return s.openAdd()
	case "r":
		return s.openRename(s.cursor)
	case "d":
		s.openDeleteConfirm(s.cursor)
	case "R":
		s.openRestoreConfirm()
	case "ctrl+r":
		s.reload()
	case "T":
		// BACI-68: toggle display.show_archived from the Settings tab.
		// Same audit shape as the REST + CLI paths (Kind=app_setting,
		// Details=show_archived=<bool>) so `bacio history --kind
		// app_setting` returns a consistent stream regardless of which
		// surface flipped the bit.
		cur, _ := s.store.GetDisplayShowArchived()
		next := !cur
		if err := s.store.SetDisplayShowArchived(next); err != nil {
			s.err = err
			return nil
		}
		s.recordSettingOp("display.update", "display.show_archived",
			fmt.Sprintf("show_archived=%t", next))
	}
	return nil
}

func (s *settingsView) updateOverlay(msg tea.Msg) tea.Cmd {
	switch {
	case s.editing:
		return s.updateEditor(msg)
	case s.adding:
		return s.updateAdd(msg)
	case s.renaming:
		return s.updateRename(msg)
	case s.confirmDelete:
		return s.updateDeleteConfirm(msg)
	case s.confirmReset:
		return s.updateRestoreConfirm(msg)
	}
	return nil
}

// openEditor opens the per-template editor overlay, seeding the
// textarea with the template's current body and focusing the body pane.
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
		if s.editPane == paneBody {
			var cmd tea.Cmd
			s.ta, cmd = s.ta.Update(msg)
			return cmd
		}
		return nil
	}

	switch key.String() {
	case "esc":
		s.CloseOverlay()
		return nil
	case "tab", "shift+tab":
		return s.togglePane()
	}

	if s.editPane == paneBody {
		switch key.String() {
		case "ctrl+s":
			s.saveBody()
			return nil
		case "ctrl+r":
			s.resetBody()
			return nil
		}
		var cmd tea.Cmd
		s.ta, cmd = s.ta.Update(msg)
		return cmd
	}

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
	case "ctrl+r":
		s.resetStates()
	}
	return nil
}

// togglePane flips focus between the body textarea and the state-gate
// chips, managing textarea focus.
func (s *settingsView) togglePane() tea.Cmd {
	if s.editPane == paneBody {
		s.editPane = paneGate
		s.ta.Blur()
		return nil
	}
	s.editPane = paneBody
	return s.ta.Focus()
}

// saveBody persists the textarea's value as the template's body. An
// empty body either reverts a built-in to its embedded default or
// stores an empty body for a user-created template (per the store's
// SetPromptTemplate semantic).
func (s *settingsView) saveBody() {
	idx := s.editIdx
	mode := model.DispatchMode(s.stages[idx].slug)
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
	s.recordSettingOp(op, "prompt_template:"+s.stages[idx].slug, "slug="+s.stages[idx].slug)
	s.refreshStage(idx)
	s.ta.SetValue(s.stages[idx].body)
}

// resetBody only makes sense for built-in templates — it restores the
// embedded default body and state-gate via the store's SetPromptTemplate
// "" semantic. For user templates the empty save would just clear the
// body, which the editor's plain ctrl+s already does.
func (s *settingsView) resetBody() {
	idx := s.editIdx
	if !s.stages[idx].isBuiltin {
		s.editErr = fmt.Errorf("reset is only for built-in templates; user templates have no embedded default")
		return
	}
	mode := model.DispatchMode(s.stages[idx].slug)
	if err := s.store.SetPromptTemplate(mode, ""); err != nil {
		s.editErr = err
		return
	}
	s.editErr = nil
	s.recordSettingOp("prompt_template.reset", "prompt_template:"+s.stages[idx].slug, "slug="+s.stages[idx].slug)
	s.refreshStage(idx)
	s.ta.SetValue(s.stages[idx].body)
}

// toggleState flips the focused state in/out of the template's gate
// and saves immediately, matching the desktop panel.
func (s *settingsView) toggleState() {
	idx := s.editIdx
	mode := model.DispatchMode(s.stages[idx].slug)
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
	s.recordSettingOp("prompt_states.update", "prompt_states:"+s.stages[idx].slug, "slug="+s.stages[idx].slug)
	s.refreshStage(idx)
}

func (s *settingsView) resetStates() {
	idx := s.editIdx
	if !s.stages[idx].isBuiltin {
		s.editErr = fmt.Errorf("reset is only for built-in templates; user templates have no embedded default")
		return
	}
	mode := model.DispatchMode(s.stages[idx].slug)
	if err := s.store.SetPromptStates(mode, nil); err != nil {
		s.editErr = err
		return
	}
	s.editErr = nil
	s.recordSettingOp("prompt_states.reset", "prompt_states:"+s.stages[idx].slug, "slug="+s.stages[idx].slug)
	s.refreshStage(idx)
}

// openAdd opens the new-template overlay with a fresh slug + name +
// body editor. The state-gate defaults to "todo" (the most common
// stage for a new template).
func (s *settingsView) openAdd() tea.Cmd {
	s.adding = true
	s.editErr = nil
	s.addFocus = addFieldSlug
	s.gateCur = 0

	s.slugInput = textinput.New()
	s.slugInput.Placeholder = "slug (kebab- or snake-case)"
	s.slugInput.CharLimit = 60
	s.slugInput.Width = 40

	s.nameInput = textinput.New()
	s.nameInput.Placeholder = "Display name"
	s.nameInput.CharLimit = 80
	s.nameInput.Width = 40

	// BACI-67: optional imperative override rendered on the dispatch
	// action menus. Empty value = "derive from display name" via
	// model.DeriveActionLabel.
	s.actionLabelInput = textinput.New()
	s.actionLabelInput.Placeholder = "Action label (optional; empty = derive from name)"
	s.actionLabelInput.CharLimit = 80
	s.actionLabelInput.Width = 40

	ta := textarea.New()
	ta.Prompt = ""
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	ta.MaxHeight = 0
	s.ta = ta
	s.gateStates = map[model.State]bool{model.StateTodo: true}
	return s.slugInput.Focus()
}

func (s *settingsView) updateAdd(msg tea.Msg) tea.Cmd {
	key, isKey := msg.(tea.KeyMsg)
	if !isKey {
		if s.addFocus == addFieldBody {
			var cmd tea.Cmd
			s.ta, cmd = s.ta.Update(msg)
			return cmd
		}
		return nil
	}
	switch key.String() {
	case "esc":
		s.CloseOverlay()
		return nil
	case "tab":
		return s.cycleAddField(1)
	case "shift+tab":
		return s.cycleAddField(-1)
	case "ctrl+s":
		s.commitAdd()
		return nil
	}
	switch s.addFocus {
	case addFieldSlug:
		var cmd tea.Cmd
		s.slugInput, cmd = s.slugInput.Update(msg)
		return cmd
	case addFieldName:
		var cmd tea.Cmd
		s.nameInput, cmd = s.nameInput.Update(msg)
		return cmd
	case addFieldActionLabel:
		var cmd tea.Cmd
		s.actionLabelInput, cmd = s.actionLabelInput.Update(msg)
		return cmd
	case addFieldBody:
		var cmd tea.Cmd
		s.ta, cmd = s.ta.Update(msg)
		return cmd
	case addFieldGate:
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
			all := model.AllStates()
			if s.gateCur < len(all) {
				target := all[s.gateCur]
				s.gateStates[target] = !s.gateStates[target]
			}
		}
	}
	return nil
}

func (s *settingsView) cycleAddField(delta int) tea.Cmd {
	fields := []addField{addFieldSlug, addFieldName, addFieldActionLabel, addFieldBody, addFieldGate}
	var idx int
	for i, f := range fields {
		if f == s.addFocus {
			idx = i
			break
		}
	}
	idx = (idx + delta + len(fields)) % len(fields)
	s.addFocus = fields[idx]
	s.slugInput.Blur()
	s.nameInput.Blur()
	s.actionLabelInput.Blur()
	s.ta.Blur()
	switch s.addFocus {
	case addFieldSlug:
		return s.slugInput.Focus()
	case addFieldName:
		return s.nameInput.Focus()
	case addFieldActionLabel:
		return s.actionLabelInput.Focus()
	case addFieldBody:
		return s.ta.Focus()
	}
	return nil
}

func (s *settingsView) commitAdd() {
	all := model.AllStates()
	states := make([]model.State, 0, len(s.gateStates))
	for _, st := range all {
		if s.gateStates[st] {
			states = append(states, st)
		}
	}
	in := store.AddPromptTemplateIn{
		Slug:          s.slugInput.Value(),
		Name:          s.nameInput.Value(),
		Body:          s.ta.Value(),
		AllowedStates: states,
		ActionLabel:   s.actionLabelInput.Value(),
	}
	t, err := s.store.AddPromptTemplate(in)
	if err != nil {
		s.editErr = err
		return
	}
	s.editErr = nil
	id := t.ID
	recordTUIOp(s.store, model.HistoryEntry{
		Actor:       s.actor,
		Op:          "template.create",
		Kind:        "app_setting",
		TargetID:    &id,
		TargetLabel: "prompt_template:" + t.Slug,
		Details:     fmt.Sprintf("slug=%s, name=%s", t.Slug, t.Name),
	})
	s.CloseOverlay()
	s.reload()
}

// openRename opens the rename overlay seeded with the current slug +
// name. Either may be edited; an unchanged save errors at the store.
func (s *settingsView) openRename(idx int) tea.Cmd {
	if idx < 0 || idx >= len(s.stages) {
		return nil
	}
	s.renaming = true
	s.editIdx = idx
	s.editErr = nil

	s.slugInput = textinput.New()
	s.slugInput.SetValue(s.stages[idx].slug)
	s.slugInput.CharLimit = 60
	s.slugInput.Width = 40

	s.nameInput = textinput.New()
	s.nameInput.SetValue(s.stages[idx].label)
	s.nameInput.CharLimit = 80
	s.nameInput.Width = 40

	s.addFocus = addFieldSlug
	return s.slugInput.Focus()
}

func (s *settingsView) updateRename(msg tea.Msg) tea.Cmd {
	key, isKey := msg.(tea.KeyMsg)
	if !isKey {
		return nil
	}
	switch key.String() {
	case "esc":
		s.CloseOverlay()
		return nil
	case "tab", "shift+tab":
		if s.addFocus == addFieldSlug {
			s.addFocus = addFieldName
			s.slugInput.Blur()
			return s.nameInput.Focus()
		}
		s.addFocus = addFieldSlug
		s.nameInput.Blur()
		return s.slugInput.Focus()
	case "ctrl+s":
		s.commitRename()
		return nil
	}
	if s.addFocus == addFieldSlug {
		var cmd tea.Cmd
		s.slugInput, cmd = s.slugInput.Update(msg)
		return cmd
	}
	var cmd tea.Cmd
	s.nameInput, cmd = s.nameInput.Update(msg)
	return cmd
}

func (s *settingsView) commitRename() {
	idx := s.editIdx
	old := s.stages[idx]
	t, err := s.store.RenamePromptTemplate(store.RenamePromptTemplateIn{
		OldSlug: old.slug,
		NewSlug: s.slugInput.Value(),
		NewName: s.nameInput.Value(),
	})
	if err != nil {
		s.editErr = err
		return
	}
	id := t.ID
	recordTUIOp(s.store, model.HistoryEntry{
		Actor:       s.actor,
		Op:          "template.rename",
		Kind:        "app_setting",
		TargetID:    &id,
		TargetLabel: "prompt_template:" + t.Slug,
		Details:     fmt.Sprintf("from=%s, to=%s, name=%s", old.slug, t.Slug, t.Name),
	})
	s.CloseOverlay()
	s.reload()
}

func (s *settingsView) openDeleteConfirm(idx int) {
	if idx < 0 || idx >= len(s.stages) {
		return
	}
	s.confirmDelete = true
	s.editIdx = idx
	s.editErr = nil
}

func (s *settingsView) updateDeleteConfirm(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	switch key.String() {
	case "y", "Y", "enter":
		idx := s.editIdx
		slug := s.stages[idx].slug
		removed, err := s.store.DeletePromptTemplate(slug)
		if err != nil {
			s.editErr = err
			return nil
		}
		id := removed.ID
		recordTUIOp(s.store, model.HistoryEntry{
			Actor:       s.actor,
			Op:          "template.delete",
			Kind:        "app_setting",
			TargetID:    &id,
			TargetLabel: "prompt_template:" + slug,
			Details:     "slug=" + slug,
		})
		s.CloseOverlay()
		s.reload()
	case "n", "N", "esc":
		s.CloseOverlay()
	}
	return nil
}

func (s *settingsView) openRestoreConfirm() {
	s.confirmReset = true
	s.editErr = nil
}

func (s *settingsView) updateRestoreConfirm(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	switch key.String() {
	case "y", "Y", "enter":
		created, err := s.store.RestoreBuiltinPromptTemplates()
		if err != nil {
			s.editErr = err
			return nil
		}
		if len(created) > 0 {
			recordTUIOp(s.store, model.HistoryEntry{
				Actor:       s.actor,
				Op:          "template.restore_defaults",
				Kind:        "app_setting",
				TargetLabel: "prompt_template",
				Details:     "slugs=" + strings.Join(created, ","),
			})
		}
		s.CloseOverlay()
		s.reload()
	case "n", "N", "esc":
		s.CloseOverlay()
	}
	return nil
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
	switch {
	case s.editing:
		return s.viewEditor(width, height)
	case s.adding:
		return s.viewAdd(width, height)
	case s.renaming:
		return s.viewRename(width, height)
	case s.confirmDelete:
		return s.viewConfirm(width, height,
			fmt.Sprintf("Delete template %q?", s.stages[s.editIdx].slug),
			"y to delete · n to cancel · esc to cancel")
	case s.confirmReset:
		return s.viewConfirm(width, height,
			"Restore missing built-in templates from the embedded defaults?",
			"y to restore · n to cancel · esc to cancel")
	}
	showArchived, _ := s.store.GetDisplayShowArchived()
	return renderSettingsList(width, height, s.stages, s.cursor, s.err, showArchived)
}

func (s *settingsView) viewEditor(width, height int) string {
	innerWidth, innerHeight, box := settingsBox(width, height)
	st := s.stages[s.editIdx]

	header := boldStyle.Render(st.label + " — prompt template")
	if s.ta.Value() != st.body {
		header += "  " + lipgloss.NewStyle().Foreground(takenColor).Render("● modified")
	}
	if st.isBuiltin {
		header += "  " + mutedStyle.Render("(built-in)")
	}
	headerBar := lipgloss.NewStyle().Width(innerWidth).Padding(0, 1).Render(header)

	reserved := 7
	if s.editErr != nil {
		reserved++
	}
	taHeight := innerHeight - reserved - 2
	if taHeight < 3 {
		taHeight = 3
	}
	s.ta.SetWidth(innerWidth - 2)
	s.ta.SetHeight(taHeight)

	bodyLabel := paneLabel("Template body", s.editPane == paneBody)
	gateLabel := paneLabel("Valid from states", s.editPane == paneGate)

	gateRow := renderStateChips(innerWidth, st.states, s.gateCur, s.editPane == paneGate)

	parts := []string{
		headerBar, "",
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

func (s *settingsView) viewAdd(width, height int) string {
	innerWidth, innerHeight, box := settingsBox(width, height)

	headerBar := lipgloss.NewStyle().Width(innerWidth).Padding(0, 1).
		Render(boldStyle.Render("New template"))

	reserved := 11
	if s.editErr != nil {
		reserved++
	}
	taHeight := innerHeight - reserved - 2
	if taHeight < 3 {
		taHeight = 3
	}
	s.ta.SetWidth(innerWidth - 2)
	s.ta.SetHeight(taHeight)
	s.slugInput.Width = innerWidth - 6
	s.nameInput.Width = innerWidth - 6
	s.actionLabelInput.Width = innerWidth - 6

	on := make([]model.State, 0)
	all := model.AllStates()
	for _, st := range all {
		if s.gateStates[st] {
			on = append(on, st)
		}
	}

	parts := []string{
		headerBar, "",
		paneLabel("Slug", s.addFocus == addFieldSlug),
		lipgloss.NewStyle().Padding(0, 1).Render(s.slugInput.View()),
		paneLabel("Name (gerund — used by activity pill)", s.addFocus == addFieldName),
		lipgloss.NewStyle().Padding(0, 1).Render(s.nameInput.View()),
		paneLabel("Action label (imperative — dispatch button text; empty = derive from name)", s.addFocus == addFieldActionLabel),
		lipgloss.NewStyle().Padding(0, 1).Render(s.actionLabelInput.View()),
		paneLabel("Body", s.addFocus == addFieldBody),
		lipgloss.NewStyle().Padding(0, 1).Render(s.ta.View()),
		paneLabel("Valid from states", s.addFocus == addFieldGate),
		renderStateChips(innerWidth, on, s.gateCur, s.addFocus == addFieldGate),
	}
	if s.editErr != nil {
		parts = append(parts, errorStyle.Render(s.editErr.Error()))
	}
	return box.Render(lipgloss.JoinVertical(lipgloss.Left, parts...))
}

func (s *settingsView) viewRename(width, height int) string {
	innerWidth, _, box := settingsBox(width, height)
	s.slugInput.Width = innerWidth - 6
	s.nameInput.Width = innerWidth - 6
	headerBar := lipgloss.NewStyle().Width(innerWidth).Padding(0, 1).
		Render(boldStyle.Render("Rename " + s.stages[s.editIdx].label))
	parts := []string{
		headerBar, "",
		paneLabel("Slug", s.addFocus == addFieldSlug),
		lipgloss.NewStyle().Padding(0, 1).Render(s.slugInput.View()),
		paneLabel("Name", s.addFocus == addFieldName),
		lipgloss.NewStyle().Padding(0, 1).Render(s.nameInput.View()),
	}
	if s.editErr != nil {
		parts = append(parts, errorStyle.Render(s.editErr.Error()))
	}
	return box.Render(lipgloss.JoinVertical(lipgloss.Left, parts...))
}

func (s *settingsView) viewConfirm(width, height int, question, hint string) string {
	innerWidth, _, box := settingsBox(width, height)
	parts := []string{
		lipgloss.NewStyle().Width(innerWidth).Padding(0, 1).Render(boldStyle.Render(question)),
		"",
		lipgloss.NewStyle().Padding(0, 1).Render(mutedStyle.Render(hint)),
	}
	if s.editErr != nil {
		parts = append(parts, "", errorStyle.Render(s.editErr.Error()))
	}
	return box.Render(lipgloss.JoinVertical(lipgloss.Left, parts...))
}

// settingsBox is the shared lipgloss frame for every Settings overlay.
func settingsBox(width, height int) (int, int, lipgloss.Style) {
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
	return innerWidth, innerHeight, box
}

// renderStateChips renders the row of state chips, with the focused
// chip underlined when the gate pane has focus.
func renderStateChips(innerWidth int, on []model.State, cur int, focused bool) string {
	onSet := make(map[model.State]bool, len(on))
	for _, st := range on {
		onSet[st] = true
	}
	var chips []string
	for i, state := range model.AllStates() {
		label := stateLabel(state)
		cs := lipgloss.NewStyle().Padding(0, 1)
		switch {
		case onSet[state]:
			cs = cs.Foreground(lipgloss.Color("231")).Background(lipgloss.Color("76"))
		default:
			cs = cs.Foreground(mutedColor)
		}
		if focused && i == cur {
			cs = cs.Underline(true).Bold(true)
		}
		chips = append(chips, cs.Render(label))
	}
	return lipgloss.NewStyle().Width(innerWidth).Padding(0, 1).
		Render(lipgloss.JoinHorizontal(lipgloss.Top, chips...))
}

// paneLabel renders an editor pane's heading, accented when focused.
func paneLabel(text string, focused bool) string {
	st := mutedStyle.Padding(0, 1)
	if focused {
		st = lipgloss.NewStyle().Padding(0, 1).Bold(true).Foreground(colFocusBorder)
	}
	return st.Render(text)
}
