package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// boardRefreshInterval is how often the board reloads issues from the
// store while the TUI is open. Short enough to feel live, long enough
// not to thrash SQLite.
const boardRefreshInterval = 30 * time.Second

// overlayPane identifies which sub-pane of the fullscreen card overlay
// currently has the focus, so j/k and enter route to the right place
// and the matching header gets the accent treatment.
type overlayPane int

const (
	paneDescription overlayPane = iota
	paneComments
	paneAttachments
)

// boardRefreshMsg is delivered by tea.Tick to trigger a reload.
type boardRefreshMsg time.Time

func boardRefreshTick() tea.Cmd {
	return tea.Tick(boardRefreshInterval, func(t time.Time) tea.Msg {
		return boardRefreshMsg(t)
	})
}

// boardView is the kanban tab: one column per state, optional bottom detail
// pane, and a fullscreen card overlay opened with `enter`.
type boardView struct {
	store         *store.Store
	repo          *model.Repo
	states        []model.State
	hidden        map[model.State]bool
	columns       map[model.State][]*model.Issue
	col           int
	rows          map[model.State]int
	scroll        map[model.State]int // top visible card index per column
	detailVisible bool

	selected    *model.Issue
	comments    []*model.Comment
	commentsErr error
	docLinks    []*model.DocumentLink
	prs         []*model.PullRequest
	attachErr   error

	overlay       bool
	overlayFocus  overlayPane // which sub-pane has focus inside the card overlay
	overlayScroll int         // scroll offset for the description (top pane)
	commentsRow   int         // line scroll offset within the comments pane
	attachRow     int         // cursor index in the attachments list

	// Comment detail overlay: a fullscreen view of every comment on
	// the focused issue, opened with `enter` while the comments pane
	// is focused. Lives inside b.overlay (you can only get here from
	// the card overlay), so HasOverlay continues to gate on b.overlay.
	commentOverlay       bool
	commentOverlayScroll int

	picker    bool
	pickerRow int

	// Feature filter picker — opened with `f`. hiddenFeatures is keyed
	// by slug; the sentinel store.HiddenFeaturesUnassigned represents
	// the "no feature" group. featurePickerSlugs holds the list shown
	// in the picker (snapshotted when the picker opens so the order
	// doesn't shift while the user is navigating).
	hiddenFeatures    map[string]bool
	featurePicker     bool
	featurePickerRow  int
	featurePickerSlugs []string

	lastRefresh time.Time

	mdCache map[int]mdCacheEntry // see docsView for shape

	// commentMD caches glamour-rendered comment bodies keyed by
	// (commentID, width). Cleared whenever the selected issue changes
	// since the cache only ever serves the focused issue's comments.
	commentMD map[commentMDKey]string

	err error
}

type commentMDKey struct {
	id    int64
	width int
}

func (b *boardView) cachedMD(id int64, src string, width int) string {
	if b.mdCache == nil {
		b.mdCache = map[int]mdCacheEntry{}
	}
	if e, ok := b.mdCache[width]; ok && e.id == id {
		return e.out
	}
	out := renderMarkdown(src, width)
	b.mdCache[width] = mdCacheEntry{id: id, out: out}
	return out
}

func newBoardView(s *store.Store, repo *model.Repo) (*boardView, error) {
	hidden, err := s.LoadHiddenStates(repo.ID)
	if err != nil {
		return nil, err
	}
	hiddenFeats, err := s.LoadHiddenFeatures(repo.ID)
	if err != nil {
		return nil, err
	}
	b := &boardView{
		store:          s,
		repo:           repo,
		states:         model.AllStates(),
		hidden:         hidden,
		hiddenFeatures: hiddenFeats,
		columns:        map[model.State][]*model.Issue{},
		rows:           map[model.State]int{},
		scroll:         map[model.State]int{},
		detailVisible:  true,
	}
	if err := b.reload(); err != nil {
		return nil, err
	}
	b.lastRefresh = time.Now()
	b.clampCol()
	b.refreshSelection()
	return b, nil
}

// visibleStates returns the states whose columns should be drawn, preserving
// canonical state order. When everything is hidden the slice is empty and the
// caller renders a placeholder.
func (b *boardView) visibleStates() []model.State {
	out := make([]model.State, 0, len(b.states))
	for _, st := range b.states {
		if !b.hidden[st] {
			out = append(out, st)
		}
	}
	return out
}

