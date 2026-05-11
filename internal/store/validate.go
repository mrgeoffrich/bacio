package store

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Defensive validators that run at every mutation boundary so hallucinated
// or malformed agent input can't poison the database or the audit log.
// The CLI may run a subset earlier (for fast-fail UX), but the store is the
// single source of truth: any caller — CLI, TUI, future API — goes through
// these.
//
// The threat model is "agents pasting nonsense", not malicious users. We
// reject things that break terminal rendering and audit-log readability
// (control chars, NULs, embedded newlines in single-line fields), enforce
// UTF-8, and cap absurd lengths. We deliberately don't try to filter
// unicode bidi overrides or zero-width chars — that's overkill for a
// local tracker.

// Limits used by the validators below. Generous enough that legitimate
// content fits, tight enough that a runaway paste fails fast.
const (
	maxTitleLen    = 200
	maxNameLen     = 80
	maxSlugLen     = 60
	maxFilenameLen = 200
	maxBodyBytes   = 1 << 20 // 1 MiB
	maxURLBytes    = 2 << 10 // 2 KiB
)

var slugRule = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// ValidateTitle is for single-line titles (issues, features). Required,
// trimmed, no control characters, no embedded newlines, capped at
// maxTitleLen runes.
func ValidateTitle(s, field string) (string, error) {
	return validateSingleLine(s, field, maxTitleLen, true)
}

// ValidateName is for short single-line identifiers attributed to people
// or agents (assignee, comment author). Same rules as a title, smaller cap.
func ValidateName(s, field string) (string, error) {
	return validateSingleLine(s, field, maxNameLen, true)
}

// ValidateActor validates a `--user` value before it lands in the audit
// log. Same shape as ValidateName.
func ValidateActor(s string) (string, error) {
	return validateSingleLine(s, "user", maxNameLen, true)
}

// ValidateSlug enforces the kebab-case slug shape used for feature slugs.
// Must start with [a-z0-9] and contain only [a-z0-9-]; capped at maxSlugLen.
func ValidateSlug(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", fmt.Errorf("slug is required")
	}
	if len(s) > maxSlugLen {
		return "", fmt.Errorf("slug too long: %d chars, max %d", len(s), maxSlugLen)
	}
	if !slugRule.MatchString(s) {
		return "", fmt.Errorf("slug %q must be kebab-case (lowercase letters, digits, hyphens; starting with a letter or digit)", s)
	}
	return s, nil
}

// ValidateBody is for multi-line free text (issue/feature description,
// comment body, document content). Optional by default — pass required=true
// to reject empty input. Newlines, tabs, and carriage returns are allowed;
// other control characters and NUL are rejected. Capped at maxBodyBytes.
func ValidateBody(s, field string, required bool) (string, error) {
	if !utf8.ValidString(s) {
		return "", fmt.Errorf("%s is not valid UTF-8", field)
	}
	if len(s) > maxBodyBytes {
		return "", fmt.Errorf("%s too long: %d bytes, max %d", field, len(s), maxBodyBytes)
	}
	for i, r := range s {
		if isDisallowedControlMulti(r) {
			return "", fmt.Errorf("%s contains a disallowed control character (U+%04X) at byte %d", field, r, i)
		}
	}
	if required && strings.TrimSpace(s) == "" {
		return "", fmt.Errorf("%s is required", field)
	}
	return s, nil
}

// ValidatePRURLStrict checks that raw is a non-empty, length-capped,
// control-char-free, http(s) URL with a host. Used at every entry point
// that stores a PR URL (CLI, store-layer fallback) so a future caller
// bypassing the CLI still can't slip in `javascript:` or schemeless junk.
func ValidatePRURLStrict(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("URL is required")
	}
	if len(raw) > maxURLBytes {
		return "", fmt.Errorf("URL too long: %d bytes, max %d", len(raw), maxURLBytes)
	}
	for _, r := range raw {
		if isDisallowedControlSingle(r) {
			return "", fmt.Errorf("URL contains a disallowed control character (U+%04X)", r)
		}
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("URL must use http or https scheme")
	}
	if u.Host == "" {
		return "", fmt.Errorf("URL must include a host")
	}
	return raw, nil
}

// ValidateDocFilenameStrict tightens the older filename check: still rejects
// `/`, `\`, NUL, but additionally rejects all control characters, leading
// or trailing whitespace, the special names `.` and `..`, and caps length.
func ValidateDocFilenameStrict(name string) (string, error) {
	if !utf8.ValidString(name) {
		return "", fmt.Errorf("filename is not valid UTF-8")
	}
	if name == "" {
		return "", fmt.Errorf("filename is required")
	}
	if strings.TrimSpace(name) != name {
		return "", fmt.Errorf("filename must not have leading or trailing whitespace")
	}
	if len(name) > maxFilenameLen {
		return "", fmt.Errorf("filename too long: %d chars, max %d", len(name), maxFilenameLen)
	}
	if name == "." || name == ".." {
		return "", fmt.Errorf("filename %q is not allowed", name)
	}
	for _, r := range name {
		switch {
		case r == '/' || r == '\\':
			return "", fmt.Errorf("filename must not contain '/' or '\\'")
		case isDisallowedControlSingle(r):
			return "", fmt.Errorf("filename contains a disallowed control character (U+%04X)", r)
		}
	}
	return name, nil
}

func validateSingleLine(s, field string, maxLen int, required bool) (string, error) {
	if !utf8.ValidString(s) {
		return "", fmt.Errorf("%s is not valid UTF-8", field)
	}
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		if required {
			return "", fmt.Errorf("%s is required", field)
		}
		return "", nil
	}
	if len(trimmed) > maxLen {
		return "", fmt.Errorf("%s too long: %d chars, max %d", field, len(trimmed), maxLen)
	}
	for _, r := range trimmed {
		if isDisallowedControlSingle(r) {
			return "", fmt.Errorf("%s contains a disallowed control character (U+%04X)", field, r)
		}
	}
	return trimmed, nil
}

// isDisallowedControlSingle reports whether r should be rejected from
// single-line fields. Rejects all C0 (U+0000–U+001F) and DEL (U+007F).
func isDisallowedControlSingle(r rune) bool {
	return (r >= 0x00 && r <= 0x1F) || r == 0x7F
}

// isDisallowedControlMulti reports whether r should be rejected from
// multi-line free-text fields. Same as the single-line set, but allows
// tab (U+0009), newline (U+000A), and carriage return (U+000D).
func isDisallowedControlMulti(r rune) bool {
	if r == '\t' || r == '\n' || r == '\r' {
		return false
	}
	return isDisallowedControlSingle(r)
}
