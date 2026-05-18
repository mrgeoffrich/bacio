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

// docsView is a two-pane document library: filenames on the left, content
// preview on the right. Pressing enter opens a fullscreen overlay so users
// can read a long doc without leaving the TUI.
type docsView struct {
	store *store.Store
	repo  *model.Repo

	docs       []*model.Document
	row        int
	listScroll int
	loaded     *model.Document // selected doc with content (lazy loaded)
	loadErr    error

	overlay       bool
	overlayScroll int

	// Glamour rendering is multi-ms for long docs; cache the rendered
	// output keyed on terminal width so scrolling within an overlay
	// doesn't re-run glamour every keystroke. See mdCache in
	// internal/tui/markdown.go — one entry per width, with an id check
	// to invalidate stale renders when the focused doc changes.
	mdCache mdCache

	err error
}

func (d *docsView) cachedMD(id int64, src string, width int) string {
	return d.mdCache.get(id, src, width)
}

func newDocsView(s *store.Store, repo *model.Repo) *docsView {
	d := &docsView{store: s, repo: repo}
	d.reload()
	d.refreshSelection()
	return d
}

func (d *docsView) reload() {
	docs, err := d.store.ListDocuments(store.DocumentFilter{RepoID: d.repo.ID})
	if err != nil {
		d.err = err
		return
	}
	d.err = nil
	d.mdCache.reset() // doc bodies may have changed under us
	// Group rendering uses a stable sort by (type-order, filename). Doing
	// this here keeps d.row indexing simple — the same flat slice drives
	// both nav and the grouped view.
	typeOrder := map[model.DocumentType]int{}
	for i, t := range model.AllDocumentTypes() {
		typeOrder[t] = i
	}
	sort.SliceStable(docs, func(i, j int) bool {
		ai, bi := typeOrder[docs[i].Type], typeOrder[docs[j].Type]
		if ai != bi {
			return ai < bi
		}
		return docs[i].Filename < docs[j].Filename
	})
	d.docs = docs
	if d.row >= len(docs) {
		d.row = max(0, len(docs)-1)
	}
}

func (d *docsView) currentDoc() *model.Document {
	if len(d.docs) == 0 {
		return nil
	}
	if d.row >= len(d.docs) {
		d.row = len(d.docs) - 1
	}
	if d.row < 0 {
		d.row = 0
	}
	return d.docs[d.row]
}

// selectByFilename moves the cursor to the doc with the given filename
// and opens it fullscreen. Called from the shell when the board's
// attachments pane sends an openDocMsg, so the user lands directly in
// the doc reader without an intermediate click.
func (d *docsView) selectByFilename(filename string) {
	// The board's attachments pane refreshes its doc links whenever the
	// selected issue changes, but this view only loads its list on
	// construction and on 'r'. Reload here so a document linked while
	// the TUI was already running is still found — otherwise the jump
	// silently lands the user on the bare Documents list.
	d.reload()
	for i, doc := range d.docs {
		if doc.Filename == filename {
			d.row = i
			d.refreshSelection()
			if d.loaded != nil {
				d.overlay = true
				d.overlayScroll = 0
			}
			return
		}
	}
}

func (d *docsView) refreshSelection() {
	cur := d.currentDoc()
	if cur == nil {
		d.loaded = nil
		d.loadErr = nil
		return
	}
	if d.loaded != nil && d.loaded.ID == cur.ID {
		return
	}
	doc, err := d.store.GetDocumentByID(cur.ID, true)
	d.loaded = doc
	d.loadErr = err
	d.overlayScroll = 0
}

func (d *docsView) Init() tea.Cmd    { return nil }
func (d *docsView) Status() string   { return "" }
func (d *docsView) HasOverlay() bool { return d.overlay }

func (d *docsView) CloseOverlay() { d.overlay = false }

func (d *docsView) CapturesInput() bool { return false }

func (d *docsView) Breadcrumb() string {
	if d.overlay && d.loaded != nil {
		return d.loaded.Filename
	}
	return ""
}

func (d *docsView) Help() string {
	if d.overlay {
		return "j/k scroll · g/G top/bottom · esc close"
	}
	return "j/k docs · enter open · r reload · q quit"
}

