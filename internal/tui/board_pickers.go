package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

func (b *boardView) openPicker() {
	b.picker = true
	// Park the picker cursor on the focused column so toggling it is a
	// single keystroke after opening.
	b.pickerRow = 0
	visible := b.visibleStates()
	if len(visible) == 0 {
		return
	}
	focusedState := visible[b.col]
	for i, st := range b.states {
		if st == focusedState {
			b.pickerRow = i
			return
		}
	}
}

func (b *boardView) updatePicker(key tea.KeyMsg) {
	switch key.String() {
	case "esc":
		b.picker = false
	case "j", "down":
		if b.pickerRow < len(b.states)-1 {
			b.pickerRow++
		}
	case "k", "up":
		if b.pickerRow > 0 {
			b.pickerRow--
		}
	case "g", "home":
		b.pickerRow = 0
	case "G", "end":
		b.pickerRow = len(b.states) - 1
	case " ", "enter":
		st := b.states[b.pickerRow]
		// Don't allow toggling the last visible column off — keeps the
		// board from rendering as empty.
		if !b.hidden[st] && len(b.visibleStates()) <= 1 {
			break
		}
		b.hidden[st] = !b.hidden[st]
		if !b.hidden[st] {
			delete(b.hidden, st)
		}
		b.persistHidden()
		b.clampCol()
		b.refreshSelection()
	case "a":
		// Show all.
		b.hidden = map[model.State]bool{}
		b.persistHidden()
	case "n":
		// Hide all but the first state — refuses to leave the board empty.
		next := map[model.State]bool{}
		for i, st := range b.states {
			if i == 0 {
				continue
			}
			next[st] = true
		}
		b.hidden = next
		b.persistHidden()
		b.clampCol()
		b.refreshSelection()
	}
}

// openFeaturePicker snapshots the set of features in the current
// repo's issues (plus the "(unassigned)" sentinel if any issue lacks
// one) and parks the cursor at the top.
func (b *boardView) openFeaturePicker() {
	b.featurePicker = true
	b.featurePickerRow = 0

	seen := map[string]bool{}
	hasUnassigned := false
	// Re-fetch from the unfiltered list so the picker shows hidden
	// features too (the user needs to be able to un-hide them).
	all, err := b.store.ListIssues(store.IssueFilter{RepoID: &b.repo.ID})
	if err != nil {
		b.err = err
		return
	}
	for _, iss := range all {
		if iss.FeatureSlug == "" {
			hasUnassigned = true
			continue
		}
		seen[iss.FeatureSlug] = true
	}
	slugs := make([]string, 0, len(seen)+1)
	for s := range seen {
		slugs = append(slugs, s)
	}
	sort.Strings(slugs)
	if hasUnassigned {
		slugs = append(slugs, store.HiddenFeaturesUnassigned)
	}
	b.featurePickerSlugs = slugs
}

// persistHiddenFeatures writes the current hidden-features set to disk;
// errors surface in the footer rather than crashing the loop.
func (b *boardView) persistHiddenFeatures() {
	if err := b.store.SaveHiddenFeatures(b.repo.ID, b.hiddenFeatures); err != nil {
		b.err = err
	}
}

// applyFeatureFilter rebuilds b.columns from the store, applying the
// current hidden-features filter. Reuses reload() since that's where
// the filter logic lives.
func (b *boardView) applyFeatureFilter() {
	if err := b.reload(); err != nil {
		b.err = err
	}
	b.clampCol()
	b.refreshSelection()
}

func (b *boardView) updateFeaturePicker(key tea.KeyMsg) {
	n := len(b.featurePickerSlugs)
	switch key.String() {
	case "esc":
		b.featurePicker = false
	case "j", "down":
		if b.featurePickerRow < n-1 {
			b.featurePickerRow++
		}
	case "k", "up":
		if b.featurePickerRow > 0 {
			b.featurePickerRow--
		}
	case "g", "home":
		b.featurePickerRow = 0
	case "G", "end":
		if n > 0 {
			b.featurePickerRow = n - 1
		}
	case " ", "enter":
		if n == 0 {
			break
		}
		slug := b.featurePickerSlugs[b.featurePickerRow]
		if b.hiddenFeatures == nil {
			b.hiddenFeatures = map[string]bool{}
		}
		if b.hiddenFeatures[slug] {
			delete(b.hiddenFeatures, slug)
		} else {
			b.hiddenFeatures[slug] = true
		}
		b.persistHiddenFeatures()
		b.applyFeatureFilter()
	case "a":
		// Show all.
		b.hiddenFeatures = map[string]bool{}
		b.persistHiddenFeatures()
		b.applyFeatureFilter()
	case "n":
		// Hide everything except the cursor row — quick "isolate this
		// feature" shortcut. Cursor row is left visible to avoid leaving
		// the board empty.
		if n == 0 {
			break
		}
		focus := b.featurePickerSlugs[b.featurePickerRow]
		next := map[string]bool{}
		for _, s := range b.featurePickerSlugs {
			if s != focus {
				next[s] = true
			}
		}
		b.hiddenFeatures = next
		b.persistHiddenFeatures()
		b.applyFeatureFilter()
	}
}

