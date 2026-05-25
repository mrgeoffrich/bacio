package store

import "testing"

// TestValidateEmoji exercises the BACI-172 per-feature emoji validator.
// The rule is "empty or exactly one grapheme cluster" — strict enough
// to reject "FEATURE" / "ab" abuse, lax enough to accept ZWJ sequences
// and regional-indicator flag pairs that count as one cluster.
func TestValidateEmoji(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantErr bool
	}{
		// Happy cases.
		{"empty", "", false},
		{"basic emoji", "🔐", false},
		{"butterfly", "🦋", false},
		{"ZWJ family", "👨‍👩‍👧", false}, // single grapheme cluster
		{"flag", "🇦🇺", false},                    // two regional indicators = one cluster
		{"single letter", "a", false},             // one cluster — by design, agents can read the rule from the schema and JSON callers see the realistic emoji example

		// Reject cases.
		{"multi-cluster word", "FEATURE", true},
		{"two letters", "ab", true},
		{"two emoji", "🔐🪲", true},
		{"emoji plus letter", "🔐a", true},
		{"embedded NUL", "🔐\x00", true},
		{"embedded tab", "🔐\t", true},
		{"newline", "\n", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ValidateEmoji(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ValidateEmoji(%q) = %q, want error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidateEmoji(%q) returned unexpected error: %v", tc.in, err)
			}
			if got != tc.in {
				t.Fatalf("ValidateEmoji(%q) returned %q; want passthrough", tc.in, got)
			}
		})
	}
}