// clampCol pulls b.col back into range whenever the visible set changes.
func (b *boardView) clampCol() {
	v := b.visibleStates()
	if len(v) == 0 {
		b.col = 0
		return
	}
	if b.col >= len(v) {
		b.col = len(v) - 1
	}
	if b.col < 0 {
		b.col = 0
	}
}

// persistHidden writes the current hidden set to disk. Errors are stored on
// the model so the user sees them in the footer instead of crashing the UI.
func (b *boardView) persistHidden() {
	if err := b.store.SaveHiddenStates(b.repo.ID, b.hidden); err != nil {
		b.err = err
	}
}

func (b *boardView) reload() error {
	issues, err := b.store.ListIssues(store.IssueFilter{RepoID: &b.repo.ID})
	if err != nil {
		return err
	}
	for _, st := range b.states {
		b.columns[st] = nil
	}
	for _, iss := range issues {
		if b.featureHidden(iss.FeatureSlug) {
			continue
		}
		b.columns[iss.State] = append(b.columns[iss.State], iss)
	}
	// Issue descriptions may have changed under us — start the cache
	// fresh so stale renders aren't served until a new render replaces
	// them.
	b.mdCache = nil
	return nil
}

// featureHidden reports whether the given feature slug is currently
// filtered out via the feature picker. The empty slug ("") maps to
// store.HiddenFeaturesUnassigned so "(unassigned)" can be toggled like
// any other group.
func (b *boardView) featureHidden(slug string) bool {
	key := slug
	if key == "" {
		key = store.HiddenFeaturesUnassigned
	}
	return b.hiddenFeatures[key]
}

func (b *boardView) currentIssue() *model.Issue {
	v := b.visibleStates()
	if b.col < 0 || b.col >= len(v) {
		return nil
	}
	st := v[b.col]
	issues := b.columns[st]
	if len(issues) == 0 {
		return nil
	}
	r := b.rows[st]
	if r >= len(issues) {
		r = len(issues) - 1
	}
	if r < 0 {
		r = 0
	}
	return issues[r]
}

// refreshSelection re-fetches comments and attachments only when the
// selected issue changes — keeps navigation snappy without a separate
// cache.
func (b *boardView) refreshSelection() {
	iss := b.currentIssue()
	if iss == nil {
		b.selected = nil
		b.comments = nil
		b.commentsErr = nil
		b.docLinks = nil
		b.prs = nil
		b.attachErr = nil
		return
	}
	// ListIssues omits the heavy description column; re-fetch the full row
	// so the preview pane and overlay can render it.
	full, err := b.store.GetIssueByID(iss.ID)
	if err != nil {
		full = iss
	}
	if b.selected != nil && b.selected.ID == iss.ID {
		b.selected = full
		return
	}
	b.selected = full
	b.commentMD = nil
	b.commentsRow = 0
	b.commentOverlay = false
	b.commentOverlayScroll = 0
	cs, err := b.store.ListComments(iss.ID)
	b.comments = cs
	b.commentsErr = err
	docs, derr := b.store.ListDocumentsLinkedToIssue(iss.ID)
	b.docLinks = docs
	prs, perr := b.store.ListPRs(iss.ID)
	b.prs = prs
	switch {
	case derr != nil:
		b.attachErr = derr
	case perr != nil:
		b.attachErr = perr
	default:
		b.attachErr = nil
	}
	b.overlayScroll = 0
}

func (b *boardView) Init() tea.Cmd { return boardRefreshTick() }

func (b *boardView) Status() string {
	if b.lastRefresh.IsZero() {
		return ""
	}
	return "↻ " + b.lastRefresh.Format("15:04:05")
}

func (b *boardView) HasOverlay() bool { return b.overlay || b.picker || b.featurePicker }

func (b *boardView) CloseOverlay() {
	b.overlay = false
	b.picker = false
	b.featurePicker = false
	b.commentOverlay = false
}

func (b *boardView) Breadcrumb() string {
	switch {
	case b.commentOverlay && b.selected != nil:
		return "[" + b.selected.Key + "] → Comments"
	case b.overlay && b.selected != nil:
		return "[" + b.selected.Key + "]"
	case b.picker:
		return "Columns"
	case b.featurePicker:
		return "Features"
	}
	return ""
}