// viewPicker renders the column-visibility modal as a centered card listing
// every state with a checkbox and its current issue count.
func (b *boardView) viewPicker(width, height int) string {
	// Sizing: ~40 cols wide, ~3 rows of chrome plus one row per state.
	innerWidth := 40
	if innerWidth > width-6 {
		innerWidth = max(20, width-6)
	}

	header := boldStyle.Render("Visible columns")
	rowStyle := lipgloss.NewStyle().Width(innerWidth).Padding(0, 1)
	selStyle := lipgloss.NewStyle().Width(innerWidth).Padding(0, 1).
		Background(cardSelectedBG).Foreground(lipgloss.Color("231"))

	var rows []string
	rows = append(rows, header, "")
	for i, st := range b.states {
		mark := "[×]"
		if b.hidden[st] {
			mark = "[ ]"
		}
		count := len(b.columns[st])
		label := fmt.Sprintf("%s  %-13s  %d", mark, stateLabel(st), count)
		if i == b.pickerRow {
			rows = append(rows, selStyle.Render(label))
		} else {
			rows = append(rows, rowStyle.Render(label))
		}
	}
	rows = append(rows, "", mutedStyle.Render("space toggle · a all · n minimal · esc close"))

	card := lipgloss.NewStyle().
		Border(colBorder).BorderForeground(colFocusBorder).
		Padding(1, 2).
		Render(strings.Join(rows, "\n"))

	// Centre the card horizontally and vertically inside the available space.
	return lipgloss.NewStyle().
		Width(width).Height(height).
		Align(lipgloss.Center, lipgloss.Center).
		Render(card)
}

// viewFeaturePicker renders the feature-filter modal. Each row shows
// the feature's colour stripe, a checkbox, the slug (or "(unassigned)"
// for the no-feature group), and how many issues currently belong to
// it across all states.
func (b *boardView) viewFeaturePicker(width, height int) string {
	innerWidth := 48
	if innerWidth > width-6 {
		innerWidth = max(24, width-6)
	}

	header := boldStyle.Render("Visible features")
	rowStyle := lipgloss.NewStyle().Width(innerWidth).Padding(0, 1)
	selStyle := lipgloss.NewStyle().Width(innerWidth).Padding(0, 1).
		Background(cardSelectedBG).Foreground(lipgloss.Color("231"))

	// Pre-compute counts across the unfiltered issue set so the picker
	// shows reality, not the post-filter view.
	all, _ := b.store.ListIssues(store.IssueFilter{RepoID: &b.repo.ID})
	counts := map[string]int{}
	for _, iss := range all {
		key := iss.FeatureSlug
		if key == "" {
			key = store.HiddenFeaturesUnassigned
		}
		counts[key]++
	}

	var rows []string
	rows = append(rows, header, "")
	if len(b.featurePickerSlugs) == 0 {
		rows = append(rows, mutedStyle.Italic(true).Render("(no features in this repo)"))
	}
	for i, slug := range b.featurePickerSlugs {
		mark := "[×]"
		if b.hiddenFeatures[slug] {
			mark = "[ ]"
		}
		label := slug
		colourSlug := slug
		if slug == store.HiddenFeaturesUnassigned {
			label = "(unassigned)"
			colourSlug = ""
		}
		stripe := lipgloss.NewStyle().Foreground(featureColor(colourSlug)).Render("▌")
		line := fmt.Sprintf("%s %s  %-22s  %d", stripe, mark, truncate(label, 22), counts[slug])
		if i == b.featurePickerRow {
			rows = append(rows, selStyle.Render(line))
		} else {
			rows = append(rows, rowStyle.Render(line))
		}
	}
	rows = append(rows, "", mutedStyle.Render("space toggle · a all · n isolate · esc close"))

	card := lipgloss.NewStyle().
		Border(colBorder).BorderForeground(colFocusBorder).
		Padding(1, 2).
		Render(strings.Join(rows, "\n"))

	return lipgloss.NewStyle().
		Width(width).Height(height).
		Align(lipgloss.Center, lipgloss.Center).
		Render(card)
}
