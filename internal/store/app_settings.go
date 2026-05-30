package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// Global (not per-repo) KV used by the desktop app. Keep keys namespaced
// (e.g. "display.show_archived") so future global preferences can
// share this table — the same rationale as tui_settings, one scope up.
//
// The dispatch prompt templates and their state-gates used to live here
// (keyed prompt_template.<slug> / prompt_states.<slug>) but moved to
// the dedicated `prompt_templates` table in BACI-31. The legacy
// Get/Set/All helpers below are thin shims that read/write the new
// table so existing callers keep working unchanged; new code should
// use the table-typed Store.ListPromptTemplates / AddPromptTemplate /
// etc. directly.

func (s *Store) GetAppSetting(key string) (string, error) {
	var v string
	err := s.DB.QueryRow(
		`SELECT value FROM app_settings WHERE key = ?`, key,
	).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

func (s *Store) SetAppSetting(key, value string) error {
	_, err := s.DB.Exec(
		`INSERT INTO app_settings (key, value, updated_at)
		 VALUES (?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(key) DO UPDATE SET
		   value      = excluded.value,
		   updated_at = CURRENT_TIMESTAMP`,
		key, value,
	)
	return err
}

// GetPromptTemplate returns the dispatch prompt template body for the
// given slug. An empty mode returns "". A slug with no matching row in
// prompt_templates (a deleted template) returns "" — historical
// dispatches whose mode-slug has since been removed are rendered as
// "no template", not an error.
//
// Deprecated: use Store.GetPromptTemplateBySlug for the typed shape.
func (s *Store) GetPromptTemplate(mode model.DispatchMode) (string, error) {
	if mode == "" {
		return "", nil
	}
	t, err := s.GetPromptTemplateBySlug(string(mode))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return "", nil
		}
		return "", err
	}
	return t.Body, nil
}

// ValidatePromptTemplate runs the body validator under the legacy mode
// argument shape. Kept for the dry-run path of the original
// SetPromptTemplate; new code should use ValidatePromptTemplateBody.
//
// Deprecated: use Store.ValidatePromptTemplateBody.
func (s *Store) ValidatePromptTemplate(mode model.DispatchMode, body string) (string, error) {
	if _, err := model.ParseDispatchMode(string(mode)); err != nil {
		return "", err
	}
	if mode == "" {
		return "", errors.New("prompt template requires a dispatch mode")
	}
	return ValidatePromptTemplateBody(body)
}

// SetPromptTemplate stores a custom body for one slug. If the slug
// already has a row, its body is updated in place. If the slug doesn't
// exist (a deleted template) and is a known built-in, the row is re-
// seeded with the supplied body and default states. An empty body
// triggers a "reset to default": built-in slugs get their embedded
// default body restored; non-built-in slugs have their body cleared.
//
// Deprecated: use Store.UpdatePromptTemplate / AddPromptTemplate /
// RestoreBuiltinPromptTemplates directly.
func (s *Store) SetPromptTemplate(mode model.DispatchMode, body string) error {
	clean, err := s.ValidatePromptTemplate(mode, body)
	if err != nil {
		return err
	}
	slug := string(mode)
	existing, err := s.GetPromptTemplateBySlug(slug)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	// Empty body = reset semantic: built-in slugs revert to the
	// embedded default body; user-created slugs accept an empty body
	// (they have no embedded default to revert to).
	if clean == "" {
		if def := model.DefaultPromptBodyForBuiltinSlug(slug); def != "" {
			clean = def
		}
	}
	if existing != nil {
		_, err := s.UpdatePromptTemplate(slug, UpdatePromptTemplatePatch{Body: &clean})
		return err
	}
	// No row for that slug. Auto-create one for known built-in slugs
	// (this is the path the desktop's "Reset to default" hits when the
	// user had deleted the built-in and is now re-saving its body).
	if def := model.DefaultPromptBodyForBuiltinSlug(slug); def != "" || isBuiltinSlug(slug) {
		name := model.BuiltinTemplateLabel(slug)
		if name == "" {
			name = slug
		}
		_, err := s.AddPromptTemplate(AddPromptTemplateIn{
			Slug:      slug,
			Name:      name,
			Body:      clean,
			IsBuiltin: true,
		})
		return err
	}
	return fmt.Errorf("no template %q is registered — use `bacio settings template add` to create one", slug)
}

