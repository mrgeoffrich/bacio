package main

import (
	"context"

	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/model"
)

// PromptTemplateDTO is one editable dispatch prompt template, shaped for
// the desktop Settings panel. Body is the effective template (the user's
// custom override, or Default when none is set); IsDefault reports
// whether Body still matches the built-in default. AllowedStates is the
// effective state-gate — the issue states this stage's prompt is valid
// to run from — with DefaultStates the built-in set and StatesAreDefault
// whether AllowedStates still matches it.
type PromptTemplateDTO struct {
	Mode             string   `json:"mode"`
	Label            string   `json:"label"`
	Body             string   `json:"body"`
	Default          string   `json:"default"`
	IsDefault        bool     `json:"isDefault"`
	AllowedStates    []string `json:"allowedStates"`
	DefaultStates    []string `json:"defaultStates"`
	StatesAreDefault bool     `json:"statesAreDefault"`
}

// promptTemplateOrder fixes the display order and human labels of the
// five dispatch stages in the Settings panel. The order mirrors a job's
// lifecycle: plan → implement → review → ship → fix a review.
var promptTemplateOrder = []struct {
	Mode  model.DispatchMode
	Label string
}{
	{model.DispatchModePlan, "Planning"},
	{model.DispatchModeImplement, "Implementing"},
	{model.DispatchModeReview, "Reviewing"},
	{model.DispatchModeShip, "Shipping"},
	{model.DispatchModeFixReview, "Fixing a review"},
}

// SettingsService is the Wails-bound API for the desktop Settings panel.
// Today it owns the customisable dispatch prompt templates; it wraps the
// same local bacio client.Client as the rest of the app.
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

// sameStrings reports whether two string slices are equal, in order.
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

// statesToStrings renders a []model.State as a []string, never nil so
// JSON consumers always see an array.
func statesToStrings(states []model.State) []string {
	out := make([]string, len(states))
	for i, st := range states {
		out[i] = string(st)
	}
	return out
}

// dtoFor builds the DTO for one stage from the resolved (custom-or-
// default) template body and state-gate.
func dtoFor(mode model.DispatchMode, label, body string, allowedStates []string) PromptTemplateDTO {
	def := model.DefaultPromptTemplate(mode)
	defStates := statesToStrings(model.DefaultPromptStates(mode))
	return PromptTemplateDTO{
		Mode:             string(mode),
		Label:            label,
		Body:             body,
		Default:          def,
		IsDefault:        body == def,
		AllowedStates:    allowedStates,
		DefaultStates:    defStates,
		StatesAreDefault: sameStrings(allowedStates, defStates),
	}
}

// labelFor returns the human label for a dispatch stage, falling back to
// the raw mode string for an unknown stage.
func labelFor(mode model.DispatchMode) string {
	for _, t := range promptTemplateOrder {
		if t.Mode == mode {
			return t.Label
		}
	}
	return string(mode)
}

// ListPromptTemplates returns the five dispatch prompt templates in
// lifecycle order, each with its effective body, the built-in default,
// the effective + default state-gate, and whether each still matches
// its built-in default.
func (s *SettingsService) ListPromptTemplates() ([]PromptTemplateDTO, error) {
	ctx := context.Background()
	current, err := s.client.GetPromptTemplates(ctx)
	if err != nil {
		return nil, err
	}
	states, err := s.client.GetPromptStates(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]PromptTemplateDTO, 0, len(promptTemplateOrder))
	for _, t := range promptTemplateOrder {
		out = append(out, dtoFor(t.Mode, t.Label, current[string(t.Mode)], states[string(t.Mode)]))
	}
	return out, nil
}

// SavePromptTemplate stores a custom body for one dispatch stage and
// returns the refreshed DTO. An empty body resets the stage to its
// built-in default.
func (s *SettingsService) SavePromptTemplate(mode, body string) (PromptTemplateDTO, error) {
	ctx := context.Background()
	if err := s.client.SetPromptTemplate(ctx, mode, body, false); err != nil {
		return PromptTemplateDTO{}, err
	}
	return s.refreshedDTO(ctx, mode)
}

// SavePromptStates stores a custom state-gate for one dispatch stage —
// the set of issue states the stage's prompt is valid to run from — and
// returns the refreshed DTO. An empty slice resets the stage to its
// built-in default gate.
func (s *SettingsService) SavePromptStates(mode string, states []string) (PromptTemplateDTO, error) {
	ctx := context.Background()
	if err := s.client.SetPromptStates(ctx, mode, states, false); err != nil {
		return PromptTemplateDTO{}, err
	}
	return s.refreshedDTO(ctx, mode)
}

// refreshedDTO re-reads the effective body + state-gate for one stage
// and builds its DTO — the shared tail of SavePromptTemplate /
// SavePromptStates.
func (s *SettingsService) refreshedDTO(ctx context.Context, mode string) (PromptTemplateDTO, error) {
	current, err := s.client.GetPromptTemplates(ctx)
	if err != nil {
		return PromptTemplateDTO{}, err
	}
	states, err := s.client.GetPromptStates(ctx)
	if err != nil {
		return PromptTemplateDTO{}, err
	}
	m := model.DispatchMode(mode)
	return dtoFor(m, labelFor(m), current[mode], states[mode]), nil
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
// preference and returns the refreshed DTO — same "save returns the
// refreshed DTO" shape as SavePromptTemplate.
func (s *SettingsService) SetBoardPreferences(hideEmptyColumns bool) (BoardPreferencesDTO, error) {
	ctx := context.Background()
	if err := s.client.SetBoardPreferences(ctx, client.BoardPreferences{
		HideEmptyColumns: hideEmptyColumns,
	}, false); err != nil {
		return BoardPreferencesDTO{}, err
	}
	return s.GetBoardPreferences()
}
