package store

import (
	"strings"
	"testing"
)

// TestValidateCustomerImpact exercises the BACI-349 customer_impact
// validator — a single-line, optional, title-capped field. The rule is
// "blank is OK (the no-impact state), trimmed, no control characters / no
// embedded newlines, capped at maxTitleLen runes". Mirrors the shape of
// TestValidateEmoji.
func TestValidateCustomerImpact(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string // expected returned value on success (ignored when wantErr)
		wantErr bool
	}{
		// Happy cases.
		{"empty is the no-impact state", "", "", false},
		{"plain line", "Login no longer 500s on Safari", "Login no longer 500s on Safari", false},
		{"trims surrounding whitespace", "  trimmed me  ", "trimmed me", false},
		{"whitespace-only collapses to empty", "   ", "", false},
		{"at the cap", strings.Repeat("a", maxTitleLen), strings.Repeat("a", maxTitleLen), false},

		// Rejections.
		{"over the cap", strings.Repeat("a", maxTitleLen+1), "", true},
		{"embedded newline", "line one\nline two", "", true},
		{"embedded tab", "col\tcol", "", true},
		{"NUL byte", "nul\x00here", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ValidateCustomerImpact(tc.in, "customer_impact")
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ValidateCustomerImpact(%q) = %q, nil; want error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidateCustomerImpact(%q) unexpected error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("ValidateCustomerImpact(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