func (d *docsView) Update(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	if d.overlay {
		switch key.String() {
		case "esc", "enter":
			d.overlay = false
			d.overlayScroll = 0
		case "j", "down":
			d.overlayScroll++
		case "k", "up":
			if d.overlayScroll > 0 {
				d.overlayScroll--
			}
		case "g", "home":
			d.overlayScroll = 0
		case "G", "end":
			d.overlayScroll = 1 << 30
		case "pgdown", " ":
			d.overlayScroll += 10
		case "pgup":
			d.overlayScroll -= 10
			if d.overlayScroll < 0 {
				d.overlayScroll = 0
			}
		}
		return nil
	}
	switch key.String() {
	case "j", "down":
		if d.row < len(d.docs)-1 {
			d.row++
		}
	case "k", "up":
		if d.row > 0 {
			d.row--
		}
	case "g", "home":
		d.row = 0
	case "G", "end":
		if len(d.docs) > 0 {
			d.row = len(d.docs) - 1
		}
	case "r":
		d.reload()
	case "enter":
		if d.loaded != nil {
			d.overlay = true
			d.overlayScroll = 0
		}
	}
	d.refreshSelection()
	return nil
}

func (d *docsView) View(width, height int) string {
	if width == 0 || height == 0 {
		return ""
	}
	if d.overlay {
		return d.viewOverlay(width, height)
	}

	// Left list pane gets ~32 cols (or 1/3, whichever is larger), right pane
	// takes the rest. Both share the available height.
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
	previewW := width - listW

	return lipgloss.JoinHorizontal(lipgloss.Top,
		d.renderList(listW, height),
		d.renderPreview(previewW, height),
	)
}

func (d *docsView) renderList(width, height int) string {
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
		Render(fmt.Sprintf("Documents · %d", len(d.docs)))

	// Body width reserves 1 col for the scrollbar that scrollableBlock pins
	// to the right edge.
	bodyContentWidth := innerWidth - 1
	if bodyContentWidth < 4 {
		bodyContentWidth = 4
	}
	rowStyle := lipgloss.NewStyle().Width(bodyContentWidth).Padding(0, 1)
	selStyle := lipgloss.NewStyle().Width(bodyContentWidth).Padding(0, 1).
		Background(cardSelectedBG).Foreground(lipgloss.Color("231"))
	groupStyle := lipgloss.NewStyle().Width(bodyContentWidth).Padding(0, 1).
		Foreground(cardKeyColor).Bold(true)

	var lines []string
	if len(d.docs) == 0 {
		lines = append(lines, mutedStyle.Padding(1, 1).Render("— no documents —"))
	}
	// Walk the (type-sorted) flat list and emit a group header whenever we
	// cross a type boundary. Headers are non-selectable; d.row still indexes
	// directly into d.docs so navigation stays simple.
	var lastType model.DocumentType
	cursorStart, cursorEnd := -1, -1
	for i, doc := range d.docs {
		if i == 0 || doc.Type != lastType {
			lines = append(lines, groupStyle.Render("▸ "+stringDocType(doc.Type)))
			lastType = doc.Type
		}
		line := "  " + truncate(doc.Filename, bodyContentWidth-4)
		if i == d.row {
			cursorStart = len(lines)
			lines = append(lines, selStyle.Render(line))
			cursorEnd = len(lines)
		} else {
			lines = append(lines, rowStyle.Render(line))
		}
	}

	body := strings.Join(lines, "\n")
	bodyHeight := innerHeight - 1 // header takes 1 row
	if bodyHeight < 1 {
		bodyHeight = 1
	}
	// Keep the selected doc visible by nudging the scroll offset before
	// handing it to scrollableBlock (which clamps against total body height).
	if cursorStart >= 0 {
		if cursorStart < d.listScroll {
			d.listScroll = cursorStart
		}
		if cursorEnd > d.listScroll+bodyHeight {
			d.listScroll = cursorEnd - bodyHeight
		}
		if d.listScroll < 0 {
			d.listScroll = 0
		}
	}

	scrolled := scrollableBlock(innerWidth, bodyHeight, body, &d.listScroll, true)
	content := lipgloss.JoinVertical(lipgloss.Left, header, scrolled)

	return lipgloss.NewStyle().
		Border(colBorder).BorderForeground(colFocusBorder).
		Width(innerWidth).Height(innerHeight).
		Render(content)
}

