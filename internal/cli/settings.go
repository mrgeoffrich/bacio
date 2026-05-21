package cli

import (
	"context"
	"fmt"
	"io"
	"os"
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
// default. ActionLabel (BACI-67) is the persisted imperative override;
// DefaultActionLabel is the built-in imperative seed for the slug (empty
// for user-created templates); ActionLabelIsDefault reports whether the
// override still matches the built-in default.
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
	ActionLabel             string   `json:"action_label"`
	DefaultActionLabel      string   `json:"default_action_label"`
	ActionLabelIsDefault    bool     `json:"action_label_is_default"`
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
	ActionLabel      string   `json:"action_label"`
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
	cmd.AddCommand(newSettingsShowArchivedCmd())
	cmd.AddCommand(newSettingsSyncBackgroundCmd())
	return cmd
}

// syncBackgroundResult is the JSON + text shape `bacio settings
// sync-background` returns on both the get and set paths (BACI-89).
type syncBackgroundResult struct {
	BackgroundEnabled bool `json:"background_enabled"`
}

// newSettingsSyncBackgroundCmd implements the BACI-89
// sync.background_enabled toggle. The verb doubles as get and set:
//
//   - `bacio settings sync-background`             — read the current value
//   - `bacio settings sync-background true|false`  — write it
//   - `bacio settings sync-background --json '{"value":false}'` — same write
//
// Background sync is opt-OUT — the value defaults to true once sync is
// configured, so this verb is how a user who wants manual-only sync
// turns the continual-mirror controller ticker off. Local-only — the
// toggle is read by the in-process controller, so it has no remote
// path.
func newSettingsSyncBackgroundCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "sync-background [true|false]",
		Short: "Get or set the BACI-89 sync.background_enabled global toggle (default: true)",
		Long: `Get or set the BACI-89 sync.background_enabled global toggle. When on
(the default — background sync is opt-OUT), the leader-elected
controller continually mirrors every sync-enabled repo on a timer,
running the same pull → import → export → commit → push pipeline a
manual ` + "`bacio sync`" + ` runs. Set to false for manual-only sync.

Examples:

  bacio settings sync-background           # read current value
  bacio settings sync-background false     # disable background sync
  bacio settings sync-background true      # re-enable (default)
  bacio settings sync-background --json '{"value":false}'`,
		Args: cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if inRemoteMode() {
				return fmt.Errorf("bacio settings sync-background: local-only (the toggle is read by the in-process controller)")
			}
			raw, err := parseJSONInput(cmd, args, rawInput)
			if err != nil {
				return err
			}
			s, err := openStore()
			if err != nil {
				return err
			}
			defer s.Close()
			// Get path — no args, no --json.
			if raw == nil && len(args) == 0 {
				cur, err := s.GetSyncBackgroundEnabled()
				if err != nil {
					return err
				}
				return emit(syncBackgroundResult{BackgroundEnabled: cur})
			}
			// Set path — accept --json or a positional bool.
			var value bool
			if raw != nil {
				in, _, err := inputio.DecodeStrict[inputs.SettingsSyncBackgroundInput](raw)
				if err != nil {
					return err
				}
				value = in.Value
			} else {
				v, err := parseBoolPositional(args[0])
				if err != nil {
					return err
				}
				value = v
			}
			payload := syncBackgroundResult{BackgroundEnabled: value}
			if opts.dryRun {
				return emitDryRun(payload)
			}
			if err := s.SetSyncBackgroundEnabled(value); err != nil {
				return err
			}
			recordOp(s, model.HistoryEntry{
				Op:          "sync_pref.update",
				Kind:        "app_setting",
				TargetLabel: "sync.background_enabled",
				Details:     fmt.Sprintf("background_enabled=%t", value),
			})
			return emit(payload)
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}

// showArchivedResult is the JSON + text shape `bacio settings
// show-archived` returns on both the get and set paths. Keeps the
// payload typed so renderText prints a friendly line instead of the
// anonymous-struct fallback (BACI-68).
type showArchivedResult struct {
	ShowArchived bool `json:"show_archived"`
}

