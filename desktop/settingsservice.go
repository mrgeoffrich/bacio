package main

import (
	"context"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
	"github.com/mrgeoffrich/bacio/internal/version"
)

// PromptTemplateDTO is one editable dispatch prompt template, shaped
// for the desktop Settings panel. Slug is the storage / CLI identifier;
// Mode is kept as an alias of Slug for backward-compat with frontend
// code that still keys by `mode`. Body is the persisted body. Default
// is the built-in embedded default for the slug (empty for user-created
// templates); IsDefault reports whether Body still matches it.
// AllowedStates is the state-gate; DefaultStates is the built-in
// default for the slug (empty for user-created); StatesAreDefault
// reports whether the gate still matches.
type PromptTemplateDTO struct {
	Slug             string   `json:"slug"`
	Mode             string   `json:"mode"`
	Label            string   `json:"label"`
	Body             string   `json:"body"`
	Default          string   `json:"default"`
	IsBuiltin        bool     `json:"isBuiltin"`
	IsDefault        bool     `json:"isDefault"`
	AllowedStates    []string `json:"allowedStates"`
	DefaultStates    []string `json:"defaultStates"`
	StatesAreDefault bool     `json:"statesAreDefault"`
}

// SettingsService is the Wails-bound API for the desktop Settings panel.
// It owns the customisable dispatch prompt templates (now arbitrary in
// count and shape — see BACI-31) and the Board UI preferences.
type SettingsService struct {
	client client.Client
}

func NewSettingsService(c client.Client) *SettingsService {
	return &SettingsService{client: c}
}

// PromptPlaceholders returns the placeholder tokens a template body can
// interpolate (e.g. "issue_id"), so the Settings UI can show them next
// to the editors. The tokens are wrapped in {{...}} when substituted.
func (s *SettingsService) PromptPlaceholders() []string {
	return append([]string{}, model.PromptTemplateTokens...)
}

// BacioVersion returns the version string of the bacio binary the
// desktop app is currently running. Surfaced on the Settings panel so
// you can cross-check what the desktop client is running against the
// per-session "Bacio version" the Agents panel shows.
func (s *SettingsService) BacioVersion() string {
	return version.String()
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func statesToStrings(states []model.State) []string {
	out := make([]string, len(states))
	for i, st := range states {
		out[i] = string(st)
	}
	return out
}

// dtoForTemplate maps a store row into the DTO shape the desktop
// consumes.
func dtoForTemplate(t *store.PromptTemplate) PromptTemplateDTO {
	def := model.DefaultPromptBodyForBuiltinSlug(t.Slug)
	defStates := statesToStrings(model.DefaultPromptStatesForBuiltinSlug(t.Slug))
	allowed := statesToStrings(t.AllowedStates)
	label := t.Name
	if label == "" {
		label = model.BuiltinTemplateLabel(t.Slug)
		if label == "" {
			label = t.Slug
		}
	}
	return PromptTemplateDTO{
		Slug:             t.Slug,
		Mode:             t.Slug,
		Label:            label,
		Body:             t.Body,
		Default:          def,
		IsBuiltin:        t.IsBuiltin,
		IsDefault:        t.IsBuiltin && t.Body == def,
		AllowedStates:    allowed,
		DefaultStates:    defStates,
		StatesAreDefault: t.IsBuiltin && sameStrings(allowed, defStates),
	}
}

// ListPromptTemplates returns every registered template in store
// iteration order — the desktop Settings panel renders them in this
// order and the per-card action menu in the Board iterates the same
// list filtered by state-gate.
func (s *SettingsService) ListPromptTemplates() ([]PromptTemplateDTO, error) {
	ctx := context.Background()
	tmpls, err := s.client.ListPromptTemplates(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]PromptTemplateDTO, 0, len(tmpls))
	for _, t := range tmpls {
		out = append(out, dtoForTemplate(t))
	}
	return out, nil
}

// SavePromptTemplate stores a new body for one template slug. An empty
// body reverts a built-in to its embedded default (and is a no-op
// edit for a user-created template).
func (s *SettingsService) SavePromptTemplate(slug, body string) (PromptTemplateDTO, error) {
	ctx := context.Background()
	if err := s.client.SetPromptTemplate(ctx, slug, body, false); err != nil {
		return PromptTemplateDTO{}, err
	}
	return s.refreshedDTO(ctx, slug)
}