func (b *boardView) Help() string {
	switch {
	case b.picker:
		return "j/k move · space toggle · a all · n none · esc close"
	case b.featurePicker:
		return "j/k move · space toggle · a all · n none · esc close"
	case b.commentOverlay:
		return "j/k scroll · g/G top/bottom · esc back"
	case b.overlay:
		switch b.overlayFocus {
		case paneComments:
			return "tab next pane · j/k scroll · enter open all · esc close"
		case paneAttachments:
			return "tab next pane · j/k select · enter open · esc close"
		}
		return "tab next pane · j/k scroll · g/G top/bottom · esc close"
	}
	return "h/l cols · j/k cards · enter open · c columns · f features · H hide col · d detail · r reload · q quit"
}

func (b *boardView) Update(msg tea.Msg) tea.Cmd {
	if t, ok := msg.(boardRefreshMsg); ok {
		// Periodic background reload. We pull fresh issues, refresh the
		// selected card's comments, and schedule the next tick. Errors
		// surface in the footer rather than crashing the loop.
		if err := b.reload(); err != nil {
			b.err = err
		} else {
			b.err = nil
		}
		b.lastRefresh = time.Time(t)
		b.clampCol()
		b.refreshSelection()
		return boardRefreshTick()
	}
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	if b.picker {
		b.updatePicker(key)
		return nil
	}
	if b.featurePicker {
		b.updateFeaturePicker(key)
		return nil
	}
	if b.commentOverlay {
		b.updateCommentOverlay(key)
		return nil
	}
	if b.overlay {
		return b.updateOverlay(key)
	}
	visible := b.visibleStates()
	switch key.String() {
	case "h", "left":
		if b.col > 0 {
			b.col--
		}
	case "l", "right":
		if b.col < len(visible)-1 {
			b.col++
		}
	case "j", "down":
		if len(visible) == 0 {
			break
		}
		st := visible[b.col]
		if b.rows[st] < len(b.columns[st])-1 {
			b.rows[st]++
		}
	case "k", "up":
		if len(visible) == 0 {
			break
		}
		st := visible[b.col]
		if b.rows[st] > 0 {
			b.rows[st]--
		}
	case "g", "home":
		if len(visible) > 0 {
			b.rows[visible[b.col]] = 0
		}
	case "G", "end":
		if len(visible) == 0 {
			break
		}
		st := visible[b.col]
		if n := len(b.columns[st]); n > 0 {
			b.rows[st] = n - 1
		}
	case "r":
		if err := b.reload(); err != nil {
			b.err = err
		} else {
			b.err = nil
		}
		b.lastRefresh = time.Now()
	case "d":
		b.detailVisible = !b.detailVisible
	case "enter":
		if b.selected != nil {
			b.overlay = true
			b.overlayFocus = paneDescription
			b.overlayScroll = 0
			b.commentsRow = 0
			b.attachRow = 0
		}
	case "c":
		b.openPicker()
	case "f":
		b.openFeaturePicker()
	case "H":
		// Quick power-user hide of the focused column. We refuse to hide
		// the last visible column so the board never goes empty by accident.
		if len(visible) <= 1 {
			break
		}
		st := visible[b.col]
		b.hidden[st] = true
		b.persistHidden()
		b.clampCol()
	}
	b.refreshSelection()
	return nil
}

// updateOverlay handles input while the fullscreen card overlay is up.
// Tab cycles focus through description / comments / attachments; j/k/g/G
// route to the focused pane only.
func (b *boardView) updateOverlay(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case "esc":
		b.overlay = false
		return nil
	case "tab":
		b.overlayFocus = (b.overlayFocus + 1) % 3
		return nil
	case "shift+tab":
		b.overlayFocus = (b.overlayFocus + 2) % 3
		return nil
	}

	switch b.overlayFocus {
	case paneDescription:
		switch key.String() {
		case "enter":
			b.overlay = false
		case "j", "down":
			b.overlayScroll++
		case "k", "up":
			if b.overlayScroll > 0 {
				b.overlayScroll--
			}
		case "g", "home":
			b.overlayScroll = 0
		case "G", "end":
			b.overlayScroll = 1 << 30
		case "pgdown", " ":
			b.overlayScroll += 10
		case "pgup":
			b.overlayScroll -= 10
			if b.overlayScroll < 0 {
				b.overlayScroll = 0
			}
		}
	case paneComments:
		switch key.String() {
		case "enter":
			// Open every comment in a fullscreen scrollable view.
			if len(b.comments) > 0 {
				b.commentOverlay = true
				b.commentOverlayScroll = 0
			}
		case "j", "down":
			b.commentsRow++
		case "k", "up":
			if b.commentsRow > 0 {
				b.commentsRow--
			}
		case "g", "home":
			b.commentsRow = 0
		case "G", "end":
			b.commentsRow = 1 << 30
		case "pgdown", " ":
			b.commentsRow += 10
		case "pgup":
			b.commentsRow -= 10
			if b.commentsRow < 0 {
				b.commentsRow = 0
			}
		}
	case paneAttachments:
		total := len(b.docLinks) + len(b.prs)
		switch key.String() {
		case "enter":
			// Enter on an attachment is more useful than "close
			// overlay" — the user can still close via esc.
			return b.openSelectedAttachment()
		case "j", "down":
			if b.attachRow < total-1 {
				b.attachRow++
			}
		case "k", "up":
			if b.attachRow > 0 {
				b.attachRow--
			}
		case "g", "home":
			b.attachRow = 0
		case "G", "end":
			if total > 0 {
				b.attachRow = total - 1
			}
		}
	}
	return nil
}

