package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// featuresView is a two-pane feature browser: slugs/titles on the left,
// description + the issues belonging to the focused feature on the right.
// Enter opens a fullscreen overlay with the full description and a list of
// every linked issue.
type featuresView struct {
	store *store.Store
	repo  *model.Repo

	features   []*model.Feature
	row        int
	listScroll int

	selected    *model.Feature
	issues      []*model.Issue
	issuesErr   error
	// comments holds the BACI-124 chronological-handoff timeline for the
	// focused feature; read-only display in the TUI.
	comments    []*model.FeatureComment
	commentsErr error

	overlay       bool
	overlayScroll int

	mdCache mdCache // see internal/tui/markdown.go

	err error
}

func (f *featuresView) cachedMD(id int64, src string, width int) string {
	return f.mdCache.get(id, src, width)
}

// featureStateLabel returns the human-readable label for a
// model.FeatureState (BACI-199). Falls back to "Active" for the empty
// string so a pre-BACI-199 row (or a forward-compat new state we
// don't recognise) still renders something coherent.
func featureStateLabel(s model.FeatureState) string {
	switch s {
	case model.FeatureStateDone:
		return "Done"
	case model.FeatureStateCancelled:
		return "Cancelled"
	case model.FeatureStateActive, "":
		return "Active"
	}
	return string(s)
}

func newFeaturesView(s *store.Store, repo *model.Repo) *featuresView {
	f := &featuresView{store: s, repo: repo}
	f.reload()
	f.refreshSelection()
	return f
}

func (f *featuresView) reload() {
	includeArchived, _ := f.store.GetDisplayShowArchived()
	list, err := f.store.ListFeaturesFiltered(store.FeatureFilter{
		RepoID:             f.repo.ID,
		IncludeDescription: true,
		IncludeArchived:    includeArchived,
	})
	if err != nil {
		f.err = err
		return
	}
	f.err = nil
	f.mdCache.reset()
	f.features = list
	if f.row >= len(list) {
		f.row = max(0, len(list)-1)
	}
}

func (f *featuresView) currentFeature() *model.Feature {
	if len(f.features) == 0 {
		return nil
	}
	if f.row >= len(f.features) {
		f.row = len(f.features) - 1
	}
	if f.row < 0 {
		f.row = 0
	}
	return f.features[f.row]
}

// refreshSelection re-fetches the issue list only when the focused feature
// changes — keeps navigation snappy without holding a wider cache.
func (f *featuresView) refreshSelection() {
	cur := f.currentFeature()
	if cur == nil {
		f.selected = nil
		f.issues = nil
		f.issuesErr = nil
		return
	}
	if f.selected != nil && f.selected.ID == cur.ID {
		return
	}
	f.selected = cur
	issues, err := f.store.ListIssues(store.IssueFilter{
		RepoID:    &f.repo.ID,
		FeatureID: &cur.ID,
	})
	f.issues = issues
	f.issuesErr = err
	comments, cerr := f.store.ListFeatureComments(cur.ID)
	f.comments = comments
	f.commentsErr = cerr
	f.overlayScroll = 0
}

func (f *featuresView) Init() tea.Cmd    { return nil }
func (f *featuresView) Status() string   { return "" }
func (f *featuresView) HasOverlay() bool { return f.overlay }

func (f *featuresView) CloseOverlay() { f.overlay = false }

func (f *featuresView) CapturesInput() bool { return false }

func (f *featuresView) Breadcrumb() string {
	if f.overlay && f.selected != nil {
		return "[" + f.selected.Slug + "]"
	}
	return ""
}

func (f *featuresView) Help() string {
	if f.overlay {
		return "j/k scroll · g/G top/bottom · esc close"
	}
	return "j/k epics · enter open · a archive · A unarchive · h handoffs · r reload · q quit"
}

