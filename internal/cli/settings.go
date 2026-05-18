package cli

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/inputio"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// promptTemplateView is the full CLI/JSON shape for one prompt
// template, returned by `settings template show / set / reset / add /
// rename`. Body is the persisted template; Default is the built-in
// embedded default for the slug (empty for user-created templates);
// IsDefault reports whether Body still matches Default. AllowedStates
// is the state-gate; DefaultStates is the built-in default for the
// slug; StatesAreDefault reports whether the gate still matches the
// default.
type promptTemplateView struct {
	Slug                    string   `json:"slug"`
	Label                   string   `json:"label"`
	Body                    string   `json:"body"`
	Default                 string   `json:"default"`
	IsDefault               bool     `json:"is_default"`
	IsBuiltin               bool     `json:"is_builtin"`
	AllowedStates           []string `json:"allowed_states"`
	DefaultStates           []string `json:"default_states"`
	StatesAreDefault        bool     `json:"states_are_default"`
	ConcurrencyLimit        int      `json:"concurrency_limit"`
	DefaultConcurrencyLimit int      `json:"default_concurrency_limit"`
	ConcurrencyIsDefault    bool     `json:"concurrency_is_default"`
}

// promptTemplateSummary is the lean shape `settings template list`
// returns — it drops the Default text so a bulk read stays small.
// Fetch the default via `settings template show`.
type promptTemplateSummary struct {
	Slug             string   `json:"slug"`
	Label            string   `json:"label"`
	Body             string   `json:"body"`
	IsBuiltin        bool     `json:"is_builtin"`
	IsDefault        bool     `json:"is_default"`
	AllowedStates    []string `json:"allowed_states"`
	ConcurrencyLimit int      `json:"concurrency_limit"`
}

func newSettingsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "settings",
		Short: "Inspect and edit global bacio settings",
		Long: `Global (not per-repo) bacio settings, stored in the local SQLite
store. Today this covers the dispatch prompt templates — the
instruction text bacio renders for each template when you dispatch
work to an agent. The same templates are editable from the desktop
app's Settings panel and the TUI Settings tab; this is the CLI
surface for them.`,
	}
	cmd.AddCommand(newSettingsTemplateCmd())
	return cmd
}

func newSettingsTemplateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "template",
		Short: "Inspect and edit dispatch prompt templates",
		Long: `A dispatch prompt template is the instruction body bacio renders
when you queue work with ` + "`bacio agent dispatch --mode <slug>`" + `.
Bacio ships with five built-in templates (plan, implement, review,
ship, fix_review) that are seeded on first run; you can edit, rename,
delete or replace any of them, and you can add your own.

Templates are global, not per-repo. Bodies may use the {{issue_id}},
{{issue_title}} and {{repo_prefix}} placeholders, substituted with
the issue's context at dispatch time.

Use ` + "`bacio settings template restore-defaults`" + ` to re-seed any
built-in slug that's been deleted (idempotent).`,
	}
	cmd.AddCommand(
		settingsTemplateListCmd(),
		settingsTemplateShowCmd(),
		settingsTemplateSetCmd(),
		settingsTemplateResetCmd(),
		settingsTemplateAddCmd(),
		settingsTemplateRenameCmd(),
		settingsTemplateRmCmd(),
		settingsTemplateRestoreDefaultsCmd(),
		settingsTemplateStatesCmd(),
		settingsTemplateSetConcurrencyCmd(),
	)
	return cmd
}

// newSettingsTemplateStatesCmd is the `states` sub-group: the issue
// states each template's dispatch prompt is valid to run from. The
// desktop app + TUI gate the per-card action button on this.
func settingsTemplateStatesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "states",
		Short: "Inspect and edit the issue states each template's prompt is valid from",
		Long: `Each dispatch prompt template declares the set of issue states it is
valid to run from — its "state-gate". The desktop app and TUI's
per-card action button only offers a template when the card's current
state is in that template's gate. State-gates are global, not per-repo.`,
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
		Use:   "show <slug>",
		Short: "Show the valid-from issue states for one template's prompt",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireLocalForSettings("template states show"); err != nil {
				return err
			}
			slug, err := templateSlugArg(args[0])
			if err != nil {
				return err
			}
			c, err := openClient()
			if err != nil {
				return err
			}
			defer c.Close()
			t, err := c.GetPromptTemplate(context.Background(), slug)
			if err != nil {
				return wrapTemplateLookup(slug, err)
			}
			return emit(templateViewForRow(t))
		},
	}
}

func settingsTemplateStatesSetCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "set [SLUG] [STATES]",
		Short: "Set the issue states a template's prompt is valid to run from",
		Long: `Override the state-gate for a template. STATES is a comma-separated
list of canonical issue states (todo, in_progress, needs_action,
in_review, done, cancelled). To revert a built-in template's gate to
its embedded default, use ` + "`bacio settings template states reset`" + `.`,
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
					return fmt.Errorf("states is required; use `bacio settings template states reset` to revert a built-in to its default")
				}
				return applyTemplateStates(in.Slug, in.States)
			}
			if len(args) != 2 {
				return fmt.Errorf("requires <SLUG> <STATES> positionals or --json")
			}
			states, err := parseStates(args[1])
			if err != nil {
				return err
			}
			if len(states) == 0 {
				return fmt.Errorf("states is required; use `bacio settings template states reset` to revert a built-in to its default")
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
		Use:   "reset [SLUG]",
		Short: "Reset a built-in template's state-gate to its embedded default",
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
				return applyTemplateStates(in.Slug, nil)
			}
			if len(args) != 1 {
				return fmt.Errorf("requires <SLUG> positional or --json")
			}
			return applyTemplateStates(args[0], nil)
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}

// (non-empty list) and `states reset` (empty list = revert built-in
// to its default). It honours --dry-run.
func applyTemplateStates(slugArg string, states []string) error {
	if err := requireLocalForSettings("template states set"); err != nil {
		return err
	}
	slug, err := templateSlugArg(slugArg)
	if err != nil {
		return err
	}
	c, err := openClient()
	if err != nil {
		return err
	}
	defer c.Close()
	ctx := context.Background()
	if err := c.SetPromptStates(ctx, slug, states, opts.dryRun); err != nil {
		return err
	}
	if opts.dryRun {
		t, err := c.GetPromptTemplate(ctx, slug)
		if err != nil {
			return wrapTemplateLookup(slug, err)
		}
		projected := *t
		if len(states) > 0 {
			parsed := make([]model.State, len(states))
			for i, s := range states {
				parsed[i] = model.State(s)
			}
			projected.AllowedStates = parsed
		} else if t.IsBuiltin {
			projected.AllowedStates = model.DefaultPromptStatesForBuiltinSlug(slug)
		}
		return emitDryRun(templateViewForRow(&projected))
	}
	t, err := c.GetPromptTemplate(ctx, slug)
	if err != nil {
		return wrapTemplateLookup(slug, err)
	}
	return emit(templateViewForRow(t))
}

// requireLocalForSettings short-circuits settings verbs in remote mode —
// the prompt_templates table (like the agent registry) is local-only.
func requireLocalForSettings(verb string) error {
	if inRemoteMode() {
		return fmt.Errorf("bacio settings %s is local-only in v1 — drop --remote / unset BACIO_REMOTE (prompt templates live only in the local SQLite store)", verb)
	}
	return nil
}

// templateSlugArg validates the slug shape (matching ParseDispatchMode
// + non-empty); existence is enforced at the store boundary.
func templateSlugArg(s string) (string, error) {
	mode, err := model.ParseDispatchMode(s)
	if err != nil {
		return "", err
	}
	if mode == "" {
		return "", fmt.Errorf("a template slug is required")
	}
	return string(mode), nil
}

func statesToStrings(states []model.State) []string {
	out := make([]string, len(states))
	for i, st := range states {
		out[i] = string(st)
	}
	return out
}

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