// AllPromptTemplates returns the resolved body for every registered
// template, keyed by slug-as-DispatchMode. The map's iteration order is
// indeterminate; UIs that need ordering should use
// Store.ListPromptTemplates directly.
//
// Deprecated: use Store.ListPromptTemplates for the typed shape.
func (s *Store) AllPromptTemplates() (map[model.DispatchMode]string, error) {
	tmpls, err := s.ListPromptTemplates()
	if err != nil {
		return nil, err
	}
	out := make(map[model.DispatchMode]string, len(tmpls))
	for _, t := range tmpls {
		out[model.DispatchMode(t.Slug)] = t.Body
	}
	return out, nil
}

// isBuiltinSlug reports whether s names a built-in template slug.
func isBuiltinSlug(s string) bool {
	for _, b := range model.BuiltinTemplateSlugs() {
		if b == s {
			return true
		}
	}
	return false
}

// displayShowArchivedKey is the BACI-68 global toggle controlling
// whether archived issues / documents / features appear in default
// lists (board, kanban, doc list, feature list) and on the API. The
// CLI's per-call `--include-archived` flag overrides this setting for
// the lifetime of one command.
const displayShowArchivedKey = "display.show_archived"

// GetDisplayShowArchived reports whether archived rows should appear
// in default lists (BACI-68). A missing/empty value — or any value
// that isn't exactly "true" — reads as false (the default); a read
// path never errors on an unexpected stored value.
func (s *Store) GetDisplayShowArchived() (bool, error) {
	v, err := s.GetAppSetting(displayShowArchivedKey)
	if err != nil {
		return false, err
	}
	return v == "true", nil
}

// SetDisplayShowArchived stores the BACI-68 display toggle.
func (s *Store) SetDisplayShowArchived(show bool) error {
	v := "false"
	if show {
		v = "true"
	}
	return s.SetAppSetting(displayShowArchivedKey, v)
}

// archiveAutoEnabledKey is the BACI-162 global toggle controlling
// whether the leader-elected controller's hourly archive sweep
// archives terminal-state issues. When false, the issue pass is
// skipped entirely (manual `bacio issue archive` still works, and
// the feature / document cascade passes still run — they're driven
// by per-entity archived_at writes, not by the retention window).
const archiveAutoEnabledKey = "archive.auto_enabled"

// archiveRetentionDaysKey is the BACI-162 global setting controlling
// how many days a terminal-state issue (done / cancelled) must sit
// before the auto-sweep stamps archived_at. Measured against the
// BACI-138 `terminal_at` column (the timestamp of the latest
// transition into a terminal state), not `updated_at` — editing a
// closed issue's tags doesn't reset the retention countdown.
const archiveRetentionDaysKey = "archive.retention_days"

// GetArchiveAutoEnabled reports whether the BACI-162 hourly issue
// auto-archive pass should run. Defaults to TRUE — auto-archive is
// opt-OUT (the historical behaviour). Only the exact string "false"
// disables it; any other stored value (missing, blank, "garbage") reads
// as true. Same defensive shape as GetSyncBackgroundEnabled.
func (s *Store) GetArchiveAutoEnabled() (bool, error) {
	v, err := s.GetAppSetting(archiveAutoEnabledKey)
	if err != nil {
		return true, err
	}
	return v != "false", nil
}

// SetArchiveAutoEnabled stores the BACI-162 archive.auto_enabled toggle.
func (s *Store) SetArchiveAutoEnabled(enabled bool) error {
	v := "false"
	if enabled {
		v = "true"
	}
	return s.SetAppSetting(archiveAutoEnabledKey, v)
}

// GetArchiveRetentionDays reports the BACI-162 retention window in
// days. Defaults to DefaultArchiveRetentionDays (7) when the value is
// missing, blank, or unparseable. Values outside the 1..3650 range
// (validator bounds) also fall back to the default — defensive read
// style: never error on a malformed stored value.
func (s *Store) GetArchiveRetentionDays() (int, error) {
	v, err := s.GetAppSetting(archiveRetentionDaysKey)
	if err != nil {
		return DefaultArchiveRetentionDays, err
	}
	if v == "" {
		return DefaultArchiveRetentionDays, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return DefaultArchiveRetentionDays, nil
	}
	if n < 1 || n > maxArchiveRetentionDays {
		return DefaultArchiveRetentionDays, nil
	}
	return n, nil
}