func (f *featuresView) Update(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	if f.overlay {
		switch key.String() {
		case "esc", "enter":
			f.overlay = false
			f.overlayScroll = 0
		case "j", "down":
			f.overlayScroll++
		case "k", "up":
			if f.overlayScroll > 0 {
				f.overlayScroll--
			}
		case "g", "home":
			f.overlayScroll = 0
		case "G", "end":
			f.overlayScroll = 1 << 30
		case "pgdown", " ":
			f.overlayScroll += 10
		case "pgup":
			f.overlayScroll -= 10
			if f.overlayScroll < 0 {
				f.overlayScroll = 0
			}
		}
		return nil
	}
	switch key.String() {
	case "j", "down":
		if f.row < len(f.features)-1 {
			f.row++
		}
	case "k", "up":
		if f.row > 0 {
			f.row--
		}
	case "g", "home":
		f.row = 0
	case "G", "end":
		if len(f.features) > 0 {
			f.row = len(f.features) - 1
		}
	case "r":
		f.reload()
	case "enter":
		if f.selected != nil {
			f.overlay = true
			f.overlayScroll = 0
		}
	case "a":
		// BACI-68: archive the focused feature. No confirm — auto-
		// sweep already does this when every child issue is archived.
		// Manual control is a quick way to hide a feature whose
		// children have just been bulk-archived from the kanban.
		if cur := f.currentFeature(); cur != nil && cur.ArchivedAt == nil {
			f.setArchived(cur, true)
		}
	case "A":
		if cur := f.currentFeature(); cur != nil && cur.ArchivedAt != nil {
			f.setArchived(cur, false)
		}
	case "h":
		// BACI-333: toggle whether worker close-outs collect handoff
		// comments on the focused feature. Flip OFF for standing buckets
		// (`bugs`, `maintenance`) so their unrelated children don't pile
		// up handoff noise; ON (the default) keeps the cohesive-feature
		// behaviour.
		if cur := f.currentFeature(); cur != nil {
			f.setCollectHandoffs(cur, !cur.CollectHandoffs)
		}
	}
	f.refreshSelection()
	return nil
}

func (f *featuresView) setArchived(feat *model.Feature, archived bool) {
	if err := f.store.SetFeatureArchived(feat.ID, archived); err != nil {
		f.err = err
		return
	}
	op := "feature.archive"
	if !archived {
		op = "feature.unarchive"
	}
	recordTUIOp(f.store, model.HistoryEntry{
		Actor:       tuiActor(),
		RepoID:      &feat.RepoID,
		RepoPrefix:  f.repo.Prefix,
		Op:          op,
		Kind:        "feature",
		TargetID:    &feat.ID,
		TargetLabel: feat.Slug,
	})
	f.reload()
}

// setCollectHandoffs (BACI-333) flips the focused feature's
// collect_handoffs column and records a feature.handoffs audit row.
// Mirrors setArchived's shape; enabled maps straight to the column.
func (f *featuresView) setCollectHandoffs(feat *model.Feature, enabled bool) {
	if err := f.store.SetFeatureCollectHandoffs(feat.ID, enabled); err != nil {
		f.err = err
		return
	}
	from, to := "on", "off"
	if enabled {
		from, to = "off", "on"
	}
	recordTUIOp(f.store, model.HistoryEntry{
		Actor:       tuiActor(),
		RepoID:      &feat.RepoID,
		RepoPrefix:  f.repo.Prefix,
		Op:          "feature.handoffs",
		Kind:        "feature",
		TargetID:    &feat.ID,
		TargetLabel: feat.Slug,
		Details:     fmt.Sprintf("%s → %s", from, to),
	})
	f.reload()
}

func (f *featuresView) View(width, height int) string {
	if width == 0 || height == 0 {
		return ""
	}
	if f.overlay {
		return f.viewOverlay(width, height)
	}

	listW := width / 3
	if listW < 28 {
		listW = 28
	}
	if listW > 48 {
		listW = 48
	}
	if listW > width-20 {
		listW = max(20, width-20)
	}
	detailW := width - listW

	return lipgloss.JoinHorizontal(lipgloss.Top,
		f.renderList(listW, height),
		f.renderDetail(detailW, height),
	)
}

