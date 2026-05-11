package tui

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

// wrapLinesAt word-wraps s across up to maxLines lines, where line i may
// have its own width given by widthAt(i). Words longer than the line's
// width are hard-broken. Overflow past maxLines truncates the last visible
// line with an ellipsis.
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
		if utf8.RuneCountInString(rest) <= w {
			lines = append(lines, rest)
			rest = ""
			break
		}
		runes := []rune(rest)
		breakAt := -1
		for i := w; i > 0; i-- {
			if runes[i-1] == ' ' {
				breakAt = i - 1
				break
			}
		}
		if breakAt > 0 {
			lines = append(lines, strings.TrimRight(string(runes[:breakAt]), " "))
			rest = strings.TrimLeft(string(runes[breakAt:]), " ")
			continue
		}
		// Hard break inside a word: reserve one column for a hyphen so the
		// reader knows the word continues on the next line.
		cut := w - 1
		if cut < 1 {
			cut = w
		}
		lines = append(lines, string(runes[:cut])+"-")
		rest = string(runes[cut:])
	}

	if len(rest) > 0 && len(lines) > 0 {
		last := []rune(lines[len(lines)-1])
		if len(last) > 0 && last[len(last)-1] != '…' {
			firstDropped, _ := utf8.DecodeRuneInString(rest)
			midWord := isWordRune(last[len(last)-1]) && isWordRune(firstDropped)
			if midWord && len(last) >= 2 {
				// Mid-word truncation: prefix the ellipsis with a hyphen so
				// readers can tell the word was cut, not the sentence.
				lines[len(lines)-1] = string(last[:len(last)-2]) + "-…"
			} else {
				lines[len(lines)-1] = string(last[:len(last)-1]) + "…"
			}
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