func (b *boardView) View(width, height int) string {
	if width == 0 || height == 0 {
		return ""
	}
	if b.picker {
		return b.viewPicker(width, height)
	}
	if b.featurePicker {
		return b.viewFeaturePicker(width, height)
	}
	if b.overlay {
		return b.viewOverlay(width, height)
	}

	// Detail pane sized at ~1/3 of body, clamped 8–14 rows; suppressed on
	// short terminals.
	detailHeight := 0
	if b.detailVisible && height >= 20 {
		detailHeight = height / 3
		if detailHeight < 8 {
			detailHeight = 8
		}
		if detailHeight > 14 {
			detailHeight = 14
		}
	}
	colsHeight := height - detailHeight
	if colsHeight < 5 {
		colsHeight = 5
	}

	visible := b.visibleStates()
	if len(visible) == 0 {
		empty := lipgloss.NewStyle().
			Width(width).Height(colsHeight).
			Align(lipgloss.Center, lipgloss.Center).
			Foreground(mutedColor).
			Render("All columns hidden — press c to choose visible columns.")
		return empty
	}

	n := len(visible)
	colWidth := width / n
	if colWidth < 16 {
		colWidth = 16
	}

	cols := make([]string, n)
	for i, st := range visible {
		cols[i] = b.renderColumn(st, i == b.col, colWidth, colsHeight)
	}
	board := lipgloss.JoinHorizontal(lipgloss.Top, cols...)

	parts := []string{board}
	if detailHeight > 0 {
		parts = append(parts, b.renderDetail(width, detailHeight))
	}
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

func (b *boardView) renderColumn(st model.State, focused bool, width, height int) string {
	issues := b.columns[st]
	sel := b.rows[st]
	if sel >= len(issues) {
		sel = max(0, len(issues)-1)
	}

	border := colBorderColor
	headerBG := colHeaderUnfocus
	if focused {
		border = colFocusBorder
		headerBG = colHeaderFocus
	}

	innerWidth := width - 2
	if innerWidth < 4 {
		innerWidth = 4
	}

	// Each card occupies exactly cardHeight lines: the first carries the
	// issue key and the start of the title, subsequent lines are wrapped
	// title continuations indented to align under the title text.
	const cardHeight = 2
	const headerRows = 1

	// Compute how many cards fit in the body, then nudge the per-column
	// scroll so the cursor stays in view.
	bodyRows := height - 2 - headerRows // -2 for top/bottom borders
	if bodyRows < cardHeight {
		bodyRows = cardHeight
	}
	cardsPerCol := bodyRows / cardHeight
	if cardsPerCol < 1 {
		cardsPerCol = 1
	}
	scroll := b.scroll[st]
	if scroll > sel {
		scroll = sel
	}
	if sel >= scroll+cardsPerCol {
		scroll = sel - cardsPerCol + 1
	}
	maxScroll := len(issues) - cardsPerCol
	if maxScroll < 0 {
		maxScroll = 0
	}
	if scroll > maxScroll {
		scroll = maxScroll
	}
	if scroll < 0 {
		scroll = 0
	}
	b.scroll[st] = scroll

	end := scroll + cardsPerCol
	if end > len(issues) {
		end = len(issues)
	}
	moreAbove := scroll > 0
	moreBelow := end < len(issues)

	indicator := ""
	switch {
	case moreAbove && moreBelow:
		indicator = " ↕"
	case moreAbove:
		indicator = " ↑"
	case moreBelow:
		indicator = " ↓"
	}

	header := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("231")).
		Background(headerBG).
		Width(innerWidth).
		Align(lipgloss.Center).
		Render(fmt.Sprintf("%s · %d%s", stateLabel(st), len(issues), indicator))

	// 1 col reserved at the LEFT of every card line for a coloured
	// stripe keyed on the issue's feature slug. Card content uses the
	// remaining width; the stripe character ▌ sits flush against the
	// column's inner border.
	const stripeChar = "▌"
	contentW := innerWidth - 1
	if contentW < 3 {
		contentW = 3
	}
	cardStyle := lipgloss.NewStyle().Width(contentW)
	selStyle := lipgloss.NewStyle().Width(contentW).
		Background(cardSelectedBG).Foreground(lipgloss.Color("231"))

	var lines []string
	if len(issues) == 0 {
		lines = append(lines, mutedStyle.Width(innerWidth).Padding(1, 1).Render("— empty —"))
	}
	for i := scroll; i < end; i++ {
		iss := issues[i]
		bracketed := "[" + iss.Key + "]"
		fullW := contentW

		isSel := i == sel && focused
		styler := cardStyle
		// When the card is selected we deliberately render the key as plain
		// text. Nested lipgloss styles emit their own reset sequence, which
		// punches a black hole in the parent's background even when we set
		// an explicit bg on the inner span. Letting selStyle paint uniformly
		// gives a clean fill at the cost of the key's accent colour.
		keyRender := keyStyle.Render(bracketed)
		if isSel {
			styler = selStyle
			keyRender = bracketed
		}

		stripe := lipgloss.NewStyle().Foreground(featureColor(iss.FeatureSlug)).Render(stripeChar)
		emit := func(content string) {
			lines = append(lines, stripe+styler.Render(content))
		}

		// If the title fits on a single line at the full inner width, use
		// the spacious layout: [KEY] alone on line 0, title alone on line
		// 1. This avoids the line-0 prefix gutter eating into the title's
		// budget for the common short-title case.
		if titleRunes := []rune(iss.Title); len(titleRunes) <= fullW {
			emit(keyRender)
			emit(iss.Title)
			continue
		}

		// Title needs both lines. Pack [KEY] + the start of the title on
		// line 0, then the continuation on line 1.
		prefixW := len(bracketed) + 2 // [KEY] + two-space gutter
		firstW := contentW - prefixW
		if firstW < 4 {
			firstW = 4
		}
		titleLines := wrapLinesAt(iss.Title, func(line int) int {
			if line == 0 {
				return firstW
			}
			return fullW
		}, cardHeight)

		for j := 0; j < cardHeight; j++ {
			var content string
			switch {
			case j == 0:
				if len(titleLines) > 0 {
					content = keyRender + "  " + titleLines[0]
				} else {
					content = keyRender
				}
			case j < len(titleLines):
				content = titleLines[j]
			default:
				content = ""
			}
			emit(content)
		}
	}

	body := strings.Join(lines, "\n")
	content := lipgloss.JoinVertical(lipgloss.Left, header, body)

	return lipgloss.NewStyle().
		Border(colBorder).
		BorderForeground(border).
		Width(innerWidth).
		Height(height - 2).
		Render(content)
}

