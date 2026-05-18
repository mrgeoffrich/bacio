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
		Question:    "Which layout do you prefer?",
		Header:      "Layout",
		MultiSelect: MultiSelectFlag(false),
		Options: []QuestionOption{
			{Label: "Stacked", Description: "Vertical stack — single column.", Preview: "[header]\n[body]\n[footer]"},
			{Label: "Sidebar", Description: "Two-column with persistent nav.", Preview: "[nav] | [body]\n      | [footer]"},
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
		MultiSelect: MultiSelectFlag(true),
		Options: []QuestionOption{
			{Label: "Cheese", Description: "Mozzarella.", Preview: "🧀"},
			{Label: "Pepperoni", Description: "Spicy cured pork."},
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
		Question:    "Pick one",
		Header:      "X",
		MultiSelect: MultiSelectFlag(false),
		Options: []QuestionOption{
			{Label: "A", Description: "first option", Preview: big},
			{Label: "B", Description: "second option"},
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
		Question:    "Which?",
		Header:      "X",
		MultiSelect: MultiSelectFlag(false),
		Options: []QuestionOption{
			{Label: "A", Description: "first", Preview: "line1\nline2\n\tindented\r\nwindows"},
			{Label: "B", Description: "second"},
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
		Question:    "Which?",
		Header:      "X",
		MultiSelect: MultiSelectFlag(false),
		Options: []QuestionOption{
			{Label: "A", Description: "first", Preview: "ok\x01nope"},
			{Label: "B", Description: "second"},
		},
	}}}
	err := ValidateQuestionPayload(p)
	if err == nil || !strings.Contains(err.Error(), "disallowed control character") {
		t.Fatalf("expected control-char error, got %v", err)
	}
}

// TestValidateQuestionPayload_MultiSelectRequired mirrors native
// AskUserQuestion's requirement that the agent explicitly states
// single-vs-multi-select intent rather than relying on the absent
// default. nil *bool is the signal an agent forgot the field.
func TestValidateQuestionPayload_MultiSelectRequired(t *testing.T) {
	p := QuestionPayload{Questions: []QuestionItem{{
		Question: "Which?",
		Header:   "X",
		// MultiSelect deliberately unset.
		Options: []QuestionOption{
			{Label: "A", Description: "first"},
			{Label: "B", Description: "second"},
		},
	}}}
	err := ValidateQuestionPayload(p)
	if err == nil || !strings.Contains(err.Error(), "multiSelect is required") {
		t.Fatalf("expected multiSelect-required error, got %v", err)
	}
}

// TestValidateQuestionPayload_OptionDescriptionRequired mirrors
// native's required-list on option (label + description). A
// missing description is rejected — bare labels with no fine-print
// are unhelpful in the bacio modal where the supervisor doesn't
// have the agent's transcript to fill the context gap.
func TestValidateQuestionPayload_OptionDescriptionRequired(t *testing.T) {
	p := QuestionPayload{Questions: []QuestionItem{{
		Question:    "Which?",
		Header:      "X",
		MultiSelect: MultiSelectFlag(false),
		Options: []QuestionOption{
			{Label: "A"}, // missing description
			{Label: "B", Description: "second"},
		},
	}}}
	err := ValidateQuestionPayload(p)
	if err == nil || !strings.Contains(err.Error(), "option description is required") {
		t.Fatalf("expected description-required error, got %v", err)
	}
}
