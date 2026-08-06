package store

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/rivo/uniseg"
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
	maxBodyBytes   = 10 << 20 // 10 MiB — generous enough for a raw subagent transcript attachment (BACI-90)
	maxURLBytes    = 2 << 10  // 2 KiB
)

var slugRule = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// worktreeSlugRule allows the same kebab-case shape as a feature slug
// plus underscores and dots — worktree slugs are derived from
// directory basenames, which legitimately contain `_` and `.`. Still
// no whitespace, no slashes, no leading punctuation.
var worktreeSlugRule = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// maxWorktreeSlugLen is the upper bound for a worktree manifest's
// identity.slug. Wider than maxSlugLen (40 vs 60) — directory
// basenames can run a little long, especially with date prefixes.
const maxWorktreeSlugLen = 60

// ValidateWorktreeSlug enforces the worktree-slug shape used by
// `bacio worktree init` and ~/.bacio/worktrees.yaml. Lowercase
// kebab-with-dots-and-underscores; required; capped at
// maxWorktreeSlugLen.
func ValidateWorktreeSlug(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", fmt.Errorf("worktree slug is required")
	}
	if len(s) > maxWorktreeSlugLen {
		return "", fmt.Errorf("worktree slug too long: %d chars, max %d", len(s), maxWorktreeSlugLen)
	}
	if !worktreeSlugRule.MatchString(s) {
		return "", fmt.Errorf("worktree slug %q must match [a-z0-9][a-z0-9._-]* (lowercase letters, digits, '.', '_', '-'; starting with a letter or digit)", s)
	}
	return s, nil
}

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

// ValidateCustomerImpact is for the optional single-line BACI-349
// customer_impact field — a one-line statement of the change in the
// user's terms. Optional (blank returns "" — the "no user-facing change"
// state), trimmed, no control characters, no embedded newlines, capped at
// maxTitleLen runes (same generous cap as a title).
func ValidateCustomerImpact(s, field string) (string, error) {
	return validateSingleLine(s, field, maxTitleLen, false)
}

// ValidateActor validates an actor string before it lands in the audit
// log. Same shape as ValidateName.
func ValidateActor(s string) (string, error) {
	return validateSingleLine(s, "user", maxNameLen, true)
}

// ValidateAgentName validates the persistent agent-identity slug
// (`bacio agent register --agent <name>`). Free-form single-line —
// agents are expected to use a verb-animal@harness.host slug per
// SKILL.md, but the store doesn't enforce a shape; UNIQUE catches
// clashes between two agents that independently generate the same name.
func ValidateAgentName(s string) (string, error) {
	return validateSingleLine(s, "agent", maxNameLen, true)
}

// sessionIDPlaceholderLiterals is the explicit set of unsubstituted
// template tokens ValidateSessionID rejects up front (case-insensitive).
// Born from BACI-46: the bacio channel's setup-dispatch payload used to
// carry "$CLAUDE_CODE_SESSION_ID" as a literal example, and less careful
// agents copied the placeholder verbatim into the register call. The
// shape rule below (starts with `$`, contains `{`/`}`/`<`/`>`) is the
// belt-and-braces guard against future placeholders we haven't listed.
var sessionIDPlaceholderLiterals = []string{
	"$CLAUDE_CODE_SESSION_ID",
	"${CLAUDE_CODE_SESSION_ID}",
	"{{session_id}}",
	"{session_id}",
	"<session_id>",
}

// ValidateSessionID validates an external session id (e.g.
// CLAUDE_CODE_SESSION_ID, which is a UUID). Strict no-whitespace,
// printable ASCII only, capped at maxNameLen. Unlike most validators
// we do NOT TrimSpace — leading/trailing whitespace in a session id
// almost certainly indicates a copy-paste error and surfacing it as
// an explicit reject (per principle #4) beats silently accepting an
// almost-right id that won't match the original.
//
// On top of the shape rules above, ValidateSessionID also refuses
// obvious unsubstituted-placeholder strings (BACI-46): a literal
// "$CLAUDE_CODE_SESSION_ID", any of the explicit aliases in
// sessionIDPlaceholderLiterals, or — as a belt-and-braces guard —
// anything starting with `$` or containing template-bracket
// characters (`{`, `}`, `<`, `>`). The error message names the
// offending value and tells the caller what to pass instead.
func ValidateSessionID(s string) (string, error) {
	if !utf8.ValidString(s) {
		return "", fmt.Errorf("session_id is not valid UTF-8")
	}
	if s == "" {
		return "", fmt.Errorf("session_id is required")
	}
	if strings.TrimSpace(s) != s {
		return "", fmt.Errorf("session_id must not have leading or trailing whitespace")
	}
	if len(s) > maxNameLen {
		return "", fmt.Errorf("session_id too long: %d chars, max %d", len(s), maxNameLen)
	}
	for _, r := range s {
		switch {
		case r < 0x21 || r > 0x7E:
			return "", fmt.Errorf("session_id must be printable ASCII (no whitespace, no control chars)")
		}
	}
	if err := rejectSessionIDPlaceholder(s); err != nil {
		return "", err
	}
	return s, nil
}