func templateViewForRow(t *store.PromptTemplate) *promptTemplateView {
	def := model.DefaultPromptBodyForBuiltinSlug(t.Slug)
	defStates := model.DefaultPromptStatesForBuiltinSlug(t.Slug)
	label := t.Name
	if label == "" {
		label = model.BuiltinTemplateLabel(t.Slug)
		if label == "" {
			label = t.Slug
		}
	}
	defConc := model.DefaultConcurrencyLimit(t.Slug)
	return &promptTemplateView{
		Slug:                    t.Slug,
		Label:                   label,
		Body:                    t.Body,
		Default:                 def,
		IsDefault:               t.IsBuiltin && t.Body == def,
		IsBuiltin:               t.IsBuiltin,
		AllowedStates:           statesToStrings(t.AllowedStates),
		DefaultStates:           statesToStrings(defStates),
		StatesAreDefault:        t.IsBuiltin && sameStates(t.AllowedStates, defStates),
		ConcurrencyLimit:        t.ConcurrencyLimit,
		DefaultConcurrencyLimit: defConc,
		ConcurrencyIsDefault:    t.IsBuiltin && t.ConcurrencyLimit == defConc,
	}
}

// templateSummaryForRow is the lean shape for list output.
func templateSummaryForRow(t *store.PromptTemplate) *promptTemplateSummary {
	def := model.DefaultPromptBodyForBuiltinSlug(t.Slug)
	label := t.Name
	if label == "" {
		label = model.BuiltinTemplateLabel(t.Slug)
		if label == "" {
			label = t.Slug
		}
	}
	return &promptTemplateSummary{
		Slug:             t.Slug,
		Label:            label,
		Body:             t.Body,
		IsBuiltin:        t.IsBuiltin,
		IsDefault:        t.IsBuiltin && t.Body == def,
		AllowedStates:    statesToStrings(t.AllowedStates),
		ConcurrencyLimit: t.ConcurrencyLimit,
	}
}

// wrapTemplateLookup turns store.ErrNotFound from a slug lookup into
// the user-friendly "no template named X" message.
func wrapTemplateLookup(slug string, err error) error {
	if err == nil {
		return nil
	}
	if errMsg := err.Error(); strings.Contains(errMsg, "not found") || strings.Contains(errMsg, "no rows") {
		return fmt.Errorf("no template named %q is registered (see `bacio settings template list`)", slug)
	}
	// store.ErrNotFound is a sentinel — check with errors.Is via client.
	if err == store.ErrNotFound {
		return fmt.Errorf("no template named %q is registered (see `bacio settings template list`)", slug)
	}
	return err
}

// parseStates parses a comma-separated state list. Empty input yields
// an empty slice — the "reset to default" signal.
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
		Short: "List every registered dispatch prompt template",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := openClient()
			if err != nil {
				return err
			}
			defer c.Close()
			tmpls, err := c.ListPromptTemplates(context.Background())
			if err != nil {
				return err
			}
			out := make([]*promptTemplateSummary, 0, len(tmpls))
			for _, t := range tmpls {
				out = append(out, templateSummaryForRow(t))
			}
			return emit(out)
		},
	}
}

func settingsTemplateShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <slug>",
		Short: "Show one dispatch prompt template",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireLocalForSettings("template show"); err != nil {
				return err
			}
			slug, err := templateSlugArg(args[0])
			if err != nil {
				return err
			}
			c, err := openClient()
			if err != nil {
				return err
			}
			defer c.Close()
			t, err := c.GetPromptTemplate(context.Background(), slug)
			if err != nil {
				return wrapTemplateLookup(slug, err)
			}
			return emit(templateViewForRow(t))
		},
	}
}

func settingsTemplateSetCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "set [SLUG] [BODY]",
		Short: "Update a dispatch prompt template's body",
		Long: `Update the body of an existing template. BODY may use the
{{issue_id}}, {{issue_title}} and {{repo_prefix}} placeholders — they're
substituted with the issue's context at dispatch time. To revert a
built-in template to its embedded default, use ` + "`bacio settings template reset`" + `.`,
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
					return fmt.Errorf("body is required; use `bacio settings template reset` to revert a built-in to its default")
				}
				return applyTemplateBody(in.Slug, in.Body)
			}
			if len(args) != 2 {
				return fmt.Errorf("requires <SLUG> <BODY> positionals or --json")
			}
			if args[1] == "" {
				return fmt.Errorf("body is required; use `bacio settings template reset` to revert a built-in to its default")
			}
			return applyTemplateBody(args[0], args[1])
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}

func settingsTemplateResetCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "reset [SLUG]",
		Short: "Reset a built-in template's body to its embedded default",
		Long: `Restore the embedded default body for a built-in template (plan,
implement, review, ship, fix_review). User-created templates have no
embedded default — edit the body directly via ` + "`bacio settings template set`" + `
or delete the template via ` + "`bacio settings template rm`" + `.`,
		Args: cobra.RangeArgs(0, 1),
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
				return applyTemplateBody(in.Slug, "")
			}
			if len(args) != 1 {
				return fmt.Errorf("requires <SLUG> positional or --json")
			}
			return applyTemplateBody(args[0], "")
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}

// settingsTemplateSetConcurrencyCmd is the BACI-51 cap-the-queue verb.
// Caps the per-(repo, slug) in-flight dispatches the matcher will
// allow. 0 = unlimited; positive integers cap. Built-in defaults are
// 0 except `ship` which seeds to 1 so merging serialises.
func settingsTemplateSetConcurrencyCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "set-concurrency [SLUG] [LIMIT]",
		Short: "Set a template's per-(repo, slug) in-flight dispatch cap (BACI-51); 0 = unlimited",
		Long: `Update the concurrency_limit on a dispatch prompt template. The BACI-51
matcher reads this column to decide whether to bind another queued
dispatch for a given (repo, slug) pair — at most concurrency_limit
in-flight (pending+delivered, excluding bacio-channel setup rows) at
a time.

0 means unlimited (the matcher binds whenever a free agent is
available). The ` + "`ship`" + ` template seeds to 1 by default so merges
serialise; all other built-ins seed to 0.`,
		Args: cobra.RangeArgs(0, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := parseJSONInput(cmd, args, rawInput)
			if err != nil {
				return err
			}
			var in inputs.SettingsTemplateSetConcurrencyInput
			if raw != nil {
				parsed, _, err := inputio.DecodeStrict[inputs.SettingsTemplateSetConcurrencyInput](raw)
				if err != nil {
					return err
				}
				in = *parsed
			} else {
				if len(args) != 2 {
					return fmt.Errorf("requires <SLUG> <LIMIT> positionals or --json")
				}
				limit, err := parsePositiveOrZeroInt(args[1])
				if err != nil {
					return err
				}
				in = inputs.SettingsTemplateSetConcurrencyInput{Slug: args[0], ConcurrencyLimit: limit}
			}
			return applyTemplateConcurrency(in)
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}

// parsePositiveOrZeroInt parses a >=0 integer — the validator on
// concurrency_limit rejects negatives, so do the same on the positional
// path with a clearer message.
func parsePositiveOrZeroInt(s string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0, fmt.Errorf("concurrency_limit must be an integer >= 0, got %q", s)
	}
	if n < 0 {
		return 0, fmt.Errorf("concurrency_limit must be >= 0, got %d", n)
	}
	return n, nil
}

// applyTemplateConcurrency is the shared write path for the
// set-concurrency verb. Honours --dry-run.
func applyTemplateConcurrency(in inputs.SettingsTemplateSetConcurrencyInput) error {
	if err := requireLocalForSettings("template set-concurrency"); err != nil {
		return err
	}
	slug, err := templateSlugArg(in.Slug)
	if err != nil {
		return err
	}
	in.Slug = slug
	c, err := openClient()
	if err != nil {
		return err
	}
	defer c.Close()
	t, err := c.SetPromptTemplateConcurrencyLimit(context.Background(), in, opts.dryRun)
	if err != nil {
		return wrapTemplateLookup(slug, err)
	}
	if opts.dryRun {
		return emitDryRun(templateViewForRow(t))
	}
	return emit(templateViewForRow(t))
}

