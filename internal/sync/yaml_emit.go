package sync

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// The emitter is a small hand-rolled YAML writer tuned to one job:
// produce byte-stable, hash-stable output for the sync repo. The rules
// (always-quote-user-strings, sorted keys, LF-only, single trailing
// newline, RFC3339-ms-UTC timestamps) are spelled out in the design doc.
// We don't reuse a general YAML library because every off-the-shelf
// emitter we tried either (a) didn't quote scalars by default and would
// happily round-trip "on" as a bool, or (b) needed enough post-processing
// to enforce sort order that we'd be reimplementing half the emitter
// anyway. A hundred lines of hand-rolled YAML is easier to audit than
// "trust the library plus our wrappers".

// Node is the union of value shapes the emitter understands. Production
// callers build these via the Map / Seq / Str / Int / Bool / Time helpers
// rather than constructing Node values directly, which keeps the wire
// types invisible to the rest of the package.
type Node struct {
	kind nodeKind

	str   string
	i64   int64
	b     bool
	t     time.Time
	seq   []Node
	pairs []pair
}

type nodeKind int

const (
	kindStr nodeKind = iota
	kindInt
	kindBool
	kindTime
	kindSeq
	kindMap
	kindNull
)

type pair struct {
	key string
	val Node
}

// Str wraps a user-supplied string. The emitter unconditionally wraps it
// in double quotes on output — see the design doc's YAML emit rules for
// why every user scalar gets quoted.
func Str(s string) Node { return Node{kind: kindStr, str: s} }

// Int wraps an integer. Emitted unquoted; safe because integers can't
// be misread as YAML 1.1 booleans or other ambiguous scalars.
func Int(n int64) Node { return Node{kind: kindInt, i64: n} }

// Bool wraps a boolean. Emitted as `true` / `false` unquoted.
func Bool(v bool) Node { return Node{kind: kindBool, b: v} }

// Time wraps a timestamp. Emitted as a quoted RFC3339 UTC string with
// millisecond precision, e.g. "2026-05-09T14:22:00.000Z". The double
// quotes match the user-string rule: always-quoted scalars never
// round-trip as ambiguous types.
func Time(t time.Time) Node { return Node{kind: kindTime, t: t} }

// Seq wraps a list. Empty seqs are emitted as `[]` on a single line.
func Seq(items ...Node) Node { return Node{kind: kindSeq, seq: items} }

// Map wraps a sequence of (key, value) pairs. The emitter sorts keys
// alphabetically before writing for hash stability — pair order at
// construction time is irrelevant.
func Map(pairs ...Pair) Node {
	out := make([]pair, len(pairs))
	for i, p := range pairs {
		out[i] = pair{key: p.Key, val: p.Val}
	}
	return Node{kind: kindMap, pairs: out}
}

// Pair is a (key, value) tuple used to build maps. We export this rather
// than letting callers stuff a Go map[string]Node directly so callers
// pick the order they like in the source code (the emitter sorts at
// emit time anyway, but readability wins).
type Pair struct {
	Key string
	Val Node
}

// Null emits a YAML null. Used sparingly — most "absent" fields are
// omitted from the map entirely.
func Null() Node { return Node{kind: kindNull} }

// Emit serialises a top-level Map (or Seq) to canonical YAML bytes:
// sorted keys, double-quoted user strings, LF line endings, single
// trailing newline. The hash function downstream relies on this being
// byte-stable across runs.
//
// Emit only accepts top-level Map or Seq nodes; other top-level shapes
// don't match anything the sync layer needs to write today.
func Emit(root Node) ([]byte, error) {
	var b strings.Builder
	switch root.kind {
	case kindMap:
		if err := writeMap(&b, root, 0); err != nil {
			return nil, err
		}
	case kindSeq:
		if err := writeTopSeq(&b, root); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("sync: Emit requires a top-level Map or Seq")
	}
	out := b.String()
	// Single trailing newline. Strip any accidental extras the writers
	// produced and re-add exactly one.
	out = strings.TrimRight(out, "\n") + "\n"
	return []byte(out), nil
}