// SavePromptStates stores a new state-gate for one template. An empty
// slice reverts a built-in to its embedded default gate.
func (s *SettingsService) SavePromptStates(slug string, states []string) (PromptTemplateDTO, error) {
	ctx := context.Background()
	if err := s.client.SetPromptStates(ctx, slug, states, false); err != nil {
		return PromptTemplateDTO{}, err
	}
	return s.refreshedDTO(ctx, slug)
}

// AddPromptTemplate creates a brand-new template.
func (s *SettingsService) AddPromptTemplate(slug, name, body string, states []string) (PromptTemplateDTO, error) {
	ctx := context.Background()
	t, err := s.client.AddPromptTemplate(ctx, inputs.SettingsTemplateAddInput{
		Slug:   slug,
		Name:   name,
		Body:   body,
		States: states,
	}, false)
	if err != nil {
		return PromptTemplateDTO{}, err
	}
	return dtoForTemplate(t), nil
}

// RenamePromptTemplate renames an existing template — either the slug,
// the display name, or both.
func (s *SettingsService) RenamePromptTemplate(slug, newSlug, newName string) (PromptTemplateDTO, error) {
	ctx := context.Background()
	t, err := s.client.RenamePromptTemplate(ctx, inputs.SettingsTemplateRenameInput{
		Slug:    slug,
		NewSlug: newSlug,
		NewName: newName,
	}, false)
	if err != nil {
		return PromptTemplateDTO{}, err
	}
	return dtoForTemplate(t), nil
}

// DeletePromptTemplate removes a template by slug. The historical
// dispatches that reference the slug are left intact (a dispatch is a
// snapshot, not a live foreign key) — use RestoreBuiltinPromptTemplates
// to re-seed a deleted built-in.
func (s *SettingsService) DeletePromptTemplate(slug string) (PromptTemplateDTO, error) {
	ctx := context.Background()
	t, err := s.client.DeletePromptTemplate(ctx, inputs.SettingsTemplateRmInput{Slug: slug}, false)
	if err != nil {
		return PromptTemplateDTO{}, err
	}
	return dtoForTemplate(t), nil
}

// RestoreBuiltinPromptTemplates re-seeds any built-in slug that doesn't
// currently have a row from the embedded defaults. Idempotent. Returns
// the refreshed full template list so the frontend can update its
// `templates` state in one shot.
func (s *SettingsService) RestoreBuiltinPromptTemplates() ([]PromptTemplateDTO, error) {
	ctx := context.Background()
	if _, err := s.client.RestoreBuiltinPromptTemplates(ctx, false); err != nil {
		return nil, err
	}
	return s.ListPromptTemplates()
}

// refreshedDTO re-reads one template by slug and builds its DTO — the
// shared tail of SavePromptTemplate / SavePromptStates.
func (s *SettingsService) refreshedDTO(ctx context.Context, slug string) (PromptTemplateDTO, error) {
	t, err := s.client.GetPromptTemplate(ctx, slug)
	if err != nil {
		return PromptTemplateDTO{}, err
	}
	return dtoForTemplate(t), nil
}

// BoardPreferencesDTO is the desktop Board's UI preferences, shaped for
// the Settings panel. HideEmptyColumns drops kanban columns with zero
// cards from the Board.
type BoardPreferencesDTO struct {
	HideEmptyColumns bool `json:"hideEmptyColumns"`
}

// GetBoardPreferences returns the persisted desktop Board UI
// preferences (or the built-in defaults when none are stored).
func (s *SettingsService) GetBoardPreferences() (BoardPreferencesDTO, error) {
	prefs, err := s.client.GetBoardPreferences(context.Background())
	if err != nil {
		return BoardPreferencesDTO{}, err
	}
	return BoardPreferencesDTO{HideEmptyColumns: prefs.HideEmptyColumns}, nil
}

// SetBoardPreferences stores the desktop Board's hide-empty-columns
// preference and returns the refreshed DTO.
func (s *SettingsService) SetBoardPreferences(hideEmptyColumns bool) (BoardPreferencesDTO, error) {
	ctx := context.Background()
	if err := s.client.SetBoardPreferences(ctx, client.BoardPreferences{
		HideEmptyColumns: hideEmptyColumns,
	}, false); err != nil {
		return BoardPreferencesDTO{}, err
	}
	return s.GetBoardPreferences()
}
