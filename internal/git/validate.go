package git

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// maxRemoteURLLen caps the sync remote URL. Real git URLs are short;
// anything past 2 KiB is a paste accident or an attack payload.
const maxRemoteURLLen = 2 << 10

var (
	// remoteSchemeRe matches a leading URL scheme, e.g. "https://".
	remoteSchemeRe = regexp.MustCompile(`^([a-zA-Z][a-zA-Z0-9+.-]*)://`)
	// remoteSCPRe matches scp-style git syntax: [user@]host:path. The
	// host part carries no '/' and no ':' so a "scheme://" URL never
	// reaches this branch.
	remoteSCPRe = regexp.MustCompile(`^([^/:]+@)?[^/:]+:.+`)
)

// allowedRemoteSchemes is the allowlist of URL schemes a bacio sync
// remote may use. Notably absent: `file` and git's transport-helper
// schemes (`ext`, `transport`, `fd`), which can execute arbitrary
// commands when git resolves the remote.
var allowedRemoteSchemes = map[string]bool{
	"http":  true,
	"https": true,
	"ssh":   true,
	"git":   true,
}

// ValidateRemoteURL rejects sync remote URLs that git would treat as
// something other than a plain repository address. The dangerous
// cases this closes:
//
//   - git transport-helper syntax (`ext::sh -c '...'`), which spawns
//     an arbitrary shell command;
//   - a leading '-', which git parses as a command-line flag
//     (argument injection);
//   - non-repository schemes such as `file://`.
//
// Accepted: http(s)://, ssh://, git:// URLs, scp-style user@host:path,
// and absolute local filesystem paths. Callers that allow an empty
// remote (it's optional on `bacio sync init`) must guard for "" before
// calling — an empty string is rejected here.
func ValidateRemoteURL(remote string) error {
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return fmt.Errorf("remote URL is required")
	}
	if len(remote) > maxRemoteURLLen {
		return fmt.Errorf("remote URL too long: %d bytes, max %d", len(remote), maxRemoteURLLen)
	}
	for _, r := range remote {
		if r < 0x20 || r == 0x7F {
			return fmt.Errorf("remote URL contains a disallowed control character (U+%04X)", r)
		}
	}
	// Argument injection: git reads a leading '-' as a flag.
	if strings.HasPrefix(remote, "-") {
		return fmt.Errorf("remote URL %q must not start with '-'", remote)
	}
	// scheme:// form — scheme must be on the allowlist.
	if m := remoteSchemeRe.FindStringSubmatch(remote); m != nil {
		scheme := strings.ToLower(m[1])
		if !allowedRemoteSchemes[scheme] {
			return fmt.Errorf("remote URL scheme %q is not allowed (use http, https, ssh, or git)", scheme)
		}
		return nil
	}
	// No scheme. `::` here is git transport-helper syntax (ext::,
	// transport::, fd::, …) — the arbitrary-command-execution vector.
	if strings.Contains(remote, "::") {
		return fmt.Errorf("remote URL %q uses git transport-helper syntax, which is not allowed", remote)
	}
	// scp-style [user@]host:path, or an absolute local path.
	if remoteSCPRe.MatchString(remote) {
		return nil
	}
	if filepath.IsAbs(remote) {
		return nil
	}
	return fmt.Errorf("remote URL %q is not a recognised git URL (use http(s)://, ssh://, git://, scp-style user@host:path, or an absolute local path)", remote)
}