func writeMap(b *strings.Builder, n Node, indent int) error {
	if len(n.pairs) == 0 {
		b.WriteString("{}\n")
		return nil
	}
	pad := strings.Repeat("  ", indent)
	for _, p := range sortedPairs(n.pairs) {
		if err := writeMapEntry(b, p, pad, indent); err != nil {
			return err
		}
	}
	return nil
}

func sortedPairs(in []pair) []pair {
	out := make([]pair, len(in))
	copy(out, in)
	sort.Slice(out, func(i, j int) bool { return out[i].key < out[j].key })
	return out
}

func writeMapEntry(b *strings.Builder, p pair, pad string, indent int) error {
	switch p.val.kind {
	case kindMap:
		if len(p.val.pairs) == 0 {
			fmt.Fprintf(b, "%s%s: {}\n", pad, mapKey(p.key))
			return nil
		}
		fmt.Fprintf(b, "%s%s:\n", pad, mapKey(p.key))
		return writeMap(b, p.val, indent+1)
	case kindSeq:
		if len(p.val.seq) == 0 {
			fmt.Fprintf(b, "%s%s: []\n", pad, mapKey(p.key))
			return nil
		}
		fmt.Fprintf(b, "%s%s:\n", pad, mapKey(p.key))
		// Sequence items live one indent below the parent key — same
		// indent as the key itself in our two-space model. e.g.
		//   relations:
		//     blocks:
		//       - {label: ..., uuid: ...}
		return writeSeqItems(b, p.val.seq, indent+1)
	default:
		s, err := scalarString(p.val)
		if err != nil {
			return err
		}
		fmt.Fprintf(b, "%s%s: %s\n", pad, mapKey(p.key), s)
		return nil
	}
}

// writeTopSeq handles a top-level sequence, e.g. `redirects.yaml`.
func writeTopSeq(b *strings.Builder, n Node) error {
	if len(n.seq) == 0 {
		b.WriteString("[]\n")
		return nil
	}
	return writeSeqItems(b, n.seq, 0)
}

// writeSeqItems emits one bullet per item in `items` at the given
// indent level. Map items use block style with sorted keys; scalar
// items go on the bullet line.
func writeSeqItems(b *strings.Builder, items []Node, indent int) error {
	pad := strings.Repeat("  ", indent)
	for _, item := range items {
		switch item.kind {
		case kindMap:
			if len(item.pairs) == 0 {
				fmt.Fprintf(b, "%s- {}\n", pad)
				continue
			}
			ps := sortedPairs(item.pairs)
			// First key sits on the bullet line; subsequent keys
			// indent two spaces past the bullet.
			for j, p := range ps {
				lead := pad + "  "
				if j == 0 {
					lead = pad + "- "
				}
				if err := writeSeqMapField(b, p, lead, indent); err != nil {
					return err
				}
			}
		case kindSeq:
			// Nested sequence inside a sequence: emit a bullet, then
			// recurse one level deeper. Used by no current schema, but
			// kept for completeness so a future caller doesn't get a
			// silent malformed line.
			fmt.Fprintf(b, "%s-\n", pad)
			if err := writeSeqItems(b, item.seq, indent+1); err != nil {
				return err
			}
		default:
			s, err := scalarString(item)
			if err != nil {
				return err
			}
			fmt.Fprintf(b, "%s- %s\n", pad, s)
		}
	}
	return nil
}