func (b *boardView) renderDetail(width, height int) string {
	innerWidth := width - 4
	if innerWidth < 10 {
		innerWidth = 10
	}
	innerHeight := height - 2
	if innerHeight < 3 {
		innerHeight = 3
	}

	box := lipgloss.NewStyle().
		Border(colBorder).
		BorderForeground(colBorderColor).
		Width(width-2).
		Height(height-2).
		Padding(0, 1)

	if b.selected == nil {
		return box.Foreground(mutedColor).Render("No issue selected.")
	}

	iss := b.selected
	bracketedKey := "[" + iss.Key + "]"
	previewTag := mutedStyle.Italic(true).Render("preview")
	previewW := lipgloss.Width(previewTag)
	// Reserve room on the right for the "preview" label plus a 2-col gap.
	titleSpace := innerWidth - len(bracketedKey) - 2 - previewW - 2
	if titleSpace < 4 {
		titleSpace = 4
	}
	titleContent := keyStyle.Bold(true).Render(bracketedKey) + "  " +
		boldStyle.Render(truncate(iss.Title, titleSpace))
	gap := innerWidth - lipgloss.Width(titleContent) - previewW
	if gap < 1 {
		gap = 1
	}
	titleLine := lipgloss.JoinHorizontal(lipgloss.Top,
		titleContent,
		lipgloss.NewStyle().Width(gap).Render(""),
		previewTag,
	)

	metaParts := []string{"state: " + stateLabel(iss.State)}
	if iss.FeatureSlug != "" {
		metaParts = append(metaParts, "feature: ["+iss.FeatureSlug+"]")
	}
	if len(iss.Tags) > 0 {
		metaParts = append(metaParts, "tags: "+strings.Join(iss.Tags, ", "))
	}
	metaParts = append(metaParts, "updated: "+iss.UpdatedAt.Format("2006-01-02 15:04"))
	meta := mutedStyle.Render(truncate(strings.Join(metaParts, " · "), innerWidth))

	var commentLine string
	switch {
	case b.commentsErr != nil:
		commentLine = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).
			Render("comments error: " + b.commentsErr.Error())
	case len(b.comments) == 0:
		commentLine = mutedStyle.Render("0 comments — press enter for full view")
	default:
		c := b.comments[len(b.comments)-1]
		preview := fmt.Sprintf("%d comments · last by %s: %s",
			len(b.comments), c.Author, oneLine(c.Body))
		commentLine = mutedStyle.Render(truncate(preview, innerWidth))
	}

	descRows := innerHeight - 4
	if descRows < 1 {
		descRows = 1
	}
	var desc string
	if iss.Description == "" {
		desc = mutedStyle.Italic(true).Render("(no description — enter for full view)")
	} else {
		desc = b.cachedMD(iss.ID, iss.Description, innerWidth)
		desc = clipLines(desc, descRows)
	}

	content := lipgloss.JoinVertical(lipgloss.Left,
		titleLine,
		meta,
		"",
		desc,
		commentLine,
	)
	return box.Render(content)
}

