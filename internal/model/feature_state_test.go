package model

import "testing"

func TestParseFeatureState_AcceptsVariants(t *testing.T) {
	cases := map[string]FeatureState{
		"active":    FeatureStateActive,
		"Active":    FeatureStateActive,
		"ACTIVE":    FeatureStateActive,
		" active ":  FeatureStateActive,
		"done":      FeatureStateDone,
		"Done":      FeatureStateDone,
		"cancelled": FeatureStateCancelled,
		"Cancelled": FeatureStateCancelled,
	}
	for in, want := range cases {
		got, err := ParseFeatureState(in)
		if err != nil {
			t.Fatalf("ParseFeatureState(%q): unexpected error %v", in, err)
		}
		if got != want {
			t.Fatalf("ParseFeatureState(%q): got %q, want %q", in, got, want)
		}
	}
}

func TestParseFeatureState_RejectsInvalid(t *testing.T) {
	cases := []string{"", "archived", "in_progress", "needs_action", "foo"}
	for _, in := range cases {
		if _, err := ParseFeatureState(in); err == nil {
			t.Fatalf("ParseFeatureState(%q): expected error, got nil", in)
		}
	}
}

func TestAllFeatureStates_IsDefensiveCopy(t *testing.T) {
	a := AllFeatureStates()
	a[0] = "mutated"
	b := AllFeatureStates()
	if b[0] != FeatureStateActive {
		t.Fatalf("AllFeatureStates leaked a mutable slice; b[0] = %q", b[0])
	}
}
