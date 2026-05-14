package main

import (
	"context"

	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/model"
)

// PromptTemplateDTO is one editable dispatch prompt template, shaped for
// the desktop Settings panel. Body is the effective template (the user's
// custom override, or Default when none is set); IsDefault reports
// whether Body still matches the built-in default.
type PromptTemplateDTO struct {
	Mode      string `json:"mode"`
	Label     string `json:"label"`
	Body      string `json:"body"`
	Default   string `json:"default"`
	IsDefault bool   `json:"isDefault"`
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

// dtoFor builds the DTO for one stage from the resolved (custom-or-
// default) template body.
func dtoFor(mode model.DispatchMode, label, body string) PromptTemplateDTO {
	def := model.DefaultPromptTemplate(mode)
	return PromptTemplateDTO{
		Mode:      string(mode),
		Label:     label,
		Body:      body,
		Default:   def,
		IsDefault: body == def,
	}
}

// ListPromptTemplates returns the five dispatch prompt templates in
// lifecycle order, each with its effective body, the built-in default,
// and whether the body still matches that default.
func (s *SettingsService) ListPromptTemplates() ([]PromptTemplateDTO, error) {
	ctx := context.Background()
	current, err := s.client.GetPromptTemplates(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]PromptTemplateDTO, 0, len(promptTemplateOrder))
	for _, t := range promptTemplateOrder {
		out = append(out, dtoFor(t.Mode, t.Label, current[string(t.Mode)]))
	}
	return out, nil
}

// SavePromptTemplate stores a custom body for one dispatch stage and
// returns the refreshed DTO. An empty body resets the stage to its
// built-in default.
func (s *SettingsService) SavePromptTemplate(mode, body string) (PromptTemplateDTO, error) {
	ctx := context.Background()
	if err := s.client.SetPromptTemplate(ctx, mode, body); err != nil {
		return PromptTemplateDTO{}, err
	}
	current, err := s.client.GetPromptTemplates(ctx)
	if err != nil {
		return PromptTemplateDTO{}, err
	}
	m := model.DispatchMode(mode)
	label := mode
	for _, t := range promptTemplateOrder {
		if t.Mode == m {
			label = t.Label
		}
	}
	return dtoFor(m, label, current[mode]), nil
}
