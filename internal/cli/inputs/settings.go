package inputs

// SettingsTemplateSetInput is the payload for
// `bacio settings template set --json`. Slug is the template slug (one
// of the built-ins — plan, design, implement, review, ship, fix_review — or any
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
// to its embedded default body. Only valid for built-in slugs — a
// user-created template has no embedded default to revert to; edit its
// body directly or delete + re-add it.
type SettingsTemplateResetInput struct {
	Slug string `json:"slug"`
}

// SettingsTemplateAddInput is the payload for
// `bacio settings template add --json`. Slug is the storage / CLI
// identifier (kebab- or snake-case, max 60 chars, unique). Name is the
// human display label (1–80 chars, unique case-insensitively). Body is
// the prompt text. ConcurrencyLimit (BACI-51) caps the in-flight
// (pending+delivered) dispatches per (repo, slug) the background
// matcher will allow; 0 = unlimited. ActionLabel (BACI-67) is the
// imperative override rendered on the dispatch action menus
// (kanban-card + issue-workspace dropdowns). When empty, the UI
// derives the label from Name via the gerund→imperative rule.
type SettingsTemplateAddInput struct {
	Slug             string `json:"slug"`
	Name             string `json:"name"`
	Body             string `json:"body"`
	ConcurrencyLimit int    `json:"concurrency_limit,omitempty"`
	ActionLabel      string `json:"action_label,omitempty"`
}

// SettingsTemplateSetConcurrencyInput is the payload for
// `bacio settings template set-concurrency --json` (BACI-51). Slug
// names the template; ConcurrencyLimit is the new cap (>=0,
// 0 = unlimited).
type SettingsTemplateSetConcurrencyInput struct {
	Slug             string `json:"slug"`
	ConcurrencyLimit int    `json:"concurrency_limit"`
}

// SettingsTemplateSetActionLabelInput is the payload for
// `bacio settings template set-action-label --json` (BACI-67). Slug
// names the template; ActionLabel is the new imperative override
// rendered on the dispatch action menus. An empty ActionLabel clears
// the override — the UI then derives the label from Name via the
// gerund→imperative rule. Single-line, controls rejected; up to 80
// chars.
type SettingsTemplateSetActionLabelInput struct {
	Slug        string `json:"slug"`
	ActionLabel string `json:"action_label"`
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

// SettingsShowArchivedInput is the payload for `bacio settings
// show-archived --json` (BACI-68). The CLI verb doubles as get and
// set: empty body reads the current value; non-empty `value` writes
// it. The per-call `--include-archived` flag on `list` commands
// overrides this setting for one call.
type SettingsShowArchivedInput struct {
	Value bool `json:"value"`
}

// ArchiveSweepInput is the payload for `bacio archive sweep --json`
// (BACI-68). No fields — the verb runs the same three SQL passes the
// hourly Controller tick runs, on demand. Useful for testing and for
// users who want to skip the wait. Idempotent.
type ArchiveSweepInput struct{}

// SettingsSyncBackgroundInput is the payload for `bacio settings
// sync-background --json` (BACI-89). The CLI verb doubles as get and
// set: empty body reads the current value; non-empty `value` writes
// it. Background sync is opt-OUT — the value defaults to true, so this
// verb is how a user turns the continual-mirror ticker off.
type SettingsSyncBackgroundInput struct {
	Value bool `json:"value"`
}

// SettingsArchiveInput is the payload for `bacio settings archive
// --json` (BACI-162). Both fields are required on the write path —
// the verb writes both atomically rather than juggling a partial
// update; the read path returns the current pair. AutoEnabled gates
// the hourly issue auto-archive pass; RetentionDays is the number of
// days a terminal-state issue's terminal_at must sit before the next
// sweep archives it. 1..3650; explicitly does NOT accept 0 (use the
// boolean to disable instead — see store.ValidateArchiveRetentionDays).
type SettingsArchiveInput struct {
	AutoEnabled   bool `json:"auto_enabled"`
	RetentionDays int  `json:"retention_days"`
}

// SettingsShippedSfxInput is the payload for `bacio settings
// shipped-sfx --json` (BACI-240). The CLI verb doubles as get and
// set: empty body reads the current value; non-empty `value` writes
// it. Defaults to true (BACI-295 flipped the default on).
type SettingsShippedSfxInput struct {
	Value bool `json:"value"`
}

// SettingsTimezoneInput is the payload for `bacio settings timezone
// --json` (BACI-312). The CLI verb doubles as get and set: empty body
// reads the current value; a non-empty `timezone` writes it. The value
// is an IANA zone name (e.g. "Australia/Sydney"); it drives the
// browser-side local-midnight cutoff for the Pipeline Shipping-column
// Shipped pill's "Today" scope. Unset by default — the desktop / web UI
// auto-detects the browser zone and persists it on first run.
type SettingsTimezoneInput struct {
	Timezone string `json:"timezone"`
}

// SettingsDefaultFeatureInput is the payload for
// `bacio settings default-feature --json` (BACI-235). Slug names the
// per-repo default feature that auto-applies to issues created
// without an explicit `feature_slug`. Empty Slug clears the setting
// (the "no default" semantic — featureless creates again, the
// pre-BACI-235 behaviour). The verb is per-repo despite living under
// the otherwise-global `settings` group — see the verb's Long for
// the carve-out note.
type SettingsDefaultFeatureInput struct {
	Slug string `json:"slug"`
}
