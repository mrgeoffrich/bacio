package tui

import (
	"strings"
)

// wrapLinesAt fills s across up to maxLines lines, where line i may have
// its own width given by widthAt(i). Wraps strictly on rune count — no
// word-boundary preference, no hyphenation. Pure character wrapping
// packs the most content into each kanban card slot; the previous
// word-aware variant was leaving short trailing lines (e.g. "Receipt" +
// "printer…" instead of "Receipt printer" + "compat test") and burning
// two extra chars on the hyphen-plus-ellipsis tail.
//
// Trailing whitespace on each emitted line is stripped (it would just be
// padding under the styler anyway) and leading whitespace on continuation
// lines is also stripped so wrapped words don't look indented.
//
// When the budget runs out and real content remains, the very last
// rune of the last visible line is replaced with `…` so readers can
// tell the title was truncated rather than ending where they see it.
// The replacement is in-place (no extra width grab) so the rendered
// line stays inside its column.
func wrapLinesAt(s string, widthAt func(int) int, maxLines int) []string {
	if maxLines <= 0 {
		return nil
	}
	rest := strings.TrimSpace(s)
	if rest == "" {
		return nil
	}

	var lines []string
	for len(rest) > 0 && len(lines) < maxLines {
		w := widthAt(len(lines))
		if w <= 0 {
			break
		}
		runes := []rune(rest)
		if len(runes) <= w {
			lines = append(lines, rest)
			rest = ""
			break
		}
		lines = append(lines, strings.TrimRight(string(runes[:w]), " "))
		rest = strings.TrimLeft(string(runes[w:]), " ")
	}

	if len(rest) > 0 && len(lines) > 0 {
		last := []rune(lines[len(lines)-1])
		if len(last) > 0 && last[len(last)-1] != '…' {
			last[len(last)-1] = '…'
			lines[len(lines)-1] = string(last)
		}
	}
	return lines
}

// wrapLines is the fixed-width convenience form of wrapLinesAt.
func wrapLines(s string, width, maxLines int) []string {
	return wrapLinesAt(s, func(int) int { return width }, maxLines)
}

// truncate clips s to at most n display columns (rune-aware), appending …
// when truncation happens. Returns "" for non-positive n.
func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n == 1 {
		return "…"
	}
	return string(r[:n-1]) + "…"
}

// clipLines keeps the first n lines, marking the last visible line with an
// ellipsis when there's more.
func clipLines(s string, n int) string {
	if n <= 0 {
		return ""
	}
	parts := strings.Split(s, "\n")
	if len(parts) <= n {
		return s
	}
	parts = parts[:n]
	last := []rune(parts[n-1])
	if len(last) == 0 {
		parts[n-1] = "…"
	} else if last[len(last)-1] != '…' {
		parts[n-1] = string(last[:len(last)-1]) + "…"
	}
	return strings.Join(parts, "\n")
}

// scrollLines pages a pre-wrapped block: drops `offset` leading lines, then
// keeps `height` lines.
func scrollLines(s string, offset, height int) string {
	if height <= 0 {
		return ""
	}
	parts := strings.Split(s, "\n")
	if offset < 0 {
		offset = 0
	}
	if offset >= len(parts) {
		return ""
	}
	parts = parts[offset:]
	if len(parts) > height {
		parts = parts[:height]
	}
	return strings.Join(parts, "\n")
}

// totalLines returns the number of lines in s after splitting on \n.
func totalLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

// indentLines prepends `prefix` to every line of s. Useful for nesting a
// pre-rendered (and possibly multi-line) block under a heading.
func indentLines(s, prefix string) string {
	if s == "" {
		return ""
	}
	parts := strings.Split(s, "\n")
	for i, p := range parts {
		parts[i] = prefix + p
	}
	return strings.Join(parts, "\n")
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.TrimSpace(s)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
