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
// false — there's nothing to compare against. actionLabel +
// actionIsDefault track the BACI-67 imperative override; an empty
// actionLabel means the UI derives one from the gerund Name via
// model.DeriveActionLabel.
type stageRow struct {
	slug            string
	label           string
	body            string
	actionLabel     string
	bodyIsDefault   bool
	actionIsDefault bool
	isBuiltin       bool
}

// loadStageRowsFromTemplates builds the per-template Settings list from
// the canonical store iteration order. This is the new shape that
// supports arbitrary templates; the older loadStageRows entry point
// from a slug→body pair of maps is gone — every caller uses
// store.ListPromptTemplates directly.
func loadStageRowsFromTemplates(templates []*store.PromptTemplate) []stageRow {
	rows := make([]stageRow, 0, len(templates))
	for _, t := range templates {
		label := t.Name
		if label == "" {
			label = t.Slug
		}
		defAction := model.BuiltinTemplateActionLabel(t.Slug)
		rows = append(rows, stageRow{
			slug:            t.Slug,
			label:           label,
			body:            t.Body,
			actionLabel:     t.ActionLabel,
			bodyIsDefault:   t.IsBuiltin && t.Body == model.DefaultPromptBodyForBuiltinSlug(t.Slug),
			actionIsDefault: t.IsBuiltin && t.ActionLabel == defAction,
			isBuiltin:       t.IsBuiltin,
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
// native and wasm Settings views. showArchived is the BACI-68 global
// display toggle, surfaced as a one-line row above the templates so
// the user can see — and (on the native build) toggle — it from the
// same tab. defaultFeatureSlug is the BACI-235 per-repo default-feature
// setting; empty = unset (the legacy default). Only rendered when the
// view has a repo context (the wasm read-only stub passes "" + "").
func renderSettingsList(width, height int, stages []stageRow, cursor int, err error, showArchived bool, defaultFeatureSlug, defaultFeatureTitle string) string {
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

	// BACI-68: one-line Display preferences row at the top of the
	// list. Toggle (`T` on native) flips display.show_archived. The
	// `[x]` checkbox keeps the wasm read-only stub legible too.
	checkbox := "[ ]"
	if showArchived {
		checkbox = "[x]"
	}
	displayRow := lipgloss.NewStyle().Width(innerWidth).Padding(0, 1).
		Render(checkbox + " Show archived items  " +
			mutedStyle.Render("(T to toggle — archived issues, docs, features remain hidden by default)"))

	// BACI-235: one-line Default feature row, sibling of the show-archived
	// row. Empty slug = unset (the legacy "featureless creates"
	// behaviour). `D` on native cycles through the repo's features;
	// `X` clears.
	defaultLabel := "(none — featureless creates)"
	if defaultFeatureSlug != "" {
		if defaultFeatureTitle != "" {
			defaultLabel = defaultFeatureTitle + "  " + mutedStyle.Render("("+defaultFeatureSlug+")")
		} else {
			defaultLabel = defaultFeatureSlug
		}
	}
	defaultRow := lipgloss.NewStyle().Width(innerWidth).Padding(0, 1).
		Render("Default feature: " + defaultLabel + "  " +
			mutedStyle.Render("(D cycles · X clears — auto-applied to new issues without an explicit feature)"))

	var rows []string
	for i, st := range stages {
		marker := "  "
		if i == cursor {
			marker = "▸ "
		}
		// BACI-67: render the resolved action label (override or
		// derived from name) inline with the row so the user can see
		// what verb the dispatch picker shows for this template at a
		// glance. Built-ins that still match the embedded imperative
		// render as "default"; everything else as "custom".
		action := st.actionLabel
		if action == "" {
			action = model.DeriveActionLabel(st.label)
		}
		if action == "" {
			action = st.label
		}
		line := fmt.Sprintf("%s%-18s %s  %s · %s",
			marker, st.label,
			chip("body", st.bodyIsDefault),
			chip("action", st.actionIsDefault || (st.actionLabel == "" && !st.isBuiltin)),
			mutedStyle.Render(action))
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
	content := lipgloss.JoinVertical(lipgloss.Left, titleBar, "", displayRow, defaultRow, "", body, "", hint, versionLine)
	return box.Render(content)
}