// viewOverlay renders the fullscreen card view as four content
// regions stitched together inside a single shared rounded frame.
// From top to bottom:
//   - header (fixed 2 content rows) — id, title, metadata.
//   - description (~60% of the remainder, focusable, scrollable).
//   - bottom row (~40% of the remainder): comments | attachments,
//     each focusable, sharing a vertical divider.
//
// Borders are drawn once by renderOverlayFrame; focus is signalled by
// the in-panel "▸ Heading" accent rather than by border colour, since
// the frame is a single shared chrome.
func (b *boardView) viewOverlay(width, height int) string {
	iss := b.selected
	if iss == nil {
		return panelBox(width, height, "No issue selected.", true)
	}
	if b.commentOverlay {
		return b.viewCommentOverlay(width, height)
	}
	if width < 20 || height < 12 {
		return panelBox(width, height, "Terminal too small.", true)
	}

	// 4 horizontal border rows (top, header/desc divider, desc/bottom
	// divider, bottom) eat 4 rows; the rest is content.
	contentRows := height - 4
	headerH := 2
	if headerH > contentRows-6 {
		headerH = max(1, contentRows-6)
	}
	remaining := contentRows - headerH
	descH := remaining * 6 / 10
	if descH < 3 {
		descH = 3
	}
	bottomH := remaining - descH
	if bottomH < 3 {
		bottomH = 3
		descH = remaining - bottomH
	}

	innerW := width - 2
	leftW := innerW / 2
	rightW := innerW - 1 - leftW

	headerLines := b.renderOverlayHeaderLines(iss, innerW, headerH)
	descLines := b.renderOverlayDescriptionLines(iss, innerW, descH)
	leftLines := b.renderCommentsLines(leftW, bottomH)
	rightLines := b.renderAttachmentsLines(rightW, bottomH)

	return renderOverlayFrame(width, headerLines, descLines, leftLines, rightLines, leftW)
}

// renderOverlayHeaderLines returns h lines (each cellW visible cols)
// of the issue-overlay header: title row, then a single meta row.
// Pads with empty rows if h > 2.
func (b *boardView) renderOverlayHeaderLines(iss *model.Issue, cellW, h int) []string {
	titleLine := keyStyle.Bold(true).Render("["+iss.Key+"]") + "  " +
		boldStyle.Render(iss.Title)

	metaItems := []string{stateLabel(iss.State)}
	if iss.FeatureSlug != "" {
		metaItems = append(metaItems, "["+iss.FeatureSlug+"]")
	}
	if len(iss.Tags) > 0 {
		metaItems = append(metaItems, strings.Join(iss.Tags, ", "))
	}
	metaItems = append(metaItems,
		"created "+iss.CreatedAt.Format("2006-01-02 15:04"),
		"updated "+iss.UpdatedAt.Format("2006-01-02 15:04"),
	)
	metaLine := mutedStyle.Render(strings.Join(metaItems, " · "))

	lines := padCell(strings.Join([]string{titleLine, metaLine}, "\n"), cellW)
	for len(lines) < h {
		lines = append(lines, strings.Repeat(" ", cellW))
	}
	return lines[:h]
}

