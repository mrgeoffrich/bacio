package sync

import (
	"strings"
	"testing"
	"time"

	yaml "go.yaml.in/yaml/v4"
)

// TestEmit_AlwaysQuotedStringsRoundTripThroughYAMLParser is the YAML 1.1
// boolean-coercion gotcha test from the design doc: an unquoted
// `assignee: on` round-trips as a Go bool (true), not the string "on".
// Always-quote-user-strings is what saves us — this test pins it by
// emitting the value, parsing it back through go.yaml.in/yaml/v4, and
// asserting the round-trip kind is string.
func TestEmit_AlwaysQuotedStringsRoundTripThroughYAMLParser(t *testing.T) {
	cases := []string{
		"on", "off", "yes", "no", "true", "false",
		"y", "n", "Y", "N", "Yes", "No",
		"~", "null", "Null", "NULL",
		"0123", "0o7", "0x7F", // numeric-looking
		"+.inf", "-.inf", ".nan", // float-looking
	}
	for _, val := range cases {
		t.Run(val, func(t *testing.T) {
			root := Map(Pair{"assignee", Str(val)})
			out, err := Emit(root)
			if err != nil {
				t.Fatalf("emit: %v", err)
			}
			var got map[string]any
			if err := yaml.Unmarshal(out, &got); err != nil {
				t.Fatalf("unmarshal %q: %v\nemit was: %s", val, err, out)
			}
			s, ok := got["assignee"].(string)
			if !ok {
				t.Fatalf("assignee=%q round-tripped as %T (%v) not string\nemit was: %s",
					val, got["assignee"], got["assignee"], out)
			}
			if s != val {
				t.Fatalf("assignee=%q round-tripped as %q\nemit was: %s", val, s, out)
			}
		})
	}
}

// TestEmit_EmojiInTitle pins that multi-byte UTF-8 (emoji, CJK) passes
// through verbatim without escaping. The emitter only escapes control
// characters and the structural set; everything else stays literal.
func TestEmit_EmojiInTitle(t *testing.T) {
	cases := []struct{ name, in string }{
		{"single-emoji", "Add auth 🔐 middleware"},
		{"family-zwj", "PR by 👨‍👩‍👧"},
		{"cjk", "認証ミドルウェアを追加"},
		{"flag", "Ship it 🇦🇺"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := Emit(Map(Pair{"title", Str(tc.in)}))
			if err != nil {
				t.Fatalf("emit: %v", err)
			}
			var got map[string]any
			if err := yaml.Unmarshal(out, &got); err != nil {
				t.Fatalf("unmarshal: %v\nemit was: %s", err, out)
			}
			if got["title"] != tc.in {
				t.Fatalf("title round-trip: got %q want %q\nemit was: %s",
					got["title"], tc.in, out)
			}
		})
	}
}

// TestEmit_QuotesAndApostrophes checks the escaping path for the two
// characters that matter most in real text: " and '. Backslash is
// included for completeness.
func TestEmit_QuotesAndApostrophes(t *testing.T) {
	cases := []struct{ name, in string }{
		{"apostrophe", "geoff's issue"},
		{"double-quote", `she said "go"`},
		{"backslash", `path\to\file`},
		{"mixed", `he said "it's a \trap"`},
		{"leading-space", "   leading whitespace"},
		{"trailing-space", "trailing whitespace   "},
		{"empty", ""},
		{"newline-mid", "line one\nline two"},
		{"tab-mid", "col1\tcol2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := Emit(Map(Pair{"v", Str(tc.in)}))
			if err != nil {
				t.Fatalf("emit: %v", err)
			}
			// Output must have no literal newlines inside the scalar
			// — body bytes inside the quotes must all stay on the
			// `v: "…"` line.
			lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
			if len(lines) != 1 {
				t.Fatalf("emit produced %d lines, want 1\nbytes: %q", len(lines), out)
			}
			var got map[string]any
			if err := yaml.Unmarshal(out, &got); err != nil {
				t.Fatalf("unmarshal: %v\nemit was: %s", err, out)
			}
			if got["v"] != tc.in {
				t.Fatalf("round-trip: got %q want %q\nemit: %s", got["v"], tc.in, out)
			}
		})
	}
}

// TestEmit_NFCNormalisation checks that an NFD-encoded string (e + combining
// acute, two code points) becomes NFC (é, one code point) on disk. We use
// the byte form to assert exactly which sequence ended up between the
// quotes.
func TestEmit_NFCNormalisation(t *testing.T) {
	const nfd = "Café" // "Cafe" + combining acute → "Café"
	const nfc = "Café"
	out, err := Emit(Map(Pair{"name", Str(nfd)}))
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	want := "name: \"" + nfc + "\"\n"
	if string(out) != want {
		t.Fatalf("NFC normalisation:\n got %q\nwant %q", out, want)
	}
}

// TestEmit_KeySortOrderIsStable: same input pairs in different orders
// must produce identical bytes.
func TestEmit_KeySortOrderIsStable(t *testing.T) {
	a := Map(
		Pair{"zeta", Str("z")},
		Pair{"alpha", Str("a")},
		Pair{"mu", Str("m")},
	)
	b := Map(
		Pair{"mu", Str("m")},
		Pair{"alpha", Str("a")},
		Pair{"zeta", Str("z")},
	)
	ao, err := Emit(a)
	if err != nil {
		t.Fatalf("emit a: %v", err)
	}
	bo, err := Emit(b)
	if err != nil {
		t.Fatalf("emit b: %v", err)
	}
	if string(ao) != string(bo) {
		t.Fatalf("sort instability:\n  a=%q\n  b=%q", ao, bo)
	}
	// And: keys appear in alpha order in the output.
	wantOrder := []string{"alpha:", "mu:", "zeta:"}
	for _, want := range wantOrder {
		if !strings.Contains(string(ao), want) {
			t.Fatalf("missing %q in output:\n%s", want, ao)
		}
	}
	if strings.Index(string(ao), "alpha:") > strings.Index(string(ao), "zeta:") {
		t.Fatalf("alpha must precede zeta:\n%s", ao)
	}
}