// newSettingsShowArchivedCmd implements the BACI-68 display.show_archived
// toggle. The verb doubles as get and set:
//
//   - `bacio settings show-archived`             — read the current value
//   - `bacio settings show-archived true|false`  — write it
//   - `bacio settings show-archived --json '{"value":true}'` — same write
//
// Per-call `--include-archived` on a list command still wins over the
// setting either way.
func newSettingsShowArchivedCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "show-archived [true|false]",
		Short: "Get or set the BACI-68 display.show_archived global toggle (default: false)",
		Long: `Get or set the BACI-68 display.show_archived global toggle. When on,
default list / board / kanban / docs views include archived rows
(otherwise they're hidden). The per-call ` + "`--include-archived`" + ` flag on
a list command overrides this setting for that one call.

Examples:

  bacio settings show-archived            # read current value
  bacio settings show-archived true       # turn on
  bacio settings show-archived false      # turn off (default)
  bacio settings show-archived --json '{"value":true}'`,
		Args: cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := parseJSONInput(cmd, args, rawInput)
			if err != nil {
				return err
			}
			c, err := openClient()
			if err != nil {
				return err
			}
			defer c.Close()
			// Get path — no args, no --json.
			if raw == nil && len(args) == 0 {
				cur, err := c.GetDisplayShowArchived(context.Background())
				if err != nil {
					return err
				}
				return emit(showArchivedResult{ShowArchived: cur})
			}
			// Set path — accept --json or a positional bool.
			var value bool
			if raw != nil {
				in, _, err := inputio.DecodeStrict[inputs.SettingsShowArchivedInput](raw)
				if err != nil {
					return err
				}
				value = in.Value
			} else {
				v, err := parseBoolPositional(args[0])
				if err != nil {
					return err
				}
				value = v
			}
			out, err := c.SetDisplayShowArchived(context.Background(), value, opts.dryRun)
			if err != nil {
				return err
			}
			payload := showArchivedResult{ShowArchived: out}
			if opts.dryRun {
				return emitDryRun(payload)
			}
			return emit(payload)
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}

// parseBoolPositional accepts the same tokens as strconv.ParseBool plus
// "on" / "off" so users can type "bacio settings show-archived on"
// naturally. Returns an actionable error on a typo.
func parseBoolPositional(s string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "t", "1", "yes", "y", "on":
		return true, nil
	case "false", "f", "0", "no", "n", "off":
		return false, nil
	}
	return false, fmt.Errorf("invalid boolean %q (expected true/false)", s)
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
		settingsTemplateSetActionLabelCmd(),
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
	defAction := model.BuiltinTemplateActionLabel(t.Slug)
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
		ActionLabel:             t.ActionLabel,
		DefaultActionLabel:      defAction,
		ActionLabelIsDefault:    t.IsBuiltin && t.ActionLabel == defAction,
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
		ActionLabel:      t.ActionLabel,
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
		Long: `Update the body of an existing template. To revert a built-in
template to its embedded default, use ` + "`bacio settings template reset`" + `.

A dispatch template's body becomes a per-mode subagent's system prompt
(BACI-76). Run ` + "`bacio install-agent`" + ` after editing a body to apply
the change to dispatched workers. {{issue_id}} / {{issue_title}} /
{{repo_prefix}} placeholders are a compose-time feature and do NOT
survive into an agent file — write the body to refer to "the ticket
named in your dispatch prompt" instead.`,
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

