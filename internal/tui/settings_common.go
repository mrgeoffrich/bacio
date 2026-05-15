package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// This file holds the parts of the Settings tab that are platform
// neutral — shared by the native editor-capable view (settings.go) and
// the read-only wasm stub (settings_wasm.go). The split exists because
// the native view embeds bubbles/textarea, which transitively imports
// atotto/clipboard, which has no js/wasm build. The wasm demo is
// read-only by design, so a no-editor Settings tab there is correct,
// not a compromise.

// stageRow is one dispatch stage's resolved settings, plus the
// derived "still matches the built-in default" flags the chips render.
type stageRow struct {
	mode          model.DispatchMode
	label         string
	body          string // effective (custom override or built-in default)
	states        []model.State
	bodyIsDefault bool
	statesDefault bool
}

// settingsStageLabels fixes the human label for each dispatch stage,
// mirroring promptTemplateOrder in desktop/settingsservice.go and
// promptTemplateLabels in internal/cli/settings.go.
var settingsStageLabels = map[model.DispatchMode]string{
	model.DispatchModePlan:      "Planning",
	model.DispatchModeImplement: "Implementing",
	model.DispatchModeReview:    "Reviewing",
	model.DispatchModeShip:      "Shipping",
	model.DispatchModeFixReview: "Fixing a review",
}

func settingsStageLabel(mode model.DispatchMode) string {
	if l, ok := settingsStageLabels[mode]; ok {
		return l
	}
	return string(mode)
}

// loadStageRows builds the per-stage settings list from the resolved
// templates + state-gates, in model.AllDispatchModes() lifecycle order.
func loadStageRows(templates map[model.DispatchMode]string, states map[model.DispatchMode][]model.State) []stageRow {
	modes := model.AllDispatchModes()
	rows := make([]stageRow, 0, len(modes))
	for _, mode := range modes {
		rows = append(rows, stageRow{
			mode:          mode,
			label:         settingsStageLabel(mode),
			body:          templates[mode],
			states:        states[mode],
			bodyIsDefault: templates[mode] == model.DefaultPromptTemplate(mode),
			statesDefault: sameStates(states[mode], model.DefaultPromptStates(mode)),
		})
	}
	return rows
}

// chip renders a "[name: default]" / "[name: custom]" status chip.
func chip(name string, isDefault bool) string {
	if isDefault {
		return mutedStyle.Render("[" + name + ": default]")
	}
	return lipgloss.NewStyle().Foreground(takenColor).Render("[" + name + ": custom]")
}

// placeholderTokens renders the interpolable {{token}} list for the
// footer hint, from model.PromptTemplateTokens.
func placeholderTokens() string {
	parts := make([]string, len(model.PromptTemplateTokens))
	for i, t := range model.PromptTemplateTokens {
		parts[i] = "{{" + t + "}}"
	}
	return strings.Join(parts, " ")
}

// renderSettingsList draws the stage-list base layout shared by the
// native and wasm Settings views.
func renderSettingsList(width, height int, stages []stageRow, cursor int, err error) string {
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

	if err != nil {
		return box.Render(errorStyle.Render(err.Error()))
	}

	titleBar := lipgloss.NewStyle().
		Bold(true).Foreground(lipgloss.Color("231")).Background(colHeaderFocus).
		Width(innerWidth).Padding(0, 1).
		Render(fmt.Sprintf("Settings · Prompt templates · %d stages", len(stages)))

	var rows []string
	for i, st := range stages {
		marker := "  "
		if i == cursor {
			marker = "▸ "
		}
		line := fmt.Sprintf("%s%-18s %s  %s", marker, st.label,
			chip("body", st.bodyIsDefault), chip("states", st.statesDefault))
		styled := lipgloss.NewStyle().Width(innerWidth).Padding(0, 1)
		if i == cursor {
			styled = styled.Background(cardSelectedBG).Foreground(lipgloss.Color("231"))
		}
		rows = append(rows, styled.Render(line))
	}

	hint := mutedStyle.Padding(0, 1).Render("Placeholders: " + placeholderTokens())
	body := lipgloss.JoinVertical(lipgloss.Left, rows...)
	content := lipgloss.JoinVertical(lipgloss.Left, titleBar, "", body, "", hint)
	return box.Render(content)
}

// sameStates reports whether two state slices hold the same set,
// order-insensitive — used to derive the "still default" gate chip.
func sameStates(a, b []model.State) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[model.State]bool, len(a))
	for _, s := range a {
		seen[s] = true
	}
	for _, s := range b {
		if !seen[s] {
			return false
		}
	}
	return true
}
