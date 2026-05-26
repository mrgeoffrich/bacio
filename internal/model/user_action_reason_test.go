package model

import "testing"

func TestParseUserActionReason(t *testing.T) {
	cases := []struct {
		in   string
		want UserActionReasonType
	}{
		{"user_question", UserActionReasonQuestion},
		{"user-question", UserActionReasonQuestion},
		{"User Question", UserActionReasonQuestion},
		{"USER_QUESTION", UserActionReasonQuestion},
		{"  user_question  ", UserActionReasonQuestion},
		{"user_manual_review", UserActionReasonManualReview},
		{"user-manual-review", UserActionReasonManualReview},
		{"User Manual Review", UserActionReasonManualReview},
	}
	for _, c := range cases {
		got, err := ParseUserActionReason(c.in)
		if err != nil {
			t.Errorf("ParseUserActionReason(%q) error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseUserActionReason(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseUserActionReasonRejectsUnknown(t *testing.T) {
	bad := []string{
		"",
		"user",
		"question",
		"manual_review",
		"in_progress",
		"random_other_string",
		"user_question_extra",
	}
	for _, in := range bad {
		if got, err := ParseUserActionReason(in); err == nil {
			t.Errorf("ParseUserActionReason(%q) = %q, want error", in, got)
		}
	}
}

func TestAllUserActionReasonsReturnsCopy(t *testing.T) {
	a := AllUserActionReasons()
	if len(a) != 2 {
		t.Fatalf("AllUserActionReasons() len = %d, want 2", len(a))
	}
	a[0] = "tampered"
	b := AllUserActionReasons()
	if b[0] == "tampered" {
		t.Fatal("AllUserActionReasons() leaked its internal slice")
	}
}
