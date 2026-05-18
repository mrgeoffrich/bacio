package store

import "testing"

func TestValidateWorktreeSlug_Accepts(t *testing.T) {
	for _, s := range []string{
		"bacio",
		"bacio-baci-63",
		"my_wt",
		"my.feature.branch",
		"a",
		"0",
	} {
		out, err := ValidateWorktreeSlug(s)
		if err != nil {
			t.Errorf("ValidateWorktreeSlug(%q): unexpected error %v", s, err)
		}
		if out != s {
			t.Errorf("ValidateWorktreeSlug(%q): got %q", s, out)
		}
	}
}

func TestValidateWorktreeSlug_Rejects(t *testing.T) {
	for _, s := range []string{
		"",
		" ",
		"-leading-dash",
		".leading-dot",
		"_leading-underscore",
		"has space",
		"has/slash",
		"UPPERCASE",
	} {
		if _, err := ValidateWorktreeSlug(s); err == nil {
			t.Errorf("ValidateWorktreeSlug(%q): want error, got nil", s)
		}
	}
}
