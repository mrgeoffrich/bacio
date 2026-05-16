package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// PromptTemplate is one user-editable dispatch prompt template, stored
// in the prompt_templates table. Slug is the stable storage/CLI
// identifier; Name is the human-visible label. Body is the prompt text
// (with the {{issue_id}} / {{issue_title}} / {{repo_prefix}} tokens
// substituted at dispatch time). AllowedStates is the state-gate.
// IsBuiltin is informational only — it does NOT gate deletion.
type PromptTemplate struct {
	ID            int64         `json:"id"`
	Slug          string        `json:"slug"`
	Name          string        `json:"name"`
	Body          string        `json:"body"`
	AllowedStates []model.State `json:"allowed_states"`
	IsBuiltin     bool          `json:"is_builtin"`
	CreatedAt     time.Time     `json:"created_at"`
	UpdatedAt     time.Time     `json:"updated_at"`
}

// AddPromptTemplateIn is the input for AddPromptTemplate. Slug and Name
// are required and validated; Body and AllowedStates default to empty.
type AddPromptTemplateIn struct {
	Slug          string
	Name          string
	Body          string
	AllowedStates []model.State
	// IsBuiltin is only set by the seed step in migrate() — user-created
	// templates always store IsBuiltin = false.
	IsBuiltin bool
}

// UpdatePromptTemplatePatch is the partial patch for UpdatePromptTemplate.
// Pointer-of-string lets a caller distinguish "leave unchanged" (nil)
// from "set to empty" (non-nil empty string), matching the convention
// used elsewhere in the store layer. AllowedStates uses a sentinel:
// nil = leave unchanged, non-nil (even empty) = replace.
type UpdatePromptTemplatePatch struct {
	Name          *string
	Body          *string
	AllowedStates *[]model.State
}

// maxTemplateNameLen caps the display label. Long enough for "Fixing a
// post-mortem follow-up" and short enough that the desktop list doesn't
// wrap on a narrow window.
const maxTemplateNameLen = maxNameLen

// ValidatePromptTemplateSlug enforces the slug shape used for templates.
// Like ValidateSlug but allows underscores so the built-in `fix_review`
// remains a valid slug (and so user-imported tooling that uses snake_case
// keeps working without a forced rename).
func ValidatePromptTemplateSlug(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", fmt.Errorf("slug is required")
	}
	if len(s) > maxSlugLen {
		return "", fmt.Errorf("slug too long: %d chars, max %d", len(s), maxSlugLen)
	}
	if _, err := model.ParseDispatchMode(s); err != nil {
		return "", err
	}
	return s, nil
}

// ValidatePromptTemplateName runs the name through the single-line
// validator (required, no controls, length-capped).
func ValidatePromptTemplateName(s string) (string, error) {
	return validateSingleLine(s, "template name", maxTemplateNameLen, true)
}

// ValidatePromptTemplateBody validates the body field — multi-line
// free text, not required (an empty body is allowed; the dispatch will
// just be the rendered note).
func ValidatePromptTemplateBody(s string) (string, error) {
	return ValidateBody(s, "prompt template", false)
}