// renderOverlayDescriptionLines returns h lines (each cellW visible
// cols) of the description panel: heading + scrollable markdown body
// + scrollbar, all already padded to cellW.
func (b *boardView) renderOverlayDescriptionLines(iss *model.Issue, cellW, h int) []string {
	focused := b.overlayFocus == paneDescription
	contentW := cellW - 4 // 2 cols horizontal padding on each side
	if contentW < 12 {
		contentW = 12
	}
	mdW := contentW - 1 // 1 col reserved for scrollbar inside scrollableBlock
	if mdW < 10 {
		mdW = 10
	}

	descHeader := paneHeading("Description", focused)
	var desc string
	if iss.Description == "" {
		desc = mutedStyle.Italic(true).Render("(none)")
	} else {
		desc = b.cachedMD(iss.ID, iss.Description, mdW)
	}
	body := strings.Join([]string{descHeader, "", desc}, "\n")
	inner := scrollableBlock(contentW, h, body, &b.overlayScroll, focused)
	return padCell(inner, cellW)
}

// paneHeading returns a section header. When focused, a 🟣 sits to the
// left of the label as the focus indicator. The unfocused variant uses
// 3 leading spaces so the label's x-position doesn't jump on focus
// change (🟣 is 2 cells + 1 space = 3 cells of indent).
func paneHeading(label string, focused bool) string {
	if focused {
		return lipgloss.NewStyle().Bold(true).Foreground(colFocusBorder).
			Render("🟣 " + label)
	}
	return boldStyle.Render("   " + label)
}

// renderCommentsLines returns h lines (each cellW visible cols) of
// the comments column: heading + scrollable comment list + scrollbar,
// padded to cellW so the caller can drop them directly into
// renderOverlayFrame. j/k scrolls line-by-line via b.commentsRow;
// `enter` (handled in updateOverlay) opens the fullscreen detail view.
func (b *boardView) renderCommentsLines(cellW, h int) []string {
	focused := b.overlayFocus == paneComments
	contentW := cellW - 4 // 2 cols horizontal padding each side
	if contentW < 12 {
		contentW = 12
	}
	bodyW := contentW - 1 // scrollbar reservation inside paneScrollFrame
	if bodyW < 10 {
		bodyW = 10
	}

	header := paneHeading(fmt.Sprintf("Comments · %d", len(b.comments)), focused)
	var body []string
	switch {
	case b.commentsErr != nil:
		body = append(body, errorStyle.Render(b.commentsErr.Error()))
	case len(b.comments) == 0:
		body = append(body, mutedStyle.Italic(true).Render("(no comments yet)"))
	default:
		for _, c := range b.comments {
			head := keyStyle.Render(c.Author) +
				mutedStyle.Render("  "+c.CreatedAt.Format("2006-01-02 15:04"))
			body = append(body, head)
			body = append(body, strings.Split(b.cachedCommentMD(c, bodyW), "\n")...)
			body = append(body, "")
		}
	}
	inner := paneScrollFrame(header, body, contentW, h, b.commentsRow, false, -1, true, focused)
	return padCell(inner, cellW)
}