// SetArchiveRetentionDays stores the BACI-162 retention window in
// days. Callers should run ValidateArchiveRetentionDays first.
func (s *Store) SetArchiveRetentionDays(days int) error {
	return s.SetAppSetting(archiveRetentionDaysKey, strconv.Itoa(days))
}

// syncBackgroundEnabledKey is the BACI-89 global toggle controlling
// whether the leader-elected controller runs its background git-sync
// ticker (internal/sync.BackgroundRunner). It is the opt-OUT switch
// for the continual-mirror behaviour.
const syncBackgroundEnabledKey = "sync.background_enabled"

// GetSyncBackgroundEnabled reports whether the controller's background
// git-sync ticker should run (BACI-89).
//
// IMPORTANT: unlike the other boolean getters in this file, this one
// DEFAULTS TO TRUE. A missing/empty value reads as true; only the
// exact string "false" disables it. Background sync is opt-OUT, not
// opt-in: a user only reaches this code path by having explicitly run
// `bacio sync init` / `bacio sync clone`, so continual mirroring of an
// already-configured sync repo is the expected behaviour. The inverted
// default is deliberate — do not "fix" it to match GetDisplayShowArchived.
func (s *Store) GetSyncBackgroundEnabled() (bool, error) {
	v, err := s.GetAppSetting(syncBackgroundEnabledKey)
	if err != nil {
		return true, err
	}
	return v != "false", nil
}

// SetSyncBackgroundEnabled stores the BACI-89 background-sync toggle.
func (s *Store) SetSyncBackgroundEnabled(enabled bool) error {
	v := "false"
	if enabled {
		v = "true"
	}
	return s.SetAppSetting(syncBackgroundEnabledKey, v)
}

// uiShippedSfxKey is the BACI-240 global toggle controlling whether
// the Pipeline Shipping-column Shipped pill plays a short "ka-ching"
// SFX on every genuine increment of the shipped count. BACI-295 flipped
// the default ON now that the feature has shipped.
const uiShippedSfxKey = "ui.shipped_sfx"

// GetUIShippedSfx reports whether the BACI-240 Shipped-pill SFX is
// enabled.
//
// IMPORTANT: like GetSyncBackgroundEnabled (and unlike
// GetDisplayShowArchived), this getter DEFAULTS TO TRUE (BACI-295). A
// missing/empty value reads as true; only the exact string "false"
// disables it. The inverted default is deliberate — do not "fix" it to
// match the opt-in getters. Existing users who explicitly toggled the
// sound off keep their stored "false" and stay silent; only DBs that
// never set the key inherit the new on-by-default.
func (s *Store) GetUIShippedSfx() (bool, error) {
	v, err := s.GetAppSetting(uiShippedSfxKey)
	if err != nil {
		return true, err
	}
	return v != "false", nil
}

// SetUIShippedSfx stores the BACI-240 Shipped-pill SFX toggle.
func (s *Store) SetUIShippedSfx(enabled bool) error {
	v := "false"
	if enabled {
		v = "true"
	}
	return s.SetAppSetting(uiShippedSfxKey, v)
}

// uiTimezoneKey is the BACI-312 global setting holding the user's IANA
// timezone name (e.g. "Australia/Sydney"). It drives the browser-side
// local-midnight cutoff for the Pipeline Shipping-column Shipped pill's
// "Today" scope; the Go binary never resolves it (no time/tzdata embed),
// so the value is opaque to the store beyond shape validation.
const uiTimezoneKey = "ui.timezone"

// GetUITimezone reads the BACI-312 ui.timezone setting. Unlike the
// boolean toggles in this file there is no meaningful default: an
// unset key returns "" and the React layer auto-detects the browser's
// zone and persists it on first run. Defensive read — a malformed
// stored value (which the validator should have prevented on write)
// still reads back as whatever was stored; the browser's Intl is the
// final arbiter of whether the name resolves.
func (s *Store) GetUITimezone() (string, error) {
	return s.GetAppSetting(uiTimezoneKey)
}

// SetUITimezone stores the BACI-312 ui.timezone setting after running
// ValidateTimezone, so a malformed IANA name is rejected at the store
// boundary rather than silently poisoning the cutoff math.
func (s *Store) SetUITimezone(tz string) error {
	clean, err := ValidateTimezone(tz)
	if err != nil {
		return err
	}
	return s.SetAppSetting(uiTimezoneKey, clean)
}
