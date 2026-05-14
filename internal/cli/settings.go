package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/inputio"
	"github.com/mrgeoffrich/bacio/internal/model"
)

// promptTemplateView is the full CLI/JSON shape for one dispatch prompt
// template, returned by `settings template show` / `set` / `reset`.
// Body is the effective template (the custom override, or Default when
// none is set); IsDefault reports whether Body still matches Default.
type promptTemplateView struct {
	Mode      string `json:"mode"`
	Label     string `json:"label"`
	Body      string `json:"body"`
	Default   string `json:"default"`
	IsDefault bool   `json:"is_default"`
}

// promptTemplateSummary is the lean shape `settings template list`
// returns — it drops the (redundant) built-in Default text so a bulk
// read stays small. Fetch the default via `settings template show`.
type promptTemplateSummary struct {
	Mode      string `json:"mode"`
	Label     string `json:"label"`
	Body      string `json:"body"`
	IsDefault bool   `json:"is_default"`
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
	)
	return cmd
}

// requireLocalForSettings short-circuits settings verbs in remote mode —
// app_settings (like the agent registry) is local-only in v1, so a
// clear message beats a confusing network error.
func requireLocalForSettings(verb string) error {
	if inRemoteMode() {
		return fmt.Errorf("bacio settings %s is local-only in v1 — drop --remote / unset BACIO_REMOTE (prompt templates live only in the local SQLite store)", verb)
	}
	return nil
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

func templateViewFor(mode model.DispatchMode, body string) *promptTemplateView {
	def := model.DefaultPromptTemplate(mode)
	return &promptTemplateView{
		Mode:      string(mode),
		Label:     promptTemplateLabels[mode],
		Body:      body,
		Default:   def,
		IsDefault: body == def,
	}
}

func settingsTemplateListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the dispatch prompt template for every job stage",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireLocalForSettings("template list"); err != nil {
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
			out := make([]*promptTemplateSummary, 0, len(model.AllDispatchModes()))
			for _, m := range model.AllDispatchModes() {
				body := current[string(m)]
				out = append(out, &promptTemplateSummary{
					Mode:      string(m),
					Label:     promptTemplateLabels[m],
					Body:      body,
					IsDefault: body == model.DefaultPromptTemplate(m),
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
			if err := requireLocalForSettings("template show"); err != nil {
				return err
			}
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
			return emit(templateViewFor(mode, current[string(mode)]))
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
	if err := requireLocalForSettings("template set"); err != nil {
		return err
	}
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
		// built-in default, exactly as a real call would resolve it.
		resolved := body
		if resolved == "" {
			resolved = model.DefaultPromptTemplate(mode)
		}
		return emitDryRun(templateViewFor(mode, resolved))
	}
	current, err := c.GetPromptTemplates(ctx)
	if err != nil {
		return err
	}
	return emit(templateViewFor(mode, current[string(mode)]))
}
