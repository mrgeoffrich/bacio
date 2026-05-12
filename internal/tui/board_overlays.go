package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// updateCommentOverlay handles input while the fullscreen comment
// detail view is up. esc returns to the card overlay's comments pane;
// j/k/g/G scroll the body. Always falls through to nil — the caller
// (Update) doesn't expect a Cmd here.
func (b *boardView) updateCommentOverlay(key tea.KeyMsg) {
	switch key.String() {
	case "esc":
		b.commentOverlay = false
	case "j", "down":
		b.commentOverlayScroll++
	case "k", "up":
		if b.commentOverlayScroll > 0 {
			b.commentOverlayScroll--
		}
	case "g", "home":
		b.commentOverlayScroll = 0
	case "G", "end":
		b.commentOverlayScroll = 1 << 30
	case "pgdown", " ":
		b.commentOverlayScroll += 10
	case "pgup":
		b.commentOverlayScroll -= 10
		if b.commentOverlayScroll < 0 {
			b.commentOverlayScroll = 0
		}
	}
}

// openSelectedAttachment fires an openDocMsg for a selected document so
// the shell can switch tabs and open it. PRs aren't actionable yet —
// pressing enter on one is a no-op until we wire a "copy URL" or
// browser-launch action.
func (b *boardView) openSelectedAttachment() tea.Cmd {
	docs := len(b.docLinks)
	if b.attachRow < 0 || b.attachRow >= docs {
		return nil
	}
	filename := b.docLinks[b.attachRow].DocumentFilename
	return func() tea.Msg {
		return openDocMsg{filename: filename}
	}
}

// viewCommentOverlay renders every comment on the focused issue as a
// single fullscreen scrollable markdown view. Each comment block is
// preceded by an author + timestamp header and separated from the
// next by a horizontal rule. Reuses markdownPanel for chrome so it
// matches the doc and feature overlays.
func (b *boardView) viewCommentOverlay(width, height int) string {
	if len(b.comments) == 0 {
		return panelBox(width, height, "No comments.", true)
	}

	contentWidth := width - 7
	if contentWidth < 10 {
		contentWidth = 10
	}

	parts := []string{boldStyle.Render(fmt.Sprintf("Comments · %d", len(b.comments))), ""}
	for i, c := range b.comments {
		if i > 0 {
			parts = append(parts, mutedStyle.Render(strings.Repeat("─", contentWidth)), "")
		}
		head := keyStyle.Render(c.Author) +
			mutedStyle.Render("  "+c.CreatedAt.Format("2006-01-02 15:04"))
		parts = append(parts, head, "", b.cachedCommentMD(c, contentWidth), "")
	}
	return markdownPanel(width, height, strings.Join(parts, "\n"), &b.commentOverlayScroll, true)
}

// cachedCommentMD renders a comment body through glamour at the given
// width, caching the result so frequent View() redraws don't re-run the
// markdown renderer. Cleared in refreshSelection when the focused issue
// changes.
func (b *boardView) cachedCommentMD(c *model.Comment, width int) string {
	key := commentMDKey{id: c.ID, width: width}
	if out, ok := b.commentMD[key]; ok {
		return out
	}
	out := renderMarkdown(c.Body, width)
	if out == "" {
		out = mutedStyle.Italic(true).Render("(empty)")
	}
	if b.commentMD == nil {
		b.commentMD = map[commentMDKey]string{}
	}
	b.commentMD[key] = out
	return out
}
