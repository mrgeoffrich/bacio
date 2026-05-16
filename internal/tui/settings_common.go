package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
	"github.com/mrgeoffrich/bacio/internal/version"
)

// This file holds the parts of the Settings tab that are platform
// neutral — shared by the native editor-capable view (settings.go) and
// the read-only wasm stub (settings_wasm.go). The split exists because
// the native view embeds bubbles/textarea, which transitively imports
// atotto/clipboard, which has no js/wasm build. The wasm demo is
// read-only by design, so a no-editor Settings tab there is correct,
// not a compromise.

// stageRow is one template's resolved settings, plus the derived
// "still matches the built-in default" flags the chips render. For
// user-created (non-built-in) templates the "default" flags are always
// false — there's nothing to compare against.
type stageRow struct {
	slug          string
	label         string
	body          string
	states        []model.State
	bodyIsDefault bool
	statesDefault bool
	isBuiltin     bool
}

// loadStageRowsFromTemplates builds the per-template Settings list from
// the canonical store iteration order. This is the new shape that
// supports arbitrary templates; the older loadStageRows entry point
// from a slug→body / slug→states pair of maps is gone — every caller
// uses store.ListPromptTemplates directly.
func loadStageRowsFromTemplates(templates []*store.PromptTemplate) []stageRow {
	rows := make([]stageRow, 0, len(templates))
	for _, t := range templates {
		label := t.Name
		if label == "" {
			label = t.Slug
		}
		rows = append(rows, stageRow{
			slug:          t.Slug,
			label:         label,
			body:          t.Body,
			states:        append([]model.State(nil), t.AllowedStates...),
			bodyIsDefault: t.IsBuiltin && t.Body == model.DefaultPromptBodyForBuiltinSlug(t.Slug),
			statesDefault: t.IsBuiltin && sameStates(t.AllowedStates, model.DefaultPromptStatesForBuiltinSlug(t.Slug)),
			isBuiltin:     t.IsBuiltin,
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

	// Footer carries the running binary's version so you can cross-check
	// what the TUI client is running against the BACIO column on the
	// Agents tab — easy way to spot "is this agent's channel an older
	// build than my TUI?".
	hint := mutedStyle.Padding(0, 1).Render("Placeholders: " + placeholderTokens())
	versionLine := mutedStyle.Padding(0, 1).Render("Bacio version: " + version.String())
	body := lipgloss.JoinVertical(lipgloss.Left, rows...)
	content := lipgloss.JoinVertical(lipgloss.Left, titleBar, "", body, "", hint, versionLine)
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