func (d *docsView) renderPreview(width, height int) string {
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

	if d.err != nil {
		return box.Render(errorStyle.Render(d.err.Error()))
	}
	if d.loadErr != nil {
		return box.Render(errorStyle.Render(d.loadErr.Error()))
	}
	if d.loaded == nil {
		return box.Foreground(mutedColor).Render("No document selected.")
	}

	doc := d.loaded
	title := boldStyle.Render(truncate(doc.Filename, innerWidth))
	meta := mutedStyle.Render(truncate(fmt.Sprintf("%s · %s · updated %s",
		doc.Type, humanBytes(doc.SizeBytes), doc.UpdatedAt.Format("2006-01-02 15:04")),
		innerWidth))

	contentRows := innerHeight - 4
	if contentRows < 1 {
		contentRows = 1
	}
	// Render the preview through glamour at preview-pane width so styling
	// matches the fullscreen overlay. Clip to the rows we have available;
	// it can occasionally cut a styled block mid-element but it's better
	// than a wall of plain text.
	body := d.cachedMD(doc.ID, doc.Content, innerWidth)
	body = clipLines(body, contentRows)

	hint := mutedStyle.Italic(true).Render("(enter for full view)")

	content := lipgloss.JoinVertical(lipgloss.Left, title, meta, "", body, hint)
	return box.Render(content)
}

func (d *docsView) viewOverlay(width, height int) string {
	if d.loaded == nil {
		return lipgloss.NewStyle().
			Border(colBorder).BorderForeground(colFocusBorder).
			Width(width-2).Height(height-2).Padding(1, 2).
			Render("No document selected.")
	}

	// markdownPanel reserves a column for the scrollbar, so wrap markdown
	// to (innerWidth - 1) for a clean right edge.
	contentWidth := width - 7
	if contentWidth < 10 {
		contentWidth = 10
	}

	doc := d.loaded
	title := boldStyle.Render(doc.Filename)
	meta := mutedStyle.Render(fmt.Sprintf("%s · %s · created %s · updated %s",
		doc.Type, humanBytes(doc.SizeBytes),
		doc.CreatedAt.Format("2006-01-02 15:04"),
		doc.UpdatedAt.Format("2006-01-02 15:04"),
	))
	body := d.cachedMD(doc.ID, doc.Content, contentWidth)
	if body == "" {
		body = mutedStyle.Italic(true).Render("(empty)")
	}

	full := strings.Join([]string{title, meta, "", body}, "\n")
	return markdownPanel(width, height, full, &d.overlayScroll, true)
}

// stringDocType returns a human-friendly label for the doc-type group
// headers (e.g. "Project In Planning"). shortDocType remains the compact
// chip form used in the preview pane.
func stringDocType(t model.DocumentType) string {
	switch t {
	case model.DocTypeUserDocs:
		return "User Docs"
	case model.DocTypeProjectInPlanning:
		return "Project In Planning"
	case model.DocTypeProjectInProgress:
		return "Project In Progress"
	case model.DocTypeProjectComplete:
		return "Project Complete"
	case model.DocTypeVendorDocs:
		return "Vendor Docs"
	case model.DocTypeArchitecture:
		return "Architecture"
	case model.DocTypeDesigns:
		return "Designs"
	case model.DocTypeTestingPlans:
		return "Testing Plans"
	}
	return string(t)
}

func shortDocType(t model.DocumentType) string {
	switch t {
	case model.DocTypeUserDocs:
		return "user"
	case model.DocTypeProjectInPlanning:
		return "planning"
	case model.DocTypeProjectInProgress:
		return "in-prog"
	case model.DocTypeProjectComplete:
		return "complete"
	case model.DocTypeVendorDocs:
		return "vendor"
	case model.DocTypeArchitecture:
		return "arch"
	case model.DocTypeDesigns:
		return "design"
	case model.DocTypeTestingPlans:
		return "test"
	}
	return string(t)
}

func humanBytes(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%dB", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1fKB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1fMB", float64(n)/(1024*1024))
	}
}
