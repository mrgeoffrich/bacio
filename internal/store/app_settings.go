package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

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

const promptStatesKeyPrefix = "prompt_states."

// GetPromptStates returns the set of issue states a dispatch stage's
// prompt is valid to run from — the user's per-stage override, falling
// back to the built-in default (model.DefaultPromptStates) when none is
// stored. An untyped mode ("") has no gate and returns nil.
func (s *Store) GetPromptStates(mode model.DispatchMode) ([]model.State, error) {
	if mode == "" {
		return nil, nil
	}
	v, err := s.GetAppSetting(promptStatesKeyPrefix + string(mode))
	if err != nil {
		return nil, err
	}
	if v == "" {
		return model.DefaultPromptStates(mode), nil
	}
	out := make([]model.State, 0)
	for _, part := range strings.Split(v, ",") {
		st, err := model.ParseState(part)
		if err != nil {
			// A stored value should always be valid — but if the vocabulary
			// ever shrank, fall back to the default rather than erroring a
			// read path.
			return model.DefaultPromptStates(mode), nil
		}
		out = append(out, st)
	}
	return out, nil
}

// ValidatePromptStates runs the same mode + state-set checks as
// SetPromptStates without writing — the --dry-run path. The mode must
// be a concrete stage; every entry must be a valid issue state; no
// duplicates. It returns the cleaned, canonical list.
func (s *Store) ValidatePromptStates(mode model.DispatchMode, states []model.State) ([]model.State, error) {
	if _, err := model.ParseDispatchMode(string(mode)); err != nil {
		return nil, err
	}
	if mode == "" {
		return nil, errors.New("prompt state-gate requires a dispatch mode")
	}
	seen := make(map[model.State]bool, len(states))
	out := make([]model.State, 0, len(states))
	for _, raw := range states {
		st, err := model.ParseState(string(raw))
		if err != nil {
			return nil, err
		}
		if seen[st] {
			return nil, fmt.Errorf("duplicate state %q in prompt state-gate", st)
		}
		seen[st] = true
		out = append(out, st)
	}
	return out, nil
}

// SetPromptStates stores a custom state-gate for a dispatch stage — the
// set of issue states the stage's prompt is valid to run from. An empty
// slice clears the override, so GetPromptStates falls back to the
// built-in default.
func (s *Store) SetPromptStates(mode model.DispatchMode, states []model.State) error {
	clean, err := s.ValidatePromptStates(mode, states)
	if err != nil {
		return err
	}
	parts := make([]string, len(clean))
	for i, st := range clean {
		parts[i] = string(st)
	}
	return s.SetAppSetting(promptStatesKeyPrefix+string(mode), strings.Join(parts, ","))
}

// AllPromptStates returns the resolved state-gate (custom or built-in
// default) for every dispatch stage, keyed by mode.
func (s *Store) AllPromptStates() (map[model.DispatchMode][]model.State, error) {
	out := make(map[model.DispatchMode][]model.State, len(model.AllDispatchModes()))
	for _, m := range model.AllDispatchModes() {
		st, err := s.GetPromptStates(m)
		if err != nil {
			return nil, err
		}
		out[m] = st
	}
	return out, nil
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
