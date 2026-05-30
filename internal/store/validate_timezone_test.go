package store

import "testing"

// TestValidateTimezone exercises the BACI-312 ui.timezone validator. The
// rule is a shape check on IANA zone names (one or more slash-separated
// components), not a database lookup — the Go binary stays tz-agnostic,
// so we accept any structurally valid name and let the browser's Intl be
// the final arbiter of whether the zone resolves.
func TestValidateTimezone(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantErr bool
		want    string
	}{
		// Happy cases.
		{"area/location", "Australia/Sydney", false, "Australia/Sydney"},
		{"utc", "UTC", false, "UTC"},
		{"three-part", "America/Argentina/Buenos_Aires", false, "America/Argentina/Buenos_Aires"},
		{"etc offset plus", "Etc/GMT+10", false, "Etc/GMT+10"},
		{"etc offset minus", "Etc/GMT-5", false, "Etc/GMT-5"},
		{"hyphen component", "America/Port-au-Prince", false, "America/Port-au-Prince"},
		{"trims whitespace", "  UTC  ", false, "UTC"},

		// Reject cases.
		{"empty", "", true, ""},
		{"whitespace only", "   ", true, ""},
		{"embedded space", "Australia/ Sydney", true, ""},
		{"leading slash", "/Australia/Sydney", true, ""},
		{"trailing slash", "Australia/Sydney/", true, ""},
		{"leading digit component", "Australia/9Sydney", true, ""},
		{"control char", "UTC\x00", true, ""},
		{"junk", "not a zone!", true, ""},
		{"path traversal", "../etc/passwd", true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ValidateTimezone(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ValidateTimezone(%q) = %q, want error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidateTimezone(%q) errored: %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("ValidateTimezone(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