// writeSeqMapField writes one (key: value) entry inside a sequence-of-maps
// item, where `lead` is the pre-key prefix (either "<pad>- " for the
// first key or "<pad>  " for subsequent keys). For nested map / seq
// values, downstream writers re-indent relative to (indent+1) so children
// align under the bullet's "- ".
func writeSeqMapField(b *strings.Builder, p pair, lead string, indent int) error {
	switch p.val.kind {
	case kindMap:
		if len(p.val.pairs) == 0 {
			fmt.Fprintf(b, "%s%s: {}\n", lead, mapKey(p.key))
			return nil
		}
		fmt.Fprintf(b, "%s%s:\n", lead, mapKey(p.key))
		return writeMap(b, p.val, indent+2)
	case kindSeq:
		if len(p.val.seq) == 0 {
			fmt.Fprintf(b, "%s%s: []\n", lead, mapKey(p.key))
			return nil
		}
		fmt.Fprintf(b, "%s%s:\n", lead, mapKey(p.key))
		return writeSeqItems(b, p.val.seq, indent+2)
	default:
		s, err := scalarString(p.val)
		if err != nil {
			return err
		}
		fmt.Fprintf(b, "%s%s: %s\n", lead, mapKey(p.key), s)
		return nil
	}
}

// mapKey formats a YAML map key. Today every key the sync layer emits
// is a plain ASCII identifier (`uuid`, `created_at`, …) so quoting is
// optional. We still validate against the safe set so a future caller
// passing a user-controlled key string can't accidentally break the
// schema.
func mapKey(k string) string {
	if !isPlainKey(k) {
		// Caller bug: every YAML key the emitter writes is hard-coded
		// schema, never user data. Quote-and-escape defensively rather
		// than panic so a fuzz-derived test still produces output.
		return quoteString(k)
	}
	return k
}

func isPlainKey(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if i == 0 {
			if !(r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')) {
				return false
			}
			continue
		}
		if !(r == '_' || r == '-' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

func scalarString(n Node) (string, error) {
	switch n.kind {
	case kindStr:
		return quoteString(n.str), nil
	case kindInt:
		return strconv.FormatInt(n.i64, 10), nil
	case kindBool:
		if n.b {
			return "true", nil
		}
		return "false", nil
	case kindTime:
		return quoteString(formatTime(n.t)), nil
	case kindNull:
		return "null", nil
	default:
		return "", fmt.Errorf("sync: scalarString called on non-scalar kind %d", n.kind)
	}
}

// formatTime renders a time as RFC3339 with millisecond precision and
// a `Z` suffix. Always UTC; the input is normalised to UTC before
// formatting so non-UTC inputs round-trip identically.
func formatTime(t time.Time) string {
	// Truncate to milliseconds. time.Format with ".000" rounds nothing —
	// it just zero-pads — so we explicitly truncate the underlying value
	// first to avoid surprising ms drift if the caller passes in a
	// nanosecond-precision timestamp.
	t = t.UTC().Truncate(time.Millisecond)
	return t.Format("2006-01-02T15:04:05.000Z")
}

// NormalizeNFC returns the NFC-normalised form of s. NFC is the standard
// "precomposed" Unicode form (é as one code point rather than e + combining
// acute). We normalise on the way out so the on-disk YAML is canonical
// regardless of how the user typed it; the DB itself isn't touched in
// Phase 2.
func NormalizeNFC(s string) string {
	if s == "" {
		return s
	}
	return norm.NFC.String(s)
}

// quoteString produces a double-quoted YAML scalar. We always quote
// user-supplied strings (see the design doc); this is the function that
// enforces it. The escaping rules are a strict subset of YAML 1.2 §7.3.1
// double-quoted style:
//
//   - backslash → \\
//   - double quote → \"
//   - newline → \n
//   - tab → \t
//   - carriage return → \r
//   - other control chars (< 0x20) → \xNN
//
// Non-ASCII printable characters pass through verbatim. Inputs are
// NFC-normalised first so the canonical form on disk doesn't depend on
// how the user typed combining marks.
//
// Note: emit is the canonicalisation boundary. The DB row is left in
// whatever form the user typed; the YAML on disk is always NFC.
func quoteString(s string) string {
	s = NormalizeNFC(s)
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\x%02X`, r)
				continue
			}
			if r == utf8.RuneError {
				// Replace invalid UTF-8 with the Unicode replacement
				// character. The emitter guarantees valid YAML; the
				// caller is responsible for ensuring inputs are valid
				// UTF-8 to begin with (bacio's validators already do).
				b.WriteRune('�')
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