func (f *featuresView) renderList(width, height int) string {
	innerWidth := width - 2
	if innerWidth < 6 {
		innerWidth = 6
	}
	innerHeight := height - 2
	if innerHeight < 2 {
		innerHeight = 2
	}

	header := lipgloss.NewStyle().
		Bold(true).Foreground(lipgloss.Color("231")).Background(colHeaderFocus).
		Width(innerWidth).Align(lipgloss.Center).
		Render(fmt.Sprintf("Epics · %d", len(f.features)))

	// Body width reserves 1 col for the scrollbar that scrollableBlock pins
	// to the right edge.
	bodyContentWidth := innerWidth - 1
	if bodyContentWidth < 4 {
		bodyContentWidth = 4
	}
	rowStyle := lipgloss.NewStyle().Width(bodyContentWidth).Padding(0, 1)
	selStyle := lipgloss.NewStyle().Width(bodyContentWidth).Padding(0, 1).
		Background(cardSelectedBG).Foreground(lipgloss.Color("231"))

	var lines []string
	if len(f.features) == 0 {
		lines = append(lines, mutedStyle.Padding(1, 1).Render("— no epics —"))
	}
	// Each feature row spans 3 lines: slug, then up to 2 wrapped title
	// lines. The same selection background paints all three so a focused
	// feature reads as a single chunk.
	const featureRows = 3
	titleW := bodyContentWidth - 2
	if titleW < 4 {
		titleW = 4
	}
	cursorStart, cursorEnd := -1, -1
	for i, feat := range f.features {
		isSel := i == f.row
		styler := rowStyle
		// When selected we render the slug as plain text so lipgloss paints
		// the row uniformly — same nested-style fix as the board cards.
		bracketed := "[" + feat.Slug + "]"
		slugRender := keyStyle.Render(bracketed)
		if isSel {
			styler = selStyle
			slugRender = bracketed
			cursorStart = len(lines)
		}
		// BACI-68: archived feature → faint so it reads as background
		// context once the toggle's on.
		if feat.ArchivedAt != nil {
			styler = styler.Faint(true)
		}
		titleLines := wrapLines(feat.Title, titleW, 2)
		// BACI-199: append the state chip to the slug line so the column
		// is scannable at a glance. The chip reads the same enum the
		// CLI / API use; pre-BACI-199 features default to active.
		stateChip := mutedStyle.Render(" · " + featureStateLabel(feat.State))
		// BACI-199: non-active feature → faded so the visual treatment
		// matches the archived rendering (already applied above). Done
		// or cancelled rows still read as "background" relative to
		// in-flight work.
		if feat.State != "" && feat.State != model.FeatureStateActive {
			styler = styler.Faint(true)
		}
		for j := 0; j < featureRows; j++ {
			var content string
			switch {
			case j == 0:
				content = slugRender + stateChip
			case j-1 < len(titleLines):
				content = titleLines[j-1]
			default:
				content = ""
			}
			lines = append(lines, styler.Render(content))
		}
		if isSel {
			cursorEnd = len(lines)
		}
	}

	body := strings.Join(lines, "\n")
	bodyHeight := innerHeight - 1 // header takes 1 row
	if bodyHeight < 1 {
		bodyHeight = 1
	}
	// Keep the selected feature visible by nudging the scroll offset before
	// handing it to scrollableBlock (which clamps against total body height).
	if cursorStart >= 0 {
		if cursorStart < f.listScroll {
			f.listScroll = cursorStart
		}
		if cursorEnd > f.listScroll+bodyHeight {
			f.listScroll = cursorEnd - bodyHeight
		}
		if f.listScroll < 0 {
			f.listScroll = 0
		}
	}

	scrolled := scrollableBlock(innerWidth, bodyHeight, body, &f.listScroll, true)
	content := lipgloss.JoinVertical(lipgloss.Left, header, scrolled)
	return lipgloss.NewStyle().
		Border(colBorder).BorderForeground(colFocusBorder).
		Width(innerWidth).Height(innerHeight).
		Render(content)
}