// settingsTemplateSetActionLabelCmd is the BACI-67 imperative-label
// setter — focused verb that doesn't round-trip the body or state-gate
// through the heavier `set` payload. An empty value clears the
// override; the UI then derives from the template's Name.
func settingsTemplateSetActionLabelCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "set-action-label [SLUG] [LABEL]",
		Short: "Set a template's action_label — the imperative override on dispatch action menus (BACI-67); empty = derive from name",
		Long: `Update the action_label on a dispatch prompt template. The dispatch
action menus on the kanban card and the issue workspace shelf render
the action_label as the button text, so callers see "Plan" /
"Design" / "Implement" instead of the gerund form ("Planning",
"Designing", …) the activity pill on a taken card uses.

An empty LABEL clears the override — the UI then derives a default
from the template's Name via the gerund→imperative rule. To clear
via --json, pass an explicit empty string ("").`,
		Args: cobra.RangeArgs(0, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := parseJSONInput(cmd, args, rawInput)
			if err != nil {
				return err
			}
			var in inputs.SettingsTemplateSetActionLabelInput
			if raw != nil {
				parsed, _, err := inputio.DecodeStrict[inputs.SettingsTemplateSetActionLabelInput](raw)
				if err != nil {
					return err
				}
				in = *parsed
			} else {
				if len(args) < 1 || len(args) > 2 {
					return fmt.Errorf("requires <SLUG> [LABEL] positionals or --json (empty LABEL clears the override)")
				}
				in = inputs.SettingsTemplateSetActionLabelInput{Slug: args[0]}
				if len(args) == 2 {
					in.ActionLabel = args[1]
				}
			}
			return applyTemplateActionLabel(in)
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}

// applyTemplateActionLabel is the shared write path for the
// set-action-label verb. Honours --dry-run.
func applyTemplateActionLabel(in inputs.SettingsTemplateSetActionLabelInput) error {
	if err := requireLocalForSettings("template set-action-label"); err != nil {
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
	t, err := c.SetPromptTemplateActionLabel(context.Background(), in, opts.dryRun)
	if err != nil {
		return wrapTemplateLookup(slug, err)
	}
	if opts.dryRun {
		return emitDryRun(templateViewForRow(t))
	}
	return emit(templateViewForRow(t))
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
	// BACI-76: changing a dispatchable template's body leaves the
	// generated .claude/agents/bacio-<slug>-worker.md file stale until
	// `bacio install-agent` re-runs. Surface that as stderr guidance
	// (not part of the structured success body — same split as the
	// install-agent activation banner).
	printTemplateStaleAgentHint(os.Stderr, slug)
	return emit(templateViewForRow(t))
}

// printTemplateStaleAgentHint reminds the user, on stderr, that a
// template-body change does not reach the dispatched workers until the
// generated agent file is regenerated. No-op for the reserved
// _dispatch_preamble row (it has no agent file — it stays in the
// dispatch payload).
func printTemplateStaleAgentHint(w io.Writer, slug string) {
	if slug == model.BuiltinTemplatePreamble {
		return
	}
	fmt.Fprintf(w, "note: the agent file for %q is now stale — run `bacio install-agent` to apply this change to dispatched workers.\n", slug)
}

func settingsTemplateAddCmd() *cobra.Command {
	var (
		rawInput    string
		actionLabel string
	)
	cmd := &cobra.Command{
		Use:   "add [SLUG] [NAME]",
		Short: "Create a new dispatch prompt template",
		Long: `Create a new dispatch prompt template. The richest path is --json,
which supports body, state-gate, action_label and concurrency_limit as
well as slug + name; the positional form is a quick "scaffold with
empty body, no state-gate" shortcut you can then edit via the desktop
/ TUI / `+"`set`/`states`"+`. ` + "`--action-label`" + ` is the imperative
override (BACI-67) rendered on the dispatch action menus; when empty,
the UI derives one from NAME via the gerund→imperative rule.

Examples:

  bacio settings template add --json '{"slug":"spike","name":"Spike","body":"Spike on {{issue_id}}.","states":["todo"],"action_label":"Spike"}'
  bacio settings template add spike Spike --action-label "Investigate"
  bacio settings template add spike Spike     # body empty, derives label from name`,
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
				if actionLabel != "" {
					return fmt.Errorf("--action-label and --json are mutually exclusive (set action_label inside the JSON payload)")
				}
				parsed, _, err := inputio.DecodeStrict[inputs.SettingsTemplateAddInput](raw)
				if err != nil {
					return err
				}
				in = *parsed
			} else {
				if len(args) != 2 {
					return fmt.Errorf("requires <SLUG> <NAME> positionals or --json")
				}
				in = inputs.SettingsTemplateAddInput{Slug: args[0], Name: args[1], ActionLabel: actionLabel}
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
	cmd.Flags().StringVar(&actionLabel, "action-label", "", "imperative override rendered on the dispatch action menus (BACI-67); empty = derive from NAME")
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