// ValidateSessionUUID is the stricter session-id validator used at the
// agent-register entry points (CompleteRegistration, the SessionStart
// stub, the API registerAgent path). It first runs every ValidateSessionID
// shape rule — UTF-8, non-empty, no whitespace, printable ASCII, length
// cap, BACI-46 placeholder reject — then additionally requires a
// structurally valid UUID.
//
// The split exists deliberately: ValidateSessionID is shared by many
// callers that merely *reference* a session id (dispatch targets, todo
// rows, claim/release, ...), and test fixtures across the codebase use
// non-UUID ids pervasively. Tightening the shared validator would have a
// large blast radius. A register call, by contrast, is meant to carry
// $CLAUDE_CODE_SESSION_ID verbatim — a UUID — so the UUID requirement is
// correct there and only there (BACI-100).
func ValidateSessionUUID(s string) (string, error) {
	s, err := ValidateSessionID(s)
	if err != nil {
		return "", err
	}
	if _, err := uuid.Parse(s); err != nil {
		return "", fmt.Errorf("session_id %q is not a valid UUID; it must be copied verbatim from the bacio channel's pre-filled register payload (or $CLAUDE_CODE_SESSION_ID from your environment) — do not retype it by hand", s)
	}
	return s, nil
}

// rejectSessionIDPlaceholder returns a placeholder-shaped error when s
// looks like an unsubstituted template token. Split out from
// ValidateSessionID so the shape rule can be unit-tested independently.
func rejectSessionIDPlaceholder(s string) error {
	for _, lit := range sessionIDPlaceholderLiterals {
		if strings.EqualFold(s, lit) {
			return placeholderError(s)
		}
	}
	// Shape rule: an unsubstituted shell-style placeholder almost
	// always starts with `$`; a template-bracket placeholder uses one
	// of `{ } < >`. Neither of those characters appears in a real CC
	// session id (UUIDs today; printable-ASCII-only by the loop above
	// regardless), so the false-positive cost is zero.
	if strings.HasPrefix(s, "$") || strings.ContainsAny(s, "{}<>") {
		return placeholderError(s)
	}
	return nil
}