func (f *featuresView) renderDetail(width, height int) string {
	innerWidth := width - 4
	if innerWidth < 10 {
		innerWidth = 10
	}
	innerHeight := height - 2
	if innerHeight < 3 {
		innerHeight = 3
	}

	box := lipgloss.NewStyle().
		Border(colBorder).BorderForeground(colBorderColor).
		Width(width-2).Height(height-2).Padding(0, 1)

	if f.err != nil {
		return box.Render(errorStyle.Render(f.err.Error()))
	}
	if f.selected == nil {
		return box.Foreground(mutedColor).Render("No epic selected.")
	}

	feat := f.selected
	// BACI-199: prefix the title with the state chip so the detail
	// header echoes the list row. Truncate the title separately to
	// leave room for the chip.
	stateChipPlain := featureStateLabel(feat.State)
	stateChip := mutedStyle.Render(stateChipPlain + " · ")
	titleBudget := innerWidth - len(stateChipPlain) - 3
	if titleBudget < 4 {
		titleBudget = 4
	}
	title := stateChip + boldStyle.Render(truncate(feat.Title, titleBudget))
	// BACI-231: splice `branch=<ref>` between [slug] and created/updated
	// when the feature has an integration branch set, so a glance at the
	// detail pane shows where work ships. mutedStyle carries the colour;
	// truncate clamps the whole meta string to innerWidth.
	metaParts := []string{fmt.Sprintf("[%s]", feat.Slug)}
	if feat.BranchName != nil && *feat.BranchName != "" {
		metaParts = append(metaParts, fmt.Sprintf("branch=%s", *feat.BranchName))
	}
	// BACI-333: surface the collect-handoffs opt-out only when OFF —
	// same "show the non-default" splice as branch above, keeping the
	// common (ON) detail pane uncluttered.
	if !feat.CollectHandoffs {
		metaParts = append(metaParts, "handoffs=off")
	}
	metaParts = append(metaParts,
		fmt.Sprintf("created %s", feat.CreatedAt.Format("2006-01-02")),
		fmt.Sprintf("updated %s", feat.UpdatedAt.Format("2006-01-02")),
	)
	meta := mutedStyle.Render(truncate(strings.Join(metaParts, " · "), innerWidth))

	// Split the right pane between description and the issues table.
	descRows := innerHeight / 3
	if descRows < 3 {
		descRows = 3
	}
	if descRows > 8 {
		descRows = 8
	}

	var desc string
	if feat.Description == "" {
		desc = mutedStyle.Italic(true).Render("(no description)")
	} else {
		desc = f.cachedMD(feat.ID, feat.Description, innerWidth)
		desc = clipLines(desc, descRows)
	}

	issuesHeader := boldStyle.Render(fmt.Sprintf("Issues · %d", len(f.issues)))
	// Reserve rows: title (1) + meta (1) + blank + desc (descRows) + blank +
	// header (1) + hint (1).
	issuesRows := innerHeight - descRows - 6
	if issuesRows < 1 {
		issuesRows = 1
	}

	var issueLines []string
	switch {
	case f.issuesErr != nil:
		issueLines = append(issueLines, errorStyle.Render(f.issuesErr.Error()))
	case len(f.issues) == 0:
		issueLines = append(issueLines, mutedStyle.Italic(true).Render("(no issues yet)"))
	default:
		for _, iss := range f.issues {
			bracketed := "[" + iss.Key + "]"
			line := keyStyle.Render(bracketed) + "  " +
				mutedStyle.Render(fmt.Sprintf("%-12s", stateLabel(iss.State))) + "  " +
				truncate(iss.Title, innerWidth-len(bracketed)-16)
			issueLines = append(issueLines, line)
		}
	}
	issuesBlock := clipLines(strings.Join(issueLines, "\n"), issuesRows)
	hint := mutedStyle.Italic(true).Render("(enter for full view)")

	content := lipgloss.JoinVertical(lipgloss.Left,
		title,
		meta,
		"",
		desc,
		"",
		issuesHeader,
		issuesBlock,
		"",
		hint,
	)
	return box.Render(content)
}

