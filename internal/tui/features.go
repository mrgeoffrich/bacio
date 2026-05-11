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

	features []*model.Feature
	row      int

	selected  *model.Feature
	issues    []*model.Issue
	issuesErr error

	overlay       bool
	overlayScroll int

	mdCache map[int]mdCacheEntry // see docsView for shape

	err error
}

func (f *featuresView) cachedMD(id int64, src string, width int) string {
	if f.mdCache == nil {
		f.mdCache = map[int]mdCacheEntry{}
	}
	if e, ok := f.mdCache[width]; ok && e.id == id {
		return e.out
	}
	out := renderMarkdown(src, width)
	f.mdCache[width] = mdCacheEntry{id: id, out: out}
	return out
}

func newFeaturesView(s *store.Store, repo *model.Repo) *featuresView {
	f := &featuresView{store: s, repo: repo}
	f.reload()
	f.refreshSelection()
	return f
}

func (f *featuresView) reload() {
	list, err := f.store.ListFeatures(f.repo.ID, true)
	if err != nil {
		f.err = err
		return
	}
	f.err = nil
	f.mdCache = nil
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
	f.overlayScroll = 0
}

func (f *featuresView) Init() tea.Cmd    { return nil }
func (f *featuresView) Status() string   { return "" }
func (f *featuresView) HasOverlay() bool { return f.overlay }

func (f *featuresView) CloseOverlay() { f.overlay = false }

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
	return "j/k features · enter open · r reload · q quit"
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
	}
	f.refreshSelection()
	return nil
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

	header := lipgloss.NewStyle().
		Bold(true).Foreground(lipgloss.Color("231")).Background(colHeaderFocus).
		Width(innerWidth).Align(lipgloss.Center).
		Render(fmt.Sprintf("Features · %d", len(f.features)))

	rowStyle := lipgloss.NewStyle().Width(innerWidth).Padding(0, 1)
	selStyle := lipgloss.NewStyle().Width(innerWidth).Padding(0, 1).
		Background(cardSelectedBG).Foreground(lipgloss.Color("231"))

	var lines []string
	if len(f.features) == 0 {
		lines = append(lines, mutedStyle.Padding(1, 1).Render("— no features —"))
	}
	// Each feature row spans 3 lines: slug, then up to 2 wrapped title
	// lines. The same selection background paints all three so a focused
	// feature reads as a single chunk.
	const featureRows = 3
	titleW := innerWidth - 2
	if titleW < 4 {
		titleW = 4
	}
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
		}
		titleLines := wrapLines(feat.Title, titleW, 2)
		for j := 0; j < featureRows; j++ {
			var content string
			switch {
			case j == 0:
				content = slugRender
			case j-1 < len(titleLines):
				content = titleLines[j-1]
			default:
				content = ""
			}
			lines = append(lines, styler.Render(content))
		}
	}

	body := strings.Join(lines, "\n")
	content := lipgloss.JoinVertical(lipgloss.Left, header, body)
	return lipgloss.NewStyle().
		Border(colBorder).BorderForeground(colFocusBorder).
		Width(innerWidth).Height(height - 2).
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
		return box.Foreground(mutedColor).Render("No feature selected.")
	}

	feat := f.selected
	title := boldStyle.Render(truncate(feat.Title, innerWidth))
	meta := mutedStyle.Render(truncate(fmt.Sprintf("[%s] · created %s · updated %s",
		feat.Slug,
		feat.CreatedAt.Format("2006-01-02"),
		feat.UpdatedAt.Format("2006-01-02")),
		innerWidth))

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
			Render("No feature selected.")
	}

	contentWidth := width - 7
	if contentWidth < 10 {
		contentWidth = 10
	}

	feat := f.selected
	title := boldStyle.Render(feat.Title)
	meta := mutedStyle.Render(fmt.Sprintf("[%s] · created %s · updated %s",
		feat.Slug,
		feat.CreatedAt.Format("2006-01-02 15:04"),
		feat.UpdatedAt.Format("2006-01-02 15:04"),
	))

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
	return markdownPanel(width, height, strings.Join(all, "\n"), &f.overlayScroll, true)
}