// applyTemplateBody is the shared mutation path for `set` (non-empty)
// and `reset` (empty = revert built-in to its default). It honours
// --dry-run.
func applyTemplateBody(slugArg, body string) error {
	if err := requireLocalForSettings("template set"); err != nil {
		return err
	}
	slug, err := templateSlugArg(slugArg)
	if err != nil {
		return err
	}
	// Reset semantic check: only meaningful for built-ins.
	if body == "" {
		c, err := openClient()
		if err != nil {
			return err
		}
		defer c.Close()
		t, err := c.GetPromptTemplate(context.Background(), slug)
		if err != nil {
			return wrapTemplateLookup(slug, err)
		}
		if !t.IsBuiltin {
			return fmt.Errorf("template %q is user-created and has no embedded default — edit its body via `bacio settings template set` or delete via `bacio settings template rm`", slug)
		}
	}
	c, err := openClient()
	if err != nil {
		return err
	}
	defer c.Close()
	ctx := context.Background()
	if err := c.SetPromptTemplate(ctx, slug, body, opts.dryRun); err != nil {
		return err
	}
	if opts.dryRun {
		t, err := c.GetPromptTemplate(ctx, slug)
		if err != nil {
			return wrapTemplateLookup(slug, err)
		}
		projected := *t
		if body != "" {
			projected.Body = body
		} else if t.IsBuiltin {
			projected.Body = model.DefaultPromptBodyForBuiltinSlug(slug)
		}
		return emitDryRun(templateViewForRow(&projected))
	}
	t, err := c.GetPromptTemplate(ctx, slug)
	if err != nil {
		return wrapTemplateLookup(slug, err)
	}
	return emit(templateViewForRow(t))
}

func settingsTemplateAddCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "add [SLUG] [NAME]",
		Short: "Create a new dispatch prompt template",
		Long: `Create a new dispatch prompt template. The richest path is --json,
which supports body and a state-gate as well as slug + name; the
positional form is a quick "scaffold with empty body, no state-gate"
shortcut you can then edit via the desktop / TUI / `+"`set`/`states`"+`.

Examples:

  bacio settings template add --json '{"slug":"spike","name":"Spike","body":"Spike on {{issue_id}}.","states":["todo"]}'
  bacio settings template add spike Spike     # body empty, no state-gate`,
		Args: cobra.RangeArgs(0, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireLocalForSettings("template add"); err != nil {
				return err
			}
			raw, err := parseJSONInput(cmd, args, rawInput)
			if err != nil {
				return err
			}
			var in inputs.SettingsTemplateAddInput
			if raw != nil {
				parsed, _, err := inputio.DecodeStrict[inputs.SettingsTemplateAddInput](raw)
				if err != nil {
					return err
				}
				in = *parsed
			} else {
				if len(args) != 2 {
					return fmt.Errorf("requires <SLUG> <NAME> positionals or --json")
				}
				in = inputs.SettingsTemplateAddInput{Slug: args[0], Name: args[1]}
			}
			c, err := openClient()
			if err != nil {
				return err
			}
			defer c.Close()
			t, err := c.AddPromptTemplate(context.Background(), in, opts.dryRun)
			if err != nil {
				return err
			}
			if opts.dryRun {
				return emitDryRun(templateViewForRow(t))
			}
			return emit(templateViewForRow(t))
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}

func settingsTemplateRenameCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "rename [SLUG] [NEW-SLUG] [NEW-NAME]",
		Short: "Rename a dispatch prompt template (slug change cascades to historical dispatches)",
		Long: `Rename a template — its slug and/or its display name. A slug change
cascades to ` + "`agent_dispatches.mode`" + ` rows that referenced it, so the
history surface continues to resolve. NEW-NAME is optional; pass an
empty string (or omit the third positional) to leave the display name
unchanged.

Examples:

  bacio settings template rename --json '{"slug":"spike","new_slug":"investigation","new_name":"Investigation"}'
  bacio settings template rename spike investigation Investigation
  bacio settings template rename spike spike "Quick spike"   # name-only rename`,
		Args: cobra.RangeArgs(0, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireLocalForSettings("template rename"); err != nil {
				return err
			}
			raw, err := parseJSONInput(cmd, args, rawInput)
			if err != nil {
				return err
			}
			var in inputs.SettingsTemplateRenameInput
			if raw != nil {
				parsed, _, err := inputio.DecodeStrict[inputs.SettingsTemplateRenameInput](raw)
				if err != nil {
					return err
				}
				in = *parsed
			} else {
				if len(args) < 2 {
					return fmt.Errorf("requires <SLUG> <NEW-SLUG> [NEW-NAME] positionals or --json")
				}
				in = inputs.SettingsTemplateRenameInput{Slug: args[0], NewSlug: args[1]}
				if len(args) == 3 {
					in.NewName = args[2]
				}
			}
			c, err := openClient()
			if err != nil {
				return err
			}
			defer c.Close()
			t, err := c.RenamePromptTemplate(context.Background(), in, opts.dryRun)
			if err != nil {
				return err
			}
			if opts.dryRun {
				return emitDryRun(templateViewForRow(t))
			}
			return emit(templateViewForRow(t))
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}

func settingsTemplateRmCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "rm [SLUG]",
		Short: "Delete a dispatch prompt template",
		Long: `Delete a template. Built-in templates can be deleted too; restore
them with ` + "`bacio settings template restore-defaults`" + ` (idempotent).
Historical ` + "`agent_dispatches.mode`" + ` rows that reference the slug are
left intact — a dispatch is a snapshot, not a live foreign key.`,
		Args: cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireLocalForSettings("template rm"); err != nil {
				return err
			}
			raw, err := parseJSONInput(cmd, args, rawInput)
			if err != nil {
				return err
			}
			var in inputs.SettingsTemplateRmInput
			if raw != nil {
				parsed, _, err := inputio.DecodeStrict[inputs.SettingsTemplateRmInput](raw)
				if err != nil {
					return err
				}
				in = *parsed
			} else {
				if len(args) != 1 {
					return fmt.Errorf("requires <SLUG> positional or --json")
				}
				in.Slug = args[0]
			}
			c, err := openClient()
			if err != nil {
				return err
			}
			defer c.Close()
			t, err := c.DeletePromptTemplate(context.Background(), in, opts.dryRun)
			if err != nil {
				return wrapTemplateLookup(in.Slug, err)
			}
			if opts.dryRun {
				return emitDryRun(templateViewForRow(t))
			}
			return emit(templateViewForRow(t))
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}

func settingsTemplateRestoreDefaultsCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "restore-defaults",
		Short: "Re-seed any missing built-in dispatch prompt templates (idempotent)",
		Long: `Re-seed every built-in template slug (plan, design, implement,
review, ship, fix_review) that doesn't currently have a row, using the embedded
default body and state-gate. Existing rows (whether the user has
edited them or not) are left alone. The output lists the slugs that
were re-created.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireLocalForSettings("template restore-defaults"); err != nil {
				return err
			}
			raw, err := parseJSONInput(cmd, args, rawInput)
			if err != nil {
				return err
			}
			if raw != nil {
				if _, _, err = inputio.DecodeStrict[inputs.SettingsTemplateRestoreDefaultsInput](raw); err != nil {
					return err
				}
			}
			c, err := openClient()
			if err != nil {
				return err
			}
			defer c.Close()
			created, err := c.RestoreBuiltinPromptTemplates(context.Background(), opts.dryRun)
			if err != nil {
				return err
			}
			out := struct {
				Created []string `json:"created"`
			}{Created: created}
			if opts.dryRun {
				return emitDryRun(out)
			}
			return emit(out)
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}