func (f *featuresView) viewOverlay(width, height int) string {
	if f.selected == nil {
		return lipgloss.NewStyle().
			Border(colBorder).BorderForeground(colFocusBorder).
			Width(width-2).Height(height-2).Padding(1, 2).
			Render("No epic selected.")
	}

	contentWidth := width - 7
	if contentWidth < 10 {
		contentWidth = 10
	}

	feat := f.selected
	// BACI-199: prefix the overlay title with the state chip, same
	// rendering as the inline detail pane.
	stateChip := mutedStyle.Render(featureStateLabel(feat.State) + " · ")
	title := stateChip + boldStyle.Render(feat.Title)
	// BACI-231: same `branch=<ref>` splice as renderDetail above.
	// viewOverlay skips the truncate wrap since the overlay is sized
	// for the full meta string.
	overlayMetaParts := []string{fmt.Sprintf("[%s]", feat.Slug)}
	if feat.BranchName != nil && *feat.BranchName != "" {
		overlayMetaParts = append(overlayMetaParts, fmt.Sprintf("branch=%s", *feat.BranchName))
	}
	// BACI-333: same OFF-only handoffs splice as renderDetail.
	if !feat.CollectHandoffs {
		overlayMetaParts = append(overlayMetaParts, "handoffs=off")
	}
	overlayMetaParts = append(overlayMetaParts,
		fmt.Sprintf("created %s", feat.CreatedAt.Format("2006-01-02 15:04")),
		fmt.Sprintf("updated %s", feat.UpdatedAt.Format("2006-01-02 15:04")),
	)
	meta := mutedStyle.Render(strings.Join(overlayMetaParts, " · "))

	descHeader := boldStyle.Render("Description")
	var desc string
	if feat.Description == "" {
		desc = mutedStyle.Italic(true).Render("(none)")
	} else {
		desc = f.cachedMD(feat.ID, feat.Description, contentWidth)
	}

	issuesHeader := boldStyle.Render(fmt.Sprintf("Issues · %d", len(f.issues)))
	var issueBlocks []string
	switch {
	case f.issuesErr != nil:
		issueBlocks = append(issueBlocks, errorStyle.Render(f.issuesErr.Error()))
	case len(f.issues) == 0:
		issueBlocks = append(issueBlocks, mutedStyle.Italic(true).Render("(none)"))
	default:
		for _, iss := range f.issues {
			line := keyStyle.Render("["+iss.Key+"]") + "  " +
				mutedStyle.Render(stateLabel(iss.State)) + "  " + iss.Title
			issueBlocks = append(issueBlocks, line)
		}
	}

	all := []string{title, meta, "", descHeader, desc, "", issuesHeader}
	all = append(all, issueBlocks...)

	// BACI-124 feature comments — read-only display under the issues
	// table. Each comment renders as "author — time" followed by its
	// glamour-rendered body, mirroring the issue-show comment surface.
	commentsHeader := boldStyle.Render(fmt.Sprintf("Comments · %d", len(f.comments)))
	all = append(all, "", commentsHeader)
	switch {
	case f.commentsErr != nil:
		all = append(all, errorStyle.Render(f.commentsErr.Error()))
	case len(f.comments) == 0:
		all = append(all, mutedStyle.Italic(true).Render("(none)"))
	default:
		for _, c := range f.comments {
			meta := mutedStyle.Render(fmt.Sprintf("%s — %s", c.Author, c.CreatedAt.Format("2006-01-02 15:04")))
			body := f.cachedMD(-c.ID, c.Body, contentWidth)
			all = append(all, "", meta, body)
		}
	}
	return markdownPanel(width, height, strings.Join(all, "\n"), &f.overlayScroll, true)
}