// renderAttachmentsLines returns h lines (each cellW visible cols) of
// the attachments column: heading + selectable item list, padded to
// cellW. Each item has a title (filename / PR URL) and optionally a
// subtitle (italic muted) on the next line. The selected item's title
// renders in the focus colour when this pane is focused; other titles
// render plain white. Selection auto-scrolls into view.
func (b *boardView) renderAttachmentsLines(cellW, h int) []string {
	focused := b.overlayFocus == paneAttachments
	contentW := cellW - 4
	if contentW < 12 {
		contentW = 12
	}

	count := len(b.docLinks) + len(b.prs)
	header := paneHeading(fmt.Sprintf("Attachments · %d", count), focused)

	if focused && count > 0 && b.attachRow >= count {
		b.attachRow = count - 1
	}

	titleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("231"))
	selectedStyle := lipgloss.NewStyle().Foreground(colFocusBorder).Bold(true)
	subtitleStyle := mutedStyle.Italic(true)

	// titleSlot is the body-slot index of the currently selected item's
	// title row. paneScrollFrame uses it to scroll-track without
	// painting a highlight bar (we colour the title text directly).
	titleSlot := -1

	var body []string
	switch {
	case b.attachErr != nil:
		body = append(body, errorStyle.Render(b.attachErr.Error()))
	case count == 0:
		body = append(body, mutedStyle.Italic(true).Render("(none)"))
	default:
		for i, l := range b.docLinks {
			style := titleStyle
			if focused && b.attachRow == i {
				style = selectedStyle
				titleSlot = len(body)
			}
			body = append(body, style.Render("📄 "+truncate(l.DocumentFilename, contentW-3)))
			if l.Description != "" {
				body = append(body, subtitleStyle.Render("   "+truncate(l.Description, contentW-3)))
			}
		}
		docCount := len(b.docLinks)
		for i, pr := range b.prs {
			style := titleStyle
			if focused && b.attachRow == docCount+i {
				style = selectedStyle
				titleSlot = len(body)
			}
			body = append(body, style.Render("🔀 "+truncate(pr.URL, contentW-3)))
		}
	}

	inner := paneScrollFrame(header, body, contentW, h, 0, false, titleSlot, false, focused)
	return padCell(inner, cellW)
}

// paneScrollFrame composes a focused-pane: a sticky header row, a
// blank spacer row, then a (height-2)-row body window that the caller
// can scroll (offset) and/or highlight a single row in (cursorIdx).
// When highlightCursor is false the row offset is treated as a pure
// scroll position. When withScrollbar is true a 1-col scrollbar is
// reserved on the right edge of the body rows; the column is reserved
// unconditionally so toggling scrollable / not doesn't shift content.
func paneScrollFrame(header string, body []string, width, height, offset int, highlightCursor bool, cursorIdx int, withScrollbar, focused bool) string {
	rowsAvailable := height - 2 // 1 header row + 1 spacer below it
	if rowsAvailable < 1 {
		rowsAvailable = 1
	}
	bodyWidth := width
	if withScrollbar {
		bodyWidth = width - 1
		if bodyWidth < 1 {
			bodyWidth = 1
		}
	}

	// Auto-scroll so the cursor (if any) stays in view. Independent of
	// highlightCursor — callers can ask for "scroll-tracking only" by
	// passing cursorIdx>=0 with highlightCursor=false.
	if cursorIdx >= 0 {
		if cursorIdx < offset {
			offset = cursorIdx
		}
		if cursorIdx >= offset+rowsAvailable {
			offset = cursorIdx - rowsAvailable + 1
		}
	}
	maxOffset := len(body) - rowsAvailable
	if maxOffset < 0 {
		maxOffset = 0
	}
	if offset > maxOffset {
		offset = maxOffset
	}
	if offset < 0 {
		offset = 0
	}

	end := offset + rowsAvailable
	if end > len(body) {
		end = len(body)
	}
	visible := append([]string(nil), body[offset:end]...)

	if highlightCursor && cursorIdx >= offset && cursorIdx < end {
		i := cursorIdx - offset
		visible[i] = lipgloss.NewStyle().Width(bodyWidth).
			Background(cardSelectedBG).Foreground(lipgloss.Color("231")).
			Render(visible[i])
	}

	for len(visible) < rowsAvailable {
		visible = append(visible, "")
	}

	headerLine := lipgloss.NewStyle().Width(width).Render(header)
	spacer := lipgloss.NewStyle().Width(width).Render("")
	bodyBlock := lipgloss.NewStyle().Width(bodyWidth).Render(strings.Join(visible, "\n"))
	if withScrollbar {
		bar := renderVerticalScrollbar(rowsAvailable, len(body), offset, focused)
		bodyBlock = lipgloss.JoinHorizontal(lipgloss.Top, bodyBlock, bar)
	}
	return lipgloss.JoinVertical(lipgloss.Left, headerLine, spacer, bodyBlock)
}

func stateLabel(st model.State) string {
	switch st {
	case model.StateBacklog:
		return "Backlog"
	case model.StateTodo:
		return "Todo"
	case model.StateInProgress:
		return "In Progress"
	case model.StateInReview:
		return "In Review"
	case model.StateDone:
		return "Done"
	case model.StateCancelled:
		return "Cancelled"
	case model.StateDuplicate:
		return "Duplicate"
	}
	return string(st)
}
