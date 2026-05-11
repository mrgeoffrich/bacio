package tui

import (
	"os"
	"strings"
	"testing"
)

// sampleMarkdown is a representative description body — headings, bold,
// lists, code, blockquote, links — sized like a long planning doc.
var sampleMarkdown = strings.Repeat(`# Phase Heading

Some prose explaining the phase. *Imported from upstream tracker.*

**Source:** docs/planning/some-doc.md §Phase X
[link to repo](https://example.com/repo)

## Goal

Detail what this phase delivers. Multiple paragraphs of context. Lorem
ipsum dolor sit amet, consectetur adipiscing elit, sed do eiusmod tempor
incididunt ut labore et dolore magna aliqua.

## Deliverables

* First deliverable
* Second deliverable with `+"`code identifier`"+`
* Third deliverable

`+"```go\n"+`func example() {
    return "renders through chroma"
}
`+"```\n"+`

> Block quote about trade-offs and constraints.

`, 6) // ~6 KB of representative markdown

// loadLargeMarkdown reads a real on-disk design doc as the worst-case test
// fixture (typically ~50 KB+). It's skipped by default — set BACIO_BENCH_MD to
// the absolute path of any large markdown file to opt in.
func loadLargeMarkdown(b *testing.B) string {
	b.Helper()
	path := os.Getenv("BACIO_BENCH_MD")
	if path == "" {
		b.Skip("BACIO_BENCH_MD not set; pointing it at a large markdown file enables this benchmark")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		b.Skipf("large markdown fixture missing: %v", err)
	}
	return string(data)
}

// BenchmarkRenderMarkdownCold measures a single uncached glamour render
// at our typical overlay width. This is the cost we pay every keystroke
// without caching.
func BenchmarkRenderMarkdownCold(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = renderMarkdown(sampleMarkdown, 90)
	}
}

// BenchmarkCachedMDHit measures a cache hit — the path scrolling within
// an overlay should take after the first render.
func BenchmarkCachedMDHit(b *testing.B) {
	cache := map[int]mdCacheEntry{}
	const issueID = 42
	const width = 90
	// Prime the cache once; subsequent calls are pure map lookups.
	cache[width] = mdCacheEntry{id: issueID, out: renderMarkdown(sampleMarkdown, width)}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if e, ok := cache[width]; ok && e.id == issueID {
			_ = e.out
		}
	}
}

// BenchmarkCachedMDMiss confirms a cache miss costs ~the same as a cold
// render — i.e. the cache wrapper overhead is negligible. The miss path
// is what fires once on selection change or first overlay open.
func BenchmarkCachedMDMiss(b *testing.B) {
	for i := 0; i < b.N; i++ {
		cache := map[int]mdCacheEntry{}
		const width = 90
		const issueID = 42
		out := renderMarkdown(sampleMarkdown, width)
		cache[width] = mdCacheEntry{id: issueID, out: out}
		_ = cache[width].out
	}
}

// BenchmarkRenderMarkdownLarge measures the worst case the user might
// hit: a real ~59 KB design doc rendered through glamour. This is the
// first-open cost an overlay pays before the cache fills.
func BenchmarkRenderMarkdownLarge(b *testing.B) {
	md := loadLargeMarkdown(b)
	b.SetBytes(int64(len(md)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = renderMarkdown(md, 90)
	}
}

// BenchmarkCachedMDHitLarge measures the steady-state cost of scrolling
// inside a long overlay: cache lookup + the rendered string sitting in
// memory. Should be ~1 ns regardless of source size.
func BenchmarkCachedMDHitLarge(b *testing.B) {
	md := loadLargeMarkdown(b)
	cache := map[int]mdCacheEntry{}
	const issueID = 42
	const width = 90
	cache[width] = mdCacheEntry{id: issueID, out: renderMarkdown(md, width)}
	b.SetBytes(int64(len(md)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if e, ok := cache[width]; ok && e.id == issueID {
			_ = e.out
		}
	}
}
