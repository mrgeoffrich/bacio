package inputs

// SettingsTemplateSetInput is the payload for
// `bacio settings template set --json`. Slug is the template slug (one
// of the built-ins — plan, implement, review, ship, fix_review — or any
// user-created template). Body is the new prompt text; the
// {{issue_id}}, {{issue_title}} and {{repo_prefix}} placeholders are
// substituted with the issue's context at dispatch time. An empty Body
// is rejected here — use `settings template reset` to revert a built-in
// to its embedded default.
type SettingsTemplateSetInput struct {
	Slug string `json:"slug"`
	Body string `json:"body"`
}

// SettingsTemplateResetInput is the payload for
// `bacio settings template reset --json`. It reverts a built-in template
// to its embedded default body (and state-gate). Only valid for built-in
// slugs — a user-created template has no embedded default to revert to;
// edit its body directly or delete + re-add it.
type SettingsTemplateResetInput struct {
	Slug string `json:"slug"`
}

// SettingsTemplateStatesSetInput is the payload for
// `bacio settings template states set --json`. Slug is a template slug.
// States is the set of issue states the template's prompt is valid to
// run from — each must be a canonical state (todo, in_progress,
// needs_action, in_review, done, cancelled). An empty States is rejected
// here — use `settings template states reset` to revert a built-in to
// its default.
type SettingsTemplateStatesSetInput struct {
	Slug   string   `json:"slug"`
	States []string `json:"states"`
}

// SettingsTemplateStatesResetInput is the payload for
// `bacio settings template states reset --json`. It reverts a built-in
// template's state-gate to its embedded default. Built-ins only.
type SettingsTemplateStatesResetInput struct {
	Slug string `json:"slug"`
}

// SettingsTemplateAddInput is the payload for
// `bacio settings template add --json`. Slug is the storage / CLI
// identifier (kebab- or snake-case, max 60 chars, unique). Name is the
// human display label (1–80 chars, unique case-insensitively). Body is
// the prompt text; States is the set of issue states the template is
// valid to run from. ConcurrencyLimit (BACI-51) caps the in-flight
// (pending+delivered) dispatches per (repo, slug) the background
// matcher will allow; 0 = unlimited.
type SettingsTemplateAddInput struct {
	Slug             string   `json:"slug"`
	Name             string   `json:"name"`
	Body             string   `json:"body"`
	States           []string `json:"states"`
	ConcurrencyLimit int      `json:"concurrency_limit,omitempty"`
}

// SettingsTemplateSetConcurrencyInput is the payload for
// `bacio settings template set-concurrency --json` (BACI-51). Slug
// names the template; ConcurrencyLimit is the new cap (>=0,
// 0 = unlimited).
type SettingsTemplateSetConcurrencyInput struct {
	Slug             string `json:"slug"`
	ConcurrencyLimit int    `json:"concurrency_limit"`
}

// SettingsTemplateRenameInput is the payload for
// `bacio settings template rename --json`. Slug names the existing
// template; NewSlug and/or NewName are the changes to apply. Either may
// be empty to leave that field unchanged, but at least one must differ
// from the current value (a no-op rename errors).
type SettingsTemplateRenameInput struct {
	Slug    string `json:"slug"`
	NewSlug string `json:"new_slug,omitempty"`
	NewName string `json:"new_name,omitempty"`
}

// SettingsTemplateRmInput is the payload for
// `bacio settings template rm --json`. Deletes the template named by
// Slug. Built-in templates can be deleted too; restore them with
// `bacio settings template restore-defaults`. Historical
// agent_dispatches rows that reference the slug are left intact.
type SettingsTemplateRmInput struct {
	Slug string `json:"slug"`
}

// SettingsTemplateRestoreDefaultsInput is the payload for
// `bacio settings template restore-defaults --json`. No fields — the
// verb re-seeds every built-in slug that's not currently present.
// Idempotent.
type SettingsTemplateRestoreDefaultsInput struct{}