// TestEmit_TimestampFormat pins the canonical timestamp shape:
// quoted, RFC3339, ms precision, Z suffix, UTC.
func TestEmit_TimestampFormat(t *testing.T) {
	loc, _ := time.LoadLocation("Australia/Sydney")
	in := time.Date(2026, 5, 9, 14, 22, 0, 123_456_789, loc) // not UTC; nanos
	out, err := Emit(Map(Pair{"created_at", Time(in)}))
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	// Sydney is UTC+10 in May (no DST), so the UTC equivalent is 04:22.
	want := "created_at: \"2026-05-09T04:22:00.123Z\"\n"
	if string(out) != want {
		t.Fatalf("timestamp format:\n got %q\nwant %q", out, want)
	}
}

// TestEmit_ByteIdentical_AcrossInvocations: emit the same input twice,
// bytes must match. This is the property the content-hash relies on.
func TestEmit_ByteIdentical_AcrossInvocations(t *testing.T) {
	build := func() Node {
		return Map(
			Pair{"uuid", Str("0190c2a3-7f3a-7b2c-8b21-aaaaaaaaaaaa")},
			Pair{"number", Int(7)},
			Pair{"state", Str("in_progress")},
			Pair{"assignee", Str("geoff")},
			Pair{"created_at", Time(time.Date(2026, 5, 9, 14, 22, 0, 0, time.UTC))},
			Pair{"tags", Seq(Str("p1"), Str("security"))},
			Pair{"feature", Map(
				Pair{"label", Str("auth-rewrite")},
				Pair{"uuid", Str("0190c2a3-7f3a-7b2c-8b21-dddddddddddd")},
			)},
			Pair{"relations", Map(
				Pair{"blocks", Seq(
					Map(
						Pair{"label", Str("MINI-12")},
						Pair{"uuid", Str("0190c2a3-7f3a-7b2c-8b21-cccccccccccc")},
					),
				)},
				Pair{"duplicate_of", Seq()},
				Pair{"relates_to", Seq()},
			)},
		)
	}
	a, err := Emit(build())
	if err != nil {
		t.Fatalf("emit a: %v", err)
	}
	b, err := Emit(build())
	if err != nil {
		t.Fatalf("emit b: %v", err)
	}
	if string(a) != string(b) {
		t.Fatalf("emit not byte-stable across calls:\n  a=%q\n  b=%q", a, b)
	}
}

// TestEmit_TrailingNewline: the output ends with exactly one '\n',
// regardless of how the writers happened to terminate their last line.
func TestEmit_TrailingNewline(t *testing.T) {
	out, err := Emit(Map(Pair{"x", Str("y")}))
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if !strings.HasSuffix(string(out), "\n") {
		t.Fatalf("missing trailing newline: %q", out)
	}
	if strings.HasSuffix(string(out), "\n\n") {
		t.Fatalf("doubled trailing newline: %q", out)
	}
}

// TestEmit_LFOnly: there must be no '\r' in the output.
func TestEmit_LFOnly(t *testing.T) {
	out, err := Emit(Map(
		Pair{"line1", Str("hello")},
		Pair{"line2", Str("world")},
	))
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if strings.ContainsRune(string(out), '\r') {
		t.Fatalf("CR found in output: %q", out)
	}
}

// TestEmit_SeqOfMaps validates the bullet+indent shape used by
// `relations.blocks` and `redirects.yaml`.
func TestEmit_SeqOfMaps(t *testing.T) {
	root := Map(Pair{"items", Seq(
		Map(Pair{"label", Str("MINI-7")}, Pair{"uuid", Str("u1")}),
		Map(Pair{"label", Str("MINI-12")}, Pair{"uuid", Str("u2")}),
	)})
	out, err := Emit(root)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	var got map[string]any
	if err := yaml.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v\nemit: %s", err, out)
	}
	items, ok := got["items"].([]any)
	if !ok {
		t.Fatalf("items not a slice: %T", got["items"])
	}
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	first, _ := items[0].(map[string]any)
	if first["label"] != "MINI-7" || first["uuid"] != "u1" {
		t.Fatalf("first item wrong: %+v", first)
	}
}

// TestEmit_EmptyCollections: empty seq → []; empty map → {}.
// Phase 3 import will rely on these specific shapes.
func TestEmit_EmptyCollections(t *testing.T) {
	out, err := Emit(Map(
		Pair{"empty_seq", Seq()},
		Pair{"empty_map", Map()},
	))
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if !strings.Contains(string(out), "empty_seq: []") {
		t.Fatalf("expected empty seq as []: %s", out)
	}
	if !strings.Contains(string(out), "empty_map: {}") {
		t.Fatalf("expected empty map as {}: %s", out)
	}
}

// TestEmit_ControlCharsEscaped: the bell character (\x07) must come out
// as `\x07` in the YAML — we don't want raw bytes in the output.
func TestEmit_ControlCharsEscaped(t *testing.T) {
	in := "before\x07after"
	out, err := Emit(Map(Pair{"v", Str(in)}))
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if !strings.Contains(string(out), `\x07`) {
		t.Fatalf("expected \\x07 in output: %q", out)
	}
}
