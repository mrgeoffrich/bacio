package model

import (
	"strings"
	"testing"
)

// TestValidateQuestionPayload_PreviewHappyPath asserts a single-select
// question with a preview on each option validates cleanly. This is the
// canonical "compare two designs side-by-side" call shape.
func TestValidateQuestionPayload_PreviewHappyPath(t *testing.T) {
	p := QuestionPayload{Questions: []QuestionItem{{
		Question: "Which layout do you prefer?",
		Header:   "Layout",
		Options: []QuestionOption{
			{Label: "Stacked", Preview: "[header]\n[body]\n[footer]"},
			{Label: "Sidebar", Preview: "[nav] | [body]\n      | [footer]"},
		},
	}}}
	if err := ValidateQuestionPayload(p); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestValidateQuestionPayload_PreviewRejectedOnMultiSelect mirrors
// native AskUserQuestion's "previews are only supported for
// single-select" constraint.
func TestValidateQuestionPayload_PreviewRejectedOnMultiSelect(t *testing.T) {
	p := QuestionPayload{Questions: []QuestionItem{{
		Question:    "Pick the toppings",
		Header:      "Toppings",
		MultiSelect: true,
		Options: []QuestionOption{
			{Label: "Cheese", Preview: "🧀"},
			{Label: "Pepperoni"},
		},
	}}}
	err := ValidateQuestionPayload(p)
	if err == nil {
		t.Fatal("expected an error for preview on multi-select; got nil")
	}
	if !strings.Contains(err.Error(), "preview is not allowed on multi-select") {
		t.Errorf("wrong error: %v", err)
	}
}

// TestValidateQuestionPayload_PreviewLengthCapped rejects an oversize
// preview at MaxQuestionPreviewLen + 1.
func TestValidateQuestionPayload_PreviewLengthCapped(t *testing.T) {
	big := strings.Repeat("a", MaxQuestionPreviewLen+1)
	p := QuestionPayload{Questions: []QuestionItem{{
		Question: "Pick one",
		Header:   "X",
		Options: []QuestionOption{
			{Label: "A", Preview: big},
			{Label: "B"},
		},
	}}}
	err := ValidateQuestionPayload(p)
	if err == nil || !strings.Contains(err.Error(), "too long") {
		t.Fatalf("expected too-long error, got %v", err)
	}
}

// TestValidateQuestionPayload_PreviewAllowsNewlines confirms the
// multiline string validator accepts \n / \t / \r — the whole point
// of preview content is multi-line layout.
func TestValidateQuestionPayload_PreviewAllowsNewlines(t *testing.T) {
	p := QuestionPayload{Questions: []QuestionItem{{
		Question: "Which?",
		Header:   "X",
		Options: []QuestionOption{
			{Label: "A", Preview: "line1\nline2\n\tindented\r\nwindows"},
			{Label: "B"},
		},
	}}}
	if err := ValidateQuestionPayload(p); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestValidateQuestionPayload_PreviewRejectsControlChars makes sure
// non-whitespace C0 controls are still rejected in preview content
// (otherwise an agent could smuggle escape sequences that scramble
// the modal render).
func TestValidateQuestionPayload_PreviewRejectsControlChars(t *testing.T) {
	p := QuestionPayload{Questions: []QuestionItem{{
		Question: "Which?",
		Header:   "X",
		Options: []QuestionOption{
			{Label: "A", Preview: "ok\x01nope"},
			{Label: "B"},
		},
	}}}
	err := ValidateQuestionPayload(p)
	if err == nil || !strings.Contains(err.Error(), "disallowed control character") {
		t.Fatalf("expected control-char error, got %v", err)
	}
}
