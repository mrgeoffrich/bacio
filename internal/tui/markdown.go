package tui

import (
	"strings"
	"sync"

	"github.com/charmbracelet/glamour"
)

// Markdown rendering for fullscreen overlays. The renderer is the slow part
// (it builds a chroma lexer + style at construction time), so we keep one
// per terminal width and reuse it across calls. The expectation is that
// `width` only changes on terminal resize, so the map stays small.
//
// Falls back to raw markdown on any error — formatting is decorative.

var (
	mdRenderers sync.Map // int width → *glamour.TermRenderer
	mdMinWidth  = 20
)

func mdRenderer(width int) *glamour.TermRenderer {
	if width < mdMinWidth {
		width = mdMinWidth
	}
	if v, ok := mdRenderers.Load(width); ok {
		return v.(*glamour.TermRenderer)
	}
	// WithStandardStyle("dark") not WithAutoStyle() — auto-detect issues
	// an OSC 11 background-colour query against stdin, which races bubbletea's
	// own input reader in alt-screen mode and can stall the renderer build
	// (= a UI freeze the first time a new width appears, e.g. on tab switch
	// or terminal resize). The TUI is dark-only by design, so we pin it.
	r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return nil
	}
	mdRenderers.Store(width, r)
	return r
}

// mdCacheEntry pairs a rendered markdown string with the entity ID it was
// rendered for. Views key it by terminal width and discard mismatches.
type mdCacheEntry struct {
	id  int64
	out string
}

// renderMarkdown returns a styled rendering of `md` sized to `width`. On
// error or empty input it returns a sensible fallback rather than panicking
// — markdown formatting in this TUI is purely decorative.
func renderMarkdown(md string, width int) string {
	md = strings.TrimSpace(md)
	if md == "" {
		return ""
	}
	r := mdRenderer(width)
	if r == nil {
		return md
	}
	out, err := r.Render(md)
	if err != nil {
		return md
	}
	// Glamour pads with leading + trailing newlines; strip both so the
	// caller can compose blocks tightly.
	return strings.Trim(out, "\n")
}
