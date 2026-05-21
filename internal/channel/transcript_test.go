package channel

import (
	"bytes"
	"strings"
	"testing"
)

// TestCapTranscript confirms a small transcript passes through
// byte-identical and an over-cap transcript is truncated under
// TranscriptCap with a footer pointing at the source path.
func TestCapTranscript(t *testing.T) {
	// (a) a small input passes through byte-identical.
	small := []byte(strings.Join([]string{
		`{"type":"user","message":{"role":"user","content":"hi"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"reply"}]}}`,
	}, "\n"))
	if got := CapTranscript(small, "/path/to/raw.jsonl"); !bytes.Equal(got, small) {
		t.Errorf("small transcript should pass through unchanged; got %d bytes, want %d", len(got), len(small))
	}

	// (b) an over-cap input is truncated to <= TranscriptCap and ends
	// with a footer naming the source path and the omitted byte count.
	big := bytes.Repeat([]byte("x"), TranscriptCap+128*1024)
	capped := CapTranscript(big, "/path/to/raw.jsonl")
	if len(capped) > TranscriptCap {
		t.Errorf("capped transcript %d bytes exceeds cap %d", len(capped), TranscriptCap)
	}
	s := string(capped)
	if !strings.Contains(s, "transcript truncated") {
		t.Errorf("oversized transcript must carry a truncation footer:\n%s", s[len(s)-300:])
	}
	if !strings.Contains(s, "/path/to/raw.jsonl") {
		t.Errorf("truncation footer must point at the raw transcript path")
	}
	// The prefix before the footer must be a verbatim slice of the input.
	footerIdx := strings.Index(s, "\n[bacio: transcript truncated")
	if footerIdx < 0 {
		t.Fatalf("footer marker not found in capped output")
	}
	if !bytes.Equal(capped[:footerIdx], big[:footerIdx]) {
		t.Errorf("kept prefix is not a verbatim slice of the input")
	}
}
