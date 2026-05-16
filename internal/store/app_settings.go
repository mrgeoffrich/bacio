package store

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// Global (not per-repo) KV used by the desktop app. Keep keys namespaced
// (e.g. "board.hide_empty_columns") so future global preferences can
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
			Slug:          slug,
			Name:          name,
			Body:          clean,
			AllowedStates: model.DefaultPromptStatesForBuiltinSlug(slug),
			IsBuiltin:     true,
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

// GetPromptStates returns the state-gate (set of issue states whose
// prompt is valid to run from them) for a template by slug. Empty/
// unknown slug returns nil — matching the old behaviour where a missing
// override fell back to nothing.
//
// Deprecated: use Store.GetPromptTemplateBySlug for the typed shape.
func (s *Store) GetPromptStates(mode model.DispatchMode) ([]model.State, error) {
	if mode == "" {
		return nil, nil
	}
	t, err := s.GetPromptTemplateBySlug(string(mode))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return t.AllowedStates, nil
}

// ValidatePromptStates validates the state set against the canonical
// vocabulary — same shape as ValidatePromptTemplateStates, kept under
// the legacy name for the dry-run path of the original SetPromptStates.
//
// Deprecated: use Store.ValidatePromptTemplateStates.
func (s *Store) ValidatePromptStates(mode model.DispatchMode, states []model.State) ([]model.State, error) {
	if _, err := model.ParseDispatchMode(string(mode)); err != nil {
		return nil, err
	}
	if mode == "" {
		return nil, errors.New("prompt state-gate requires a dispatch mode")
	}
	return ValidatePromptTemplateStates(states)
}

// SetPromptStates stores a custom state-gate for a slug. Empty slice =
// reset semantic: built-in slugs revert to the embedded default gate;
// non-built-in slugs accept an empty gate (their template never appears
// on a per-card menu but is still reachable via `bacio agent dispatch`).
//
// Deprecated: use Store.UpdatePromptTemplate directly.
func (s *Store) SetPromptStates(mode model.DispatchMode, states []model.State) error {
	clean, err := s.ValidatePromptStates(mode, states)
	if err != nil {
		return err
	}
	slug := string(mode)
	if len(clean) == 0 {
		if def := model.DefaultPromptStatesForBuiltinSlug(slug); def != nil {
			clean = def
		}
	}
	existing, err := s.GetPromptTemplateBySlug(slug)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	if existing != nil {
		_, err := s.UpdatePromptTemplate(slug, UpdatePromptTemplatePatch{AllowedStates: &clean})
		return err
	}
	if isBuiltinSlug(slug) {
		name := model.BuiltinTemplateLabel(slug)
		if name == "" {
			name = slug
		}
		_, err := s.AddPromptTemplate(AddPromptTemplateIn{
			Slug:          slug,
			Name:          name,
			Body:          model.DefaultPromptBodyForBuiltinSlug(slug),
			AllowedStates: clean,
			IsBuiltin:     true,
		})
		return err
	}
	return fmt.Errorf("no template %q is registered — use `bacio settings template add` to create one", slug)
}

// AllPromptStates returns the state-gate for every registered template,
// keyed by slug.
//
// Deprecated: use Store.ListPromptTemplates for the typed shape.
func (s *Store) AllPromptStates() (map[model.DispatchMode][]model.State, error) {
	tmpls, err := s.ListPromptTemplates()
	if err != nil {
		return nil, err
	}
	out := make(map[model.DispatchMode][]model.State, len(tmpls))
	for _, t := range tmpls {
		out[model.DispatchMode(t.Slug)] = append([]model.State(nil), t.AllowedStates...)
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

const boardHideEmptyColumnsKey = "board.hide_empty_columns"

// GetBoardHideEmptyColumns reports whether the desktop Board should hide
// columns that have no cards. A missing/empty value — or any value that
// isn't exactly "true" — reads as false (the default), the same
// defensive read style as GetPromptStates: a read path never errors on
// an unexpected stored value.
func (s *Store) GetBoardHideEmptyColumns() (bool, error) {
	v, err := s.GetAppSetting(boardHideEmptyColumnsKey)
	if err != nil {
		return false, err
	}
	return v == "true", nil
}

// SetBoardHideEmptyColumns stores the desktop Board's hide-empty-columns
// preference.
func (s *Store) SetBoardHideEmptyColumns(hide bool) error {
	v := "false"
	if hide {
		v = "true"
	}
	return s.SetAppSetting(boardHideEmptyColumnsKey, v)
}