func placeholderError(s string) error {
	return fmt.Errorf("session_id %q looks like an unsubstituted placeholder; pass the real value the bacio channel pre-filled for you in the setup dispatch payload (or $CLAUDE_CODE_SESSION_ID from your environment)", s)
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

// maxEmojiBytes caps the byte length of the emoji field. A grapheme
// cluster covering every emoji currently registered comfortably fits in
// ~64 bytes (a long ZWJ flag sequence is on the order of 30); 128 bytes
// is generous enough that any single cluster passes but a runaway paste
// fails before uniseg even sees it.
const maxEmojiBytes = 128

// maxBranchNameLen caps the BACI-225 features.branch_name column. Git
// itself caps refnames at 255 bytes; we round to a friendlier 256.
const maxBranchNameLen = 256

// ValidateBranchName (BACI-225) enforces the per-feature integration
// branch column's shape. Empty input returns ("", nil) — the caller
// chooses what "absent" means (CreateFeature treats it as NULL; the
// pointer-on-update path uses an explicit nil to mean "no change" and
// a non-nil empty string to mean "clear"). Non-empty input is held to
// git's refname rules so a future `git checkout <branch>` won't choke:
//
//   - UTF-8 valid; length <= maxBranchNameLen bytes.
//   - No control characters (C0 + DEL) and no whitespace at all — refs
//     never carry spaces, tabs, newlines.
//   - No leading/trailing `/` or `.` — git rejects both.
//   - No embedded `..`, `@{`, `\`, `~`, `^`, `:`, `?`, `*`, `[` — every
//     one of these is forbidden by git-check-ref-format(1).
//   - No leading `-` — leading hyphens read as a CLI flag downstream.
//
// Validation is in-process: we deliberately do NOT shell out to
// `git check-ref-format`, so the validator works in tests without a
// working git binary and stays consistent across hosts.
func ValidateBranchName(s string) (string, error) {
	if !utf8.ValidString(s) {
		return "", fmt.Errorf("branch_name is not valid UTF-8")
	}
	if s == "" {
		// Empty is the caller-controlled "no value" sentinel — see the
		// doc-comment above. CreateFeature maps it to NULL on insert;
		// UpdateFeature's pointer-on-string distinguishes "no change"
		// (nil pointer) from "clear" (non-nil empty string).
		return "", nil
	}
	if len(s) > maxBranchNameLen {
		return "", fmt.Errorf("branch_name too long: %d bytes, max %d", len(s), maxBranchNameLen)
	}
	if strings.HasPrefix(s, "-") {
		return "", fmt.Errorf("branch_name %q must not start with '-'", s)
	}
	if strings.HasPrefix(s, "/") || strings.HasSuffix(s, "/") {
		return "", fmt.Errorf("branch_name %q must not start or end with '/'", s)
	}
	if strings.HasPrefix(s, ".") || strings.HasSuffix(s, ".") {
		return "", fmt.Errorf("branch_name %q must not start or end with '.'", s)
	}
	if strings.Contains(s, "..") {
		return "", fmt.Errorf("branch_name %q must not contain '..'", s)
	}
	if strings.Contains(s, "@{") {
		return "", fmt.Errorf("branch_name %q must not contain '@{'", s)
	}
	for _, r := range s {
		switch {
		case isDisallowedControlSingle(r):
			return "", fmt.Errorf("branch_name contains a disallowed control character (U+%04X)", r)
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			return "", fmt.Errorf("branch_name %q must not contain whitespace", s)
		case r == '~' || r == '^' || r == ':' || r == '?' || r == '*' || r == '[' || r == '\\':
			return "", fmt.Errorf("branch_name %q must not contain %q", s, string(r))
		}
	}
	return s, nil
}

// maxTimezoneLen caps the BACI-312 ui.timezone setting. IANA zone names
// top out well under this — the longest registered name
// ("America/Argentina/Buenos_Aires") is 31 chars — but 80 leaves room
// for any future addition without rejecting a legitimate zone.
const maxTimezoneLen = 80

// timezoneRule matches the shape of an IANA tz database name: one or
// more slash-separated components, each starting with a letter and
// otherwise made of letters, digits, '_', '-', '+'. Covers area/location
// (Australia/Sydney), three-part names (America/Argentina/Buenos_Aires),
// the fixed-offset Etc zones (Etc/GMT+10), and the single-word "UTC".
// Deliberately a shape check, not a database lookup: the Go binary stays
// tz-agnostic (BACI-312 computes local midnight browser-side), so we
// can't call time.LoadLocation to confirm the name resolves without
// embedding time/tzdata. The browser's Intl validates the real zone.
var timezoneRule = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+_-]*(/[A-Za-z][A-Za-z0-9+_-]*)*$`)

// ValidateTimezone (BACI-312) enforces the ui.timezone global setting's
// shape: a non-empty, length-capped, control-char-free IANA zone name
// like "Australia/Sydney", "UTC", or "Etc/GMT+10". Trimmed; rejects
// embedded whitespace and anything that doesn't match the slash-
// separated component shape. It is deliberately a shape check rather
// than a time.LoadLocation lookup — see timezoneRule's doc.
func ValidateTimezone(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", fmt.Errorf("timezone is required")
	}
	if len(s) > maxTimezoneLen {
		return "", fmt.Errorf("timezone too long: %d chars, max %d", len(s), maxTimezoneLen)
	}
	if !timezoneRule.MatchString(s) {
		return "", fmt.Errorf("timezone %q must be an IANA zone name (e.g. Australia/Sydney, UTC, Etc/GMT+10)", s)
	}
	return s, nil
}

// ValidateEmoji (BACI-172) enforces the per-feature emoji column's
// shape: either empty (no glyph) or exactly one grapheme cluster as
// counted by uniseg. ZWJ sequences (e.g. 👨‍👩‍👧) and country flags
// (🇦🇺) read as one cluster and pass; multi-cluster input like
// "FEATURE" (7 clusters) or "ab" (2 clusters) is rejected so the field
// stays a glyph decoration, not a free-form label. C0 control characters
// and DEL are rejected the same way as on single-line fields — an
// embedded NUL in an "emoji" would corrupt the audit log render.
func ValidateEmoji(s string) (string, error) {
	if !utf8.ValidString(s) {
		return "", fmt.Errorf("emoji is not valid UTF-8")
	}
	if len(s) > maxEmojiBytes {
		return "", fmt.Errorf("emoji too long: %d bytes, max %d", len(s), maxEmojiBytes)
	}
	// Empty is the "no glyph" signal — the UI renders nothing.
	if s == "" {
		return "", nil
	}
	for _, r := range s {
		if isDisallowedControlSingle(r) {
			return "", fmt.Errorf("emoji contains a disallowed control character (U+%04X)", r)
		}
	}
	n := uniseg.GraphemeClusterCount(s)
	if n != 1 {
		return "", fmt.Errorf("emoji %q must be empty or exactly one grapheme cluster (got %d)", s, n)
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

// maxFolderNameLen caps a single doc-folder name. Same bound as a
// document filename: a folder name is a sibling of a filename in the
// same tree and obeys the same character rules, so it gets the same
// ceiling.
const maxFolderNameLen = maxFilenameLen

// maxKanbanColumnNameLen caps a Kanban column name. Tighter than a
// folder name — the lane header is a fixed-width board affordance, so a
// runaway label breaks the layout rather than merely reading oddly.
const maxKanbanColumnNameLen = maxNameLen

// ValidateFolderName enforces the doc-folder naming rules at the store
// boundary. Same shape as ValidateDocFilenameStrict, and deliberately
// just as strict about '/' and '\\': a folder name is ONE segment of a
// derived display path ("Design/API/Auth"), and admitting a separator
// would let a single folder forge a fake hierarchy — and, once it
// reached the sync layer's path builders, nest a record folder inside
// another record folder, which an older bacio binary resolves by
// deleting the outer record wholesale.
func ValidateFolderName(name string) (string, error) {
	return validateTreeNodeName(name, "folder name", maxFolderNameLen)
}

// ValidateKanbanColumnName enforces the Kanban lane naming rules at the
// store boundary. Same rules as ValidateFolderName with a tighter
// length cap; '/' and '\\' are rejected so a lane name is always safe
// to use as one path segment in the sync record layout.
func ValidateKanbanColumnName(name string) (string, error) {
	return validateTreeNodeName(name, "column name", maxKanbanColumnNameLen)
}

// validateTreeNodeName is the shared body behind ValidateFolderName and
// ValidateKanbanColumnName. It mirrors ValidateDocFilenameStrict rule
// for rule — reject invalid UTF-8, empty, leading/trailing whitespace,
// over-length, the special names "." and "..", '/' and '\\', and every
// C0 control plus DEL — parameterised on the field label so the error
// messages read naturally for each caller. ValidateDocFilenameStrict
// itself is intentionally left standing on its own copy of this logic:
// its exact messages are load-bearing for existing callers and tests.
func validateTreeNodeName(name, field string, maxLen int) (string, error) {
	if !utf8.ValidString(name) {
		return "", fmt.Errorf("%s is not valid UTF-8", field)
	}
	if name == "" {
		return "", fmt.Errorf("%s is required", field)
	}
	if strings.TrimSpace(name) != name {
		return "", fmt.Errorf("%s must not have leading or trailing whitespace", field)
	}
	if len(name) > maxLen {
		return "", fmt.Errorf("%s too long: %d chars, max %d", field, len(name), maxLen)
	}
	if name == "." || name == ".." {
		return "", fmt.Errorf("%s %q is not allowed", field, name)
	}
	for _, r := range name {
		switch {
		case r == '/' || r == '\\':
			return "", fmt.Errorf("%s must not contain '/' or '\\'", field)
		case isDisallowedControlSingle(r):
			return "", fmt.Errorf("%s contains a disallowed control character (U+%04X)", field, r)
		}
	}
	return name, nil
}

// maxTodoContentBytes caps a single task-list item's content. Generous
// for a real task subject; tight enough that a malformed payload
// surfaces before it lands in agent_session_todos. Shared with
// internal/store/agent_todos.go's per-row validator.
const maxTodoContentBytes = 1 << 10 // 1 KiB

// maxArchiveRetentionDays caps the BACI-162 archive.retention_days
// setting. Roughly ten years — the rendered "in N days" hint in the
// Settings UI stays sensible, and a typo (e.g. 999999) is rejected
// rather than silently turning auto-archive into a no-op. Zero is
// rejected because the disable signal is the separate archive.auto_enabled
// boolean; a retention of zero on the value path is ambiguous.
const maxArchiveRetentionDays = 3650

// ValidateArchiveRetentionDays enforces the 1..maxArchiveRetentionDays
// range on the BACI-162 retention-days setting. Returns the integer
// unchanged on success; a clear error otherwise. Zero / negatives are
// rejected — disable auto-archive via the boolean toggle instead.
func ValidateArchiveRetentionDays(n int) (int, error) {
	if n < 1 {
		return 0, fmt.Errorf("retention_days must be >= 1 (disable auto-archive via the auto_enabled toggle instead, got %d)", n)
	}
	if n > maxArchiveRetentionDays {
		return 0, fmt.Errorf("retention_days too large: %d, max %d", n, maxArchiveRetentionDays)
	}
	return n, nil
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
