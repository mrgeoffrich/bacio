package store

import (
	"database/sql"
	"errors"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// Global (not per-repo) KV used by the desktop app. Keep keys namespaced
// (e.g. "prompt_template.<mode>") so future global preferences can share
// this table — the same rationale as tui_settings, one scope up.

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

const promptTemplateKeyPrefix = "prompt_template."

// GetPromptTemplate returns the user's custom dispatch prompt template
// for a stage, falling back to the built-in default when none is
// stored. An untyped mode ("") has no template and returns "".
func (s *Store) GetPromptTemplate(mode model.DispatchMode) (string, error) {
	if mode == "" {
		return "", nil
	}
	v, err := s.GetAppSetting(promptTemplateKeyPrefix + string(mode))
	if err != nil {
		return "", err
	}
	if v == "" {
		return model.DefaultPromptTemplate(mode), nil
	}
	return v, nil
}

// ValidatePromptTemplate runs the same mode + body checks as
// SetPromptTemplate without writing — the --dry-run path. It returns the
// cleaned body so callers projecting a dry-run result see exactly what a
// real call would have stored.
func (s *Store) ValidatePromptTemplate(mode model.DispatchMode, body string) (string, error) {
	if _, err := model.ParseDispatchMode(string(mode)); err != nil {
		return "", err
	}
	if mode == "" {
		return "", errors.New("prompt template requires a dispatch mode")
	}
	return ValidateBody(body, "prompt template", false)
}

// SetPromptTemplate stores a custom dispatch prompt template for a
// stage. An empty body clears the override — GetPromptTemplate then
// falls back to the built-in default. The body is validated as
// multi-line free text, same as a document body.
func (s *Store) SetPromptTemplate(mode model.DispatchMode, body string) error {
	clean, err := s.ValidatePromptTemplate(mode, body)
	if err != nil {
		return err
	}
	return s.SetAppSetting(promptTemplateKeyPrefix+string(mode), clean)
}

// AllPromptTemplates returns the resolved template (custom or built-in
// default) for every dispatch stage, keyed by mode.
func (s *Store) AllPromptTemplates() (map[model.DispatchMode]string, error) {
	out := make(map[model.DispatchMode]string, len(model.AllDispatchModes()))
	for _, m := range model.AllDispatchModes() {
		t, err := s.GetPromptTemplate(m)
		if err != nil {
			return nil, err
		}
		out[m] = t
	}
	return out, nil
}