// ValidatePromptTemplateStates validates and canonicalises the state-
// gate: every entry must parse as a known state; duplicates are
// rejected. Order is preserved (so the stored value matches what the UI
// rendered when the user toggled the chips).
func ValidatePromptTemplateStates(states []model.State) ([]model.State, error) {
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

// encodeStates renders []model.State as the JSON-array form stored in
// prompt_templates.allowed_states_json. Always non-nil so the column
// never holds NULL.
func encodeStates(states []model.State) (string, error) {
	if states == nil {
		states = []model.State{}
	}
	strs := make([]string, len(states))
	for i, s := range states {
		strs[i] = string(s)
	}
	b, err := json.Marshal(strs)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// decodeStates parses the stored JSON array back into []model.State.
// Malformed JSON or unknown state strings fall back to an empty slice
// rather than erroring — read paths never surface a store-side encoding
// mistake; the next write rewrites the cell cleanly.
func decodeStates(raw string) []model.State {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var strs []string
	if err := json.Unmarshal([]byte(raw), &strs); err != nil {
		return nil
	}
	out := make([]model.State, 0, len(strs))
	for _, s := range strs {
		st, err := model.ParseState(s)
		if err != nil {
			continue
		}
		out = append(out, st)
	}
	return out
}

// scanPromptTemplate is the row-scan helper shared by Get/List.
func scanPromptTemplate(row interface {
	Scan(...any) error
}) (*PromptTemplate, error) {
	var (
		t          PromptTemplate
		statesJSON string
		isBuiltin  int
	)
	if err := row.Scan(&t.ID, &t.Slug, &t.Name, &t.Body, &statesJSON, &isBuiltin, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return nil, err
	}
	t.AllowedStates = decodeStates(statesJSON)
	t.IsBuiltin = isBuiltin != 0
	return &t, nil
}

// ListPromptTemplates returns every registered template, ordered by
// created_at, id — so the bundled built-ins (seeded in lifecycle order)
// lead the list and user adds slot in chronologically. This is the
// canonical iteration order surfaced to UIs.
func (s *Store) ListPromptTemplates() ([]*PromptTemplate, error) {
	rows, err := s.DB.Query(`
		SELECT id, slug, name, body, allowed_states_json, is_builtin, created_at, updated_at
		  FROM prompt_templates
		 ORDER BY created_at ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*PromptTemplate
	for rows.Next() {
		t, err := scanPromptTemplate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// GetPromptTemplateBySlug returns one template by slug. Returns
// ErrNotFound when no such template exists.
func (s *Store) GetPromptTemplateBySlug(slug string) (*PromptTemplate, error) {
	row := s.DB.QueryRow(`
		SELECT id, slug, name, body, allowed_states_json, is_builtin, created_at, updated_at
		  FROM prompt_templates
		 WHERE slug = ?`, slug)
	t, err := scanPromptTemplate(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return t, err
}

// ValidateAddPromptTemplate runs the same checks as AddPromptTemplate
// without writing — the --dry-run path. Returns a cleaned projection
// that callers can emit as the dry-run result.
func (s *Store) ValidateAddPromptTemplate(in AddPromptTemplateIn) (*PromptTemplate, error) {
	slug, err := ValidatePromptTemplateSlug(in.Slug)
	if err != nil {
		return nil, err
	}
	name, err := ValidatePromptTemplateName(in.Name)
	if err != nil {
		return nil, err
	}
	body, err := ValidatePromptTemplateBody(in.Body)
	if err != nil {
		return nil, err
	}
	states, err := ValidatePromptTemplateStates(in.AllowedStates)
	if err != nil {
		return nil, err
	}
	return &PromptTemplate{
		Slug:          slug,
		Name:          name,
		Body:          body,
		AllowedStates: states,
		IsBuiltin:     in.IsBuiltin,
	}, nil
}

// AddPromptTemplate inserts a new template. Slug must be unique; Name
// must be unique case-insensitively. Returns the persisted row with
// server-time fields populated.
func (s *Store) AddPromptTemplate(in AddPromptTemplateIn) (*PromptTemplate, error) {
	projected, err := s.ValidateAddPromptTemplate(in)
	if err != nil {
		return nil, err
	}
	encoded, err := encodeStates(projected.AllowedStates)
	if err != nil {
		return nil, err
	}
	isBuiltin := 0
	if in.IsBuiltin {
		isBuiltin = 1
	}
	res, err := s.DB.Exec(`
		INSERT INTO prompt_templates (slug, name, body, allowed_states_json, is_builtin, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		projected.Slug, projected.Name, projected.Body, encoded, isBuiltin)
	if err != nil {
		return nil, wrapTemplateConflict(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	out, err := s.GetPromptTemplateBySlug(projected.Slug)
	if err != nil {
		return nil, err
	}
	out.ID = id
	return out, nil
}

// ValidateUpdatePromptTemplate runs the body/name/states checks without
// writing. Returns the projected row (slug + IsBuiltin from the
// existing row; mutated fields from the patch).
func (s *Store) ValidateUpdatePromptTemplate(slug string, patch UpdatePromptTemplatePatch) (*PromptTemplate, error) {
	existing, err := s.GetPromptTemplateBySlug(slug)
	if err != nil {
		return nil, err
	}
	projected := *existing
	if patch.Name != nil {
		name, err := ValidatePromptTemplateName(*patch.Name)
		if err != nil {
			return nil, err
		}
		projected.Name = name
	}
	if patch.Body != nil {
		body, err := ValidatePromptTemplateBody(*patch.Body)
		if err != nil {
			return nil, err
		}
		projected.Body = body
	}
	if patch.AllowedStates != nil {
		states, err := ValidatePromptTemplateStates(*patch.AllowedStates)
		if err != nil {
			return nil, err
		}
		projected.AllowedStates = states
	}
	return &projected, nil
}

// UpdatePromptTemplate applies a partial patch (name / body / states).
// Returns the refreshed row.
func (s *Store) UpdatePromptTemplate(slug string, patch UpdatePromptTemplatePatch) (*PromptTemplate, error) {
	projected, err := s.ValidateUpdatePromptTemplate(slug, patch)
	if err != nil {
		return nil, err
	}
	encoded, err := encodeStates(projected.AllowedStates)
	if err != nil {
		return nil, err
	}
	res, err := s.DB.Exec(`
		UPDATE prompt_templates
		   SET name = ?, body = ?, allowed_states_json = ?, updated_at = CURRENT_TIMESTAMP
		 WHERE slug = ?`,
		projected.Name, projected.Body, encoded, slug)
	if err != nil {
		return nil, wrapTemplateConflict(err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, ErrNotFound
	}
	return s.GetPromptTemplateBySlug(slug)
}

// RenamePromptTemplateIn is the input for RenamePromptTemplate. NewSlug
// may equal the existing slug (a name-only rename); NewName may be empty
// to leave the display name untouched.
type RenamePromptTemplateIn struct {
	OldSlug string
	NewSlug string
	NewName string
}

// RenamePromptTemplate atomically renames a template's slug (and
// optionally its display name), cascading the slug change to
// agent_dispatches.mode so historical rows still resolve. Built-in
// templates can be renamed too — the original built-in slug becomes
// available again for `restore-defaults` to re-seed.
//
// The transaction does: lookup → update prompt_templates →
// UPDATE agent_dispatches SET mode = new WHERE mode = old → commit. A
// no-op rename (newSlug equals oldSlug AND newName empty/equal) is
// rejected with a clear error rather than silently doing nothing — an
// agent should know they sent a payload with no actual change.
func (s *Store) RenamePromptTemplate(in RenamePromptTemplateIn) (*PromptTemplate, error) {
	if _, err := ValidatePromptTemplateSlug(in.OldSlug); err != nil {
		return nil, fmt.Errorf("old slug: %w", err)
	}
	if _, err := ValidatePromptTemplateSlug(in.NewSlug); err != nil {
		return nil, fmt.Errorf("new slug: %w", err)
	}
	var cleanedName string
	if in.NewName != "" {
		name, err := ValidatePromptTemplateName(in.NewName)
		if err != nil {
			return nil, err
		}
		cleanedName = name
	}

	existing, err := s.GetPromptTemplateBySlug(in.OldSlug)
	if err != nil {
		return nil, err
	}
	finalName := existing.Name
	if cleanedName != "" {
		finalName = cleanedName
	}
	if in.OldSlug == in.NewSlug && finalName == existing.Name {
		return nil, fmt.Errorf("rename is a no-op: slug and name unchanged")
	}

	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`
		UPDATE prompt_templates
		   SET slug = ?, name = ?, updated_at = CURRENT_TIMESTAMP
		 WHERE slug = ?`, in.NewSlug, finalName, in.OldSlug); err != nil {
		return nil, wrapTemplateConflict(err)
	}
	if in.OldSlug != in.NewSlug {
		if _, err := tx.Exec(`
			UPDATE agent_dispatches SET mode = ? WHERE mode = ?`, in.NewSlug, in.OldSlug); err != nil {
			return nil, fmt.Errorf("cascade rename to agent_dispatches.mode: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetPromptTemplateBySlug(in.NewSlug)
}

// DeletePromptTemplate removes a template by slug. Historical
// agent_dispatches rows that reference the slug are left intact —
// they're a snapshot of what was dispatched, not a live FK.
func (s *Store) DeletePromptTemplate(slug string) (*PromptTemplate, error) {
	existing, err := s.GetPromptTemplateBySlug(slug)
	if err != nil {
		return nil, err
	}
	if _, err := s.DB.Exec(`DELETE FROM prompt_templates WHERE slug = ?`, slug); err != nil {
		return nil, err
	}
	return existing, nil
}

// RestoreBuiltinPromptTemplates re-seeds any built-in template slug
// (model.BuiltinTemplateSlugs) that doesn't currently have a row.
// Idempotent: existing slugs (whether the user edited them or not) are
// untouched. Returns the slugs that were re-created, in canonical
// order.
func (s *Store) RestoreBuiltinPromptTemplates() ([]string, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	existing := map[string]bool{}
	rows, err := tx.Query(`SELECT slug FROM prompt_templates WHERE slug IN (` + placeholders(len(model.BuiltinTemplateSlugs())) + `)`,
		toAnySlice(model.BuiltinTemplateSlugs())...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			rows.Close()
			return nil, err
		}
		existing[slug] = true
	}
	rows.Close()

	var created []string
	for _, slug := range model.BuiltinTemplateSlugs() {
		if existing[slug] {
			continue
		}
		body := model.DefaultPromptBodyForBuiltinSlug(slug)
		states := model.DefaultPromptStatesForBuiltinSlug(slug)
		encoded, err := encodeStates(states)
		if err != nil {
			return nil, err
		}
		name := model.BuiltinTemplateLabel(slug)
		if name == "" {
			name = slug
		}
		if _, err := tx.Exec(`
			INSERT INTO prompt_templates (slug, name, body, allowed_states_json, is_builtin, created_at, updated_at)
			VALUES (?, ?, ?, ?, 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
			slug, name, body, encoded); err != nil {
			return nil, wrapTemplateConflict(err)
		}
		created = append(created, slug)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return created, nil
}

// placeholders returns "?,?,?" for an N-element IN-clause.
func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat("?,", n-1) + "?"
}

// toAnySlice converts a []string into []any for variadic Query/Exec
// argument forwarding.
func toAnySlice(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}

// wrapTemplateConflict translates SQLite UNIQUE-violation errors on
// prompt_templates into clearer messages. The driver surfaces UNIQUE
// breaches as a string containing "UNIQUE constraint failed: ...";
// match on that to identify which column collided.
func wrapTemplateConflict(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if !strings.Contains(msg, "UNIQUE") && !strings.Contains(msg, "unique") {
		return err
	}
	switch {
	case strings.Contains(msg, "prompt_templates.slug"):
		return fmt.Errorf("a template with that slug already exists")
	case strings.Contains(msg, "uniq_prompt_templates_name_ci"):
		return fmt.Errorf("a template with that name already exists (case-insensitive)")
	default:
		return err
	}
}
