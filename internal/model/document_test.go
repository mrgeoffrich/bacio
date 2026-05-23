package model

import "testing"

func TestParseDocumentType_NewBACI115Types(t *testing.T) {
	cases := []struct {
		in   string
		want DocumentType
	}{
		{"plan", DocTypePlan},
		{"transcript", DocTypeTranscript},
		{"rendered_transcript", DocTypeRenderedTranscript},
		// The user's preferred CLI form `rendered-transcript` must
		// normalise to the canonical underscore form.
		{"rendered-transcript", DocTypeRenderedTranscript},
		{"rendered transcript", DocTypeRenderedTranscript},
		{"RENDERED-TRANSCRIPT", DocTypeRenderedTranscript},
		{"review", DocTypeReview},
		// Existing types still parse — BACI-115 extends, doesn't replace.
		{"project_complete", DocTypeProjectComplete},
		{"user-docs", DocTypeUserDocs},
	}
	for _, c := range cases {
		got, err := ParseDocumentType(c.in)
		if err != nil {
			t.Errorf("ParseDocumentType(%q) returned err: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseDocumentType(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseDocumentType_Unknown(t *testing.T) {
	if _, err := ParseDocumentType("definitely-not-a-type"); err == nil {
		t.Fatalf("ParseDocumentType returned nil error for unknown type")
	}
}

func TestAllDocumentTypes_IncludesBACI115Types(t *testing.T) {
	have := map[DocumentType]bool{}
	for _, t := range AllDocumentTypes() {
		have[t] = true
	}
	for _, want := range []DocumentType{
		DocTypePlan,
		DocTypeTranscript,
		DocTypeRenderedTranscript,
		DocTypeReview,
	} {
		if !have[want] {
			t.Errorf("AllDocumentTypes() missing %q", want)
		}
	}
}

func TestDocTypeInlinedInBrief(t *testing.T) {
	cases := []struct {
		t    DocumentType
		want bool
	}{
		{DocTypePlan, true},
		{DocTypeReview, true},
		// Every other type is NOT inlined — these are the cases that
		// matter for BACI-115 (transcript was the bug; the rest just
		// confirm the rule is narrow).
		{DocTypeTranscript, false},
		{DocTypeRenderedTranscript, false},
		{DocTypeProjectComplete, false},
		{DocTypeProjectInPlanning, false},
		{DocTypeProjectInProgress, false},
		{DocTypeUserDocs, false},
		{DocTypeVendorDocs, false},
		{DocTypeArchitecture, false},
		{DocTypeDesigns, false},
		{DocTypeTestingPlans, false},
		// An unknown/empty type is conservatively not inlined.
		{DocumentType(""), false},
		{DocumentType("made_up"), false},
	}
	for _, c := range cases {
		if got := DocTypeInlinedInBrief(c.t); got != c.want {
			t.Errorf("DocTypeInlinedInBrief(%q) = %v, want %v", c.t, got, c.want)
		}
	}
}

func TestIsBacioTranscriptFilename(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		// Real attach_transcript output.
		{"bacio-transcript-BACI-115-agent-abc123.jsonl", true},
		{"bacio-transcript-MINI-1-agent-x.jsonl", true},
		// Whitespace tolerated.
		{"  bacio-transcript-BACI-1-agent-z.jsonl  ", true},
		// Missing pieces.
		{"bacio-transcript-BACI-1.jsonl", false},                  // no -agent-
		{"transcript-BACI-1-agent-x.jsonl", false},                // no prefix
		{"bacio-transcript-BACI-1-agent-x.md", false},             // wrong suffix
		{"bacio-transcript-BACI-1-agent-x.jsonl.bak", false},      // wrong suffix
		{"something-else.jsonl", false},
		{"", false},
	}
	for _, c := range cases {
		if got := IsBacioTranscriptFilename(c.in); got != c.want {
			t.Errorf("IsBacioTranscriptFilename(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
