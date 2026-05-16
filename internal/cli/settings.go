package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/inputio"
	"github.com/mrgeoffrich/bacio/internal/model"
)

// promptTemplateView is the full CLI/JSON shape for one dispatch prompt
// template, returned by `settings template show` / `set` / `reset`.
// Body is the effective template (the custom override, or Default when
// none is set); IsDefault reports whether Body still matches Default.
// AllowedStates is the effective state-gate — the issue states the
// stage's prompt is valid to run from — with DefaultStates the built-in
// set and StatesAreDefault whether AllowedStates still matches it.
type promptTemplateView struct {
	Mode             string   `json:"mode"`
	Label            string   `json:"label"`
	Body             string   `json:"body"`
	Default          string   `json:"default"`
	IsDefault        bool     `json:"is_default"`
	AllowedStates    []string `json:"allowed_states"`
	DefaultStates    []string `json:"default_states"`
	StatesAreDefault bool     `json:"states_are_default"`
}

// promptTemplateSummary is the lean shape `settings template list`
// returns — it drops the (redundant) built-in Default text so a bulk
// read stays small. Fetch the default via `settings template show`.
type promptTemplateSummary struct {
	Mode          string   `json:"mode"`
	Label         string   `json:"label"`
	Body          string   `json:"body"`
	IsDefault     bool     `json:"is_default"`
	AllowedStates []string `json:"allowed_states"`
}

// promptTemplateLabels gives each dispatch stage a human label for text
// output. Mirrors the stage labels in the desktop app's Settings panel.
var promptTemplateLabels = map[model.DispatchMode]string{
	model.DispatchModePlan:      "Planning",
	model.DispatchModeImplement: "Implementing",
	model.DispatchModeReview:    "Reviewing",
	model.DispatchModeShip:      "Shipping",
	model.DispatchModeFixReview: "Fixing a review",
}

func newSettingsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "settings",
		Short: "Inspect and edit global bacio settings",
		Long: `Global (not per-repo) bacio settings, stored in the local app_settings
table. Today this is the dispatch prompt templates — the instruction
text bacio renders for each job stage when you dispatch work to an
agent. The same templates are editable from the desktop app's Settings
panel; this is the CLI surface for them.`,
	}
	cmd.AddCommand(newSettingsTemplateCmd())
	return cmd
}

func newSettingsTemplateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "template",
		Short: "Inspect and edit dispatch prompt templates",
		Long: `A dispatch prompt template is the instruction body bacio renders for a
job stage (plan, implement, review, ship, fix_review) when you queue
work with ` + "`bacio agent dispatch --mode <stage>`" + `. Each stage ships with a
built-in default; override it per-stage here. Templates are global, not
per-repo. Bodies may use the {{issue_id}}, {{issue_title}} and
{{repo_prefix}} placeholders, substituted with the issue's context at
dispatch time.`,
	}
	cmd.AddCommand(
		settingsTemplateListCmd(),
		settingsTemplateShowCmd(),
		settingsTemplateSetCmd(),
		settingsTemplateResetCmd(),
		settingsTemplateStatesCmd(),
	)
	return cmd
}

// newSettingsTemplateStatesCmd is the `states` sub-group: the issue
// states each job stage's dispatch prompt is valid to run from. The
// desktop app gates the per-card action button on this; the CLI is the
// parity surface, mirroring the template body verbs.
func settingsTemplateStatesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "states",
		Short: "Inspect and edit the issue states each job stage's prompt is valid from",
		Long: `Each dispatch prompt stage (plan, implement, review, ship, fix_review)
declares the set of issue states it is valid to run from — its
"state-gate". The desktop app's per-card action button only offers a
prompt when the card's current state is in that stage's gate. Each
stage ships with a built-in default; override it per-stage here.
State-gates are global, not per-repo.`,
	}
	cmd.AddCommand(
		settingsTemplateStatesShowCmd(),
		settingsTemplateStatesSetCmd(),
		settingsTemplateStatesResetCmd(),
	)
	return cmd
}

func settingsTemplateStatesShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <stage>",
		Short: "Show the valid-from issue states for one job stage's prompt",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			mode, err := templateModeArg(args[0])
			if err != nil {
				return err
			}
			c, err := openClient()
			if err != nil {
				return err
			}
			defer c.Close()
			current, err := c.GetPromptTemplates(context.Background())
			if err != nil {
				return err
			}
			allowed, err := clientPromptStates(c, mode)
			if err != nil {
				return err
			}
			return emit(templateViewFor(mode, current[string(mode)], allowed))
		},
	}
}

func settingsTemplateStatesSetCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "set [STAGE] [STATES]",
		Short: "Set the issue states a job stage's prompt is valid to run from",
		Long: `Override the built-in state-gate for a job stage. STATES is a
comma-separated list of canonical issue states (todo, in_progress,
needs_action, in_review, done, cancelled). To revert a stage to its
built-in default gate, use ` + "`bacio settings template states reset`" + `.`,
		Args: cobra.RangeArgs(0, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := parseJSONInput(cmd, args, rawInput)
			if err != nil {
				return err
			}
			if raw != nil {
				in, _, err := inputio.DecodeStrict[inputs.SettingsTemplateStatesSetInput](raw)
				if err != nil {
					return err
				}
				if len(in.States) == 0 {
					return fmt.Errorf("states is required; use `bacio settings template states reset` to revert a stage to its default")
				}
				return applyTemplateStates(in.Mode, in.States)
			}
			if len(args) != 2 {
				return fmt.Errorf("requires <STAGE> <STATES> positionals or --json")
			}
			states, err := parseStates(args[1])
			if err != nil {
				return err
			}
			if len(states) == 0 {
				return fmt.Errorf("states is required; use `bacio settings template states reset` to revert a stage to its default")
			}
			return applyTemplateStates(args[0], statesToStrings(states))
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}

func settingsTemplateStatesResetCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "reset [STAGE]",
		Short: "Reset a job stage's prompt state-gate to its built-in default",
		Args:  cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := parseJSONInput(cmd, args, rawInput)
			if err != nil {
				return err
			}
			if raw != nil {
				in, _, err := inputio.DecodeStrict[inputs.SettingsTemplateStatesResetInput](raw)
				if err != nil {
					return err
				}
				return applyTemplateStates(in.Mode, nil)
			}
			if len(args) != 1 {
				return fmt.Errorf("requires <STAGE> positional or --json")
			}
			return applyTemplateStates(args[0], nil)
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}

// applyTemplateStates is the shared mutation path for `states set`
// (non-empty list) and `states reset` (empty list clears the override).
// It honours --dry-run: the states are validated at the store boundary,
// then the write is short-circuited and the projected template emitted.
func applyTemplateStates(modeArg string, states []string) error {
	mode, err := templateModeArg(modeArg)
	if err != nil {
		return err
	}
	c, err := openClient()
	if err != nil {
		return err
	}
	defer c.Close()
	ctx := context.Background()
	if err := c.SetPromptStates(ctx, string(mode), states, opts.dryRun); err != nil {
		return err
	}
	current, err := c.GetPromptTemplates(ctx)
	if err != nil {
		return err
	}
	if opts.dryRun {
		// Project the resolved state-gate: an empty list reverts to the
		// built-in default, exactly as a real call would resolve it.
		resolved := make([]model.State, len(states))
		for i, s := range states {
			resolved[i] = model.State(s)
		}
		if len(resolved) == 0 {
			resolved = model.DefaultPromptStates(mode)
		}
		return emitDryRun(templateViewFor(mode, current[string(mode)], resolved))
	}
	allowed, err := clientPromptStates(c, mode)
	if err != nil {
		return err
	}
	return emit(templateViewFor(mode, current[string(mode)], allowed))
}

// templateModeArg parses a stage argument, rejecting the empty / untyped
// mode — every settings template verb names a concrete stage.
func templateModeArg(s string) (model.DispatchMode, error) {
	mode, err := model.ParseDispatchMode(s)
	if err != nil {
		return "", err
	}
	if mode == "" {
		return "", fmt.Errorf("a job stage is required (plan, implement, review, ship, fix_review)")
	}
	return mode, nil
}

// statesToStrings renders a []model.State as a []string in order, never
// nil — JSON consumers always see an array.
func statesToStrings(states []model.State) []string {
	out := make([]string, len(states))
	for i, st := range states {
		out[i] = string(st)
	}
	return out
}

// sameStates reports whether two state lists are equal, order-sensitive
// (the store keeps them in the order they were set).
func sameStates(a, b []model.State) bool {
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

func templateViewFor(mode model.DispatchMode, body string, allowedStates []model.State) *promptTemplateView {
	def := model.DefaultPromptTemplate(mode)
	defStates := model.DefaultPromptStates(mode)
	return &promptTemplateView{
		Mode:             string(mode),
		Label:            promptTemplateLabels[mode],
		Body:             body,
		Default:          def,
		IsDefault:        body == def,
		AllowedStates:    statesToStrings(allowedStates),
		DefaultStates:    statesToStrings(defStates),
		StatesAreDefault: sameStates(allowedStates, defStates),
	}
}

// clientPromptStates resolves the state-gate for one stage through the
// client, decoding the wire []string into []model.State.
func clientPromptStates(c client.Client, mode model.DispatchMode) ([]model.State, error) {
	all, err := c.GetPromptStates(context.Background())
	if err != nil {
		return nil, err
	}
	raw := all[string(mode)]
	out := make([]model.State, len(raw))
	for i, s := range raw {
		out[i] = model.State(s)
	}
	return out, nil
}

// parseStates parses a comma-separated state list (the CLI flag-path
// form for `settings template states set`). An empty input yields an
// empty slice — the "reset to default" signal.
func parseStates(csv string) ([]model.State, error) {
	csv = strings.TrimSpace(csv)
	if csv == "" {
		return nil, nil
	}
	parts := strings.Split(csv, ",")
	out := make([]model.State, 0, len(parts))
	for _, p := range parts {
		st, err := model.ParseState(p)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, nil
}

func settingsTemplateListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the dispatch prompt template for every job stage",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := openClient()
			if err != nil {
				return err
			}
			defer c.Close()
			ctx := context.Background()
			current, err := c.GetPromptTemplates(ctx)
			if err != nil {
				return err
			}
			states, err := c.GetPromptStates(ctx)
			if err != nil {
				return err
			}
			out := make([]*promptTemplateSummary, 0, len(model.AllDispatchModes()))
			for _, m := range model.AllDispatchModes() {
				body := current[string(m)]
				out = append(out, &promptTemplateSummary{
					Mode:          string(m),
					Label:         promptTemplateLabels[m],
					Body:          body,
					IsDefault:     body == model.DefaultPromptTemplate(m),
					AllowedStates: states[string(m)],
				})
			}
			return emit(out)
		},
	}
}

func settingsTemplateShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <stage>",
		Short: "Show the dispatch prompt template for one job stage",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			mode, err := templateModeArg(args[0])
			if err != nil {
				return err
			}
			c, err := openClient()
			if err != nil {
				return err
			}
			defer c.Close()
			current, err := c.GetPromptTemplates(context.Background())
			if err != nil {
				return err
			}
			allowed, err := clientPromptStates(c, mode)
			if err != nil {
				return err
			}
			return emit(templateViewFor(mode, current[string(mode)], allowed))
		},
	}
}

func settingsTemplateSetCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "set [STAGE] [BODY]",
		Short: "Set a custom dispatch prompt template for one job stage",
		Long: `Override the built-in prompt template for a job stage. BODY may use the
{{issue_id}}, {{issue_title}} and {{repo_prefix}} placeholders — they're
substituted with the issue's context at dispatch time. To revert a stage
to its built-in default, use ` + "`bacio settings template reset`" + `.`,
		Args: cobra.RangeArgs(0, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := parseJSONInput(cmd, args, rawInput)
			if err != nil {
				return err
			}
			if raw != nil {
				in, _, err := inputio.DecodeStrict[inputs.SettingsTemplateSetInput](raw)
				if err != nil {
					return err
				}
				if in.Body == "" {
					return fmt.Errorf("body is required; use `bacio settings template reset` to revert a stage to its default")
				}
				return applyTemplate(in.Mode, in.Body)
			}
			if len(args) != 2 {
				return fmt.Errorf("requires <STAGE> <BODY> positionals or --json")
			}
			if args[1] == "" {
				return fmt.Errorf("body is required; use `bacio settings template reset` to revert a stage to its default")
			}
			return applyTemplate(args[0], args[1])
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}

func settingsTemplateResetCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "reset [STAGE]",
		Short: "Reset a dispatch prompt template to its built-in default",
		Args:  cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := parseJSONInput(cmd, args, rawInput)
			if err != nil {
				return err
			}
			if raw != nil {
				in, _, err := inputio.DecodeStrict[inputs.SettingsTemplateResetInput](raw)
				if err != nil {
					return err
				}
				return applyTemplate(in.Mode, "")
			}
			if len(args) != 1 {
				return fmt.Errorf("requires <STAGE> positional or --json")
			}
			return applyTemplate(args[0], "")
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}

// applyTemplate is the shared mutation path for `set` (non-empty body)
// and `reset` (empty body clears the override). It honours --dry-run:
// the body is validated at the store boundary, then the write is
// short-circuited and the projected template emitted.
func applyTemplate(modeArg, body string) error {
	mode, err := templateModeArg(modeArg)
	if err != nil {
		return err
	}
	c, err := openClient()
	if err != nil {
		return err
	}
	defer c.Close()
	ctx := context.Background()
	if err := c.SetPromptTemplate(ctx, string(mode), body, opts.dryRun); err != nil {
		return err
	}
	if opts.dryRun {
		// Project the resolved template: an empty body reverts to the
		// built-in default, exactly as a real call would resolve it. The
		// state-gate is untouched by this verb, so show its current value.
		resolved := body
		if resolved == "" {
			resolved = model.DefaultPromptTemplate(mode)
		}
		allowed, err := clientPromptStates(c, mode)
		if err != nil {
			return err
		}
		return emitDryRun(templateViewFor(mode, resolved, allowed))
	}
	current, err := c.GetPromptTemplates(ctx)
	if err != nil {
		return err
	}
	allowed, err := clientPromptStates(c, mode)
	if err != nil {
		return err
	}
	return emit(templateViewFor(mode, current[string(mode)], allowed))
}
