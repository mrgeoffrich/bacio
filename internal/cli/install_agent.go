package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mrgeoffrich/bacio/internal/git"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// agentFilesDir is the per-repo directory `install-agent` writes the
// generated subagent files into. Claude Code auto-discovers custom
// subagents from `.claude/agents/`.
const agentFilesDir = ".claude/agents"

// newInstallAgentCmd builds the consolidated `bacio install-agent`
// command (BACI-79). It performs, in one invocation with a single
// plan/confirm, the three steps that used to be separate verbs:
//
//   - render the per-mode dispatch subagent files into .claude/agents/
//     (the old `install-agents`),
//   - merge bacio's Claude Code hooks into .claude/settings.json
//     (the old `install-hooks`),
//   - register the bacio channel MCP server in .mcp.json
//     (the old `install-channel`).
//
// One combined plan is printed, one confirmation prompt is asked, and
// --yes (-y) accepts all three steps non-interactively.
func newInstallAgentCmd() *cobra.Command {
	var assumeYes bool
	cmd := &cobra.Command{
		Use:   "install-agent",
		Short: "Set the current repo up to run as a bacio agent (hooks + channel + subagent files)",
		Long: `Set <repo-root> up for agent-driven bacio work in one command.

install-agent performs three steps in a single invocation:

  1. Subagent files  — render .claude/agents/bacio-<mode>-worker.md for
                       every dispatchable prompt template (BACI-76).
  2. Hooks           — merge bacio's agent-supervision hooks into
                       .claude/settings.json (SessionStart,
                       UserPromptSubmit, Stop, SessionEnd, PostToolUse).
  3. Channel         — register the "bacio" MCP server in .mcp.json so
                       Claude Code can spawn 'bacio channel' to push
                       queued dispatches into a running session live.

Every step is non-destructive and idempotent: existing non-bacio hooks
and other mcpServers entries are preserved, and bacio's own entries are
refreshed in place. The subagent files are overwritten — re-run
install-agent after editing a template body (via 'bacio settings
template set' or the Settings panel) to apply the change.

install-agent prints one combined plan covering all three steps and
asks for confirmation before writing. Pass --yes (-y) to skip the
prompt and accept automatically -- needed when running
non-interactively.

Activation: the hooks and channel are inert unless BACIO_AGENT_MODE=1
is set in the environment of the Claude session that loads them. See
the post-install output for the recommended launch incantation.

Note: top-level keys in settings.json / .mcp.json may be reordered,
since the files are round-tripped through a JSON decode. Behaviour is
unchanged.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if inRemoteMode() {
				return fmt.Errorf("bacio install-agent: not supported in remote mode (writes agent files, hooks and the MCP config to the local repo); run this verb against the local DB instead")
			}
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			info, err := git.Detect(cwd)
			if err != nil {
				return err
			}

			settingsPath := filepath.Join(info.Root, ".claude", "settings.json")
			mcpPath := filepath.Join(info.Root, ".mcp.json")
			channelCommand := bacioBinaryPath()

			// --- Plan phase (no writes) ---
			hooksTop, hookChanges, err := planBacioHooks(settingsPath)
			if err != nil {
				return err
			}
			channelTop, channelAction, err := planBacioChannel(mcpPath)
			if err != nil {
				return err
			}
			agentPlans, agentWarnings, err := planAgentFiles(info.Root, args)
			if err != nil {
				return err
			}

			// --- Confirm phase ---
			if !assumeYes {
				printInstallAgentPlan(os.Stderr, settingsPath, hookChanges, mcpPath, channelAction, channelCommand, agentPlans)
				confirmed, err := confirmPrompt("Proceed?")
				if err != nil {
					return err
				}
				if !confirmed {
					fmt.Fprintln(os.Stderr, "aborted — no changes made")
					return nil
				}
			}

			// --- Apply phase ---
			if err := applyBacioHooks(settingsPath, hooksTop); err != nil {
				return err
			}
			if err := applyBacioChannel(mcpPath, channelTop, channelCommand); err != nil {
				return err
			}
			written, err := applyAgentFiles(filepath.Join(info.Root, agentFilesDir), agentPlans)
			if err != nil {
				return err
			}

			// --- Report phase ---
			if err := reportInstallAgent(settingsPath, hookChanges, mcpPath, channelAction, written); err != nil {
				return err
			}
			for _, w := range agentWarnings {
				fmt.Fprintf(os.Stderr, "warning: %s\n", w)
			}
			printInstallAgentBanner(os.Stderr)
			printWorktreeManifestHint(os.Stderr, info.Root)
			printActivationBanner(os.Stderr)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&assumeYes, "yes", "y", false, "skip the confirmation prompt and accept the changes")
	return cmd
}

// printInstallAgentPlan writes the one combined plan covering all three
// steps to stderr, ahead of the single confirmation prompt.
func printInstallAgentPlan(w io.Writer, settingsPath string, hookChanges []hookChange, mcpPath, channelAction, channelCommand string, agentPlans []agentFilePlan) {
	fmt.Fprintln(w, "bacio install-agent will perform three steps:")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "1. Hooks — update %s:\n", settingsPath)
	for _, c := range hookChanges {
		fmt.Fprintf(w, "     %-7s %-17s → bacio hook %s\n", c.Action, c.Event, c.Subcommand)
	}
	fmt.Fprintln(w, "   Existing hooks for other events are left untouched.")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "2. Channel — %s the %q MCP server in %s:\n", channelAction, mcpServerName, mcpPath)
	fmt.Fprintf(w, "     command: %s channel\n", channelCommand)
	fmt.Fprintln(w)
	fmt.Fprintf(w, "3. Subagent files — write %d file(s):\n", len(agentPlans))
	for _, p := range agentPlans {
		fmt.Fprintf(w, "     %s\n", p.dest)
	}
}

// reportInstallAgent emits the combined post-write summary on stdout
// via ok(), so it round-trips to JSON like every other command's
// success output.
func reportInstallAgent(settingsPath string, hookChanges []hookChange, mcpPath, channelAction string, written []string) error {
	var b strings.Builder
	fmt.Fprintln(&b, "set up this repo as a bacio agent:")
	fmt.Fprintf(&b, "  hooks → %s", settingsPath)
	for _, c := range hookChanges {
		fmt.Fprintf(&b, "\n    %-7s %-17s → bacio hook %s", c.Action, c.Event, c.Subcommand)
	}
	channelDone := "added"
	if channelAction == "update" {
		channelDone = "updated"
	}
	fmt.Fprintf(&b, "\n  channel → %s the %q MCP server in %s", channelDone, mcpServerName, mcpPath)
	fmt.Fprintf(&b, "\n  subagent files → %d written:\n    %s", len(written), strings.Join(written, "\n    "))
	return ok("%s", b.String())
}

// printInstallAgentBanner reminds the user that the agent files are a
// generated artefact: a template-body edit goes stale until the next
// install-agent run.
func printInstallAgentBanner(w io.Writer) {
	fmt.Fprintln(w)
	fmt.Fprintln(w, "The subagent files are generated from the prompt_templates rows.")
	fmt.Fprintln(w, "After editing a template body (`bacio settings template set` or the")
	fmt.Fprintln(w, "Settings panel), re-run `bacio install-agent` to apply the change.")
}

// planAgentFiles resolves the dispatchable prompt templates and builds
// the (template -> file) write plan, without touching disk. Shared by
// `bacio install-agent` and `bacio status`'s freshness check so the
// expected file set is computed in exactly one place.
func planAgentFiles(repoRoot string, args []string) (plans []agentFilePlan, warnings []string, err error) {
	c, err := openClient()
	if err != nil {
		return nil, nil, err
	}
	defer c.Close()
	tmpls, err := c.ListPromptTemplates(context.Background())
	if err != nil {
		return nil, nil, err
	}

	wanted, warnings, err := selectAgentTemplates(tmpls, args)
	if err != nil {
		return nil, nil, err
	}
	if len(wanted) == 0 {
		return nil, nil, fmt.Errorf("no dispatchable templates to render — every template either has an empty body or is the reserved %s row", model.BuiltinTemplatePreamble)
	}

	base := filepath.Join(repoRoot, agentFilesDir)
	plans = make([]agentFilePlan, 0, len(wanted))
	for _, t := range wanted {
		content, err := model.RenderAgentFile(t.Slug, t.Name, t.Body)
		if err != nil {
			return nil, nil, err
		}
		dest := filepath.Join(base, model.SubagentTypeForTemplate(t.Slug)+".md")
		plans = append(plans, agentFilePlan{slug: t.Slug, dest: dest, content: content})
	}
	return plans, warnings, nil
}

// applyAgentFiles writes each planned subagent file under base,
// creating the directory if needed. Returns the list of paths written.
func applyAgentFiles(base string, plans []agentFilePlan) ([]string, error) {
	if err := os.MkdirAll(base, 0o755); err != nil {
		return nil, fmt.Errorf("create agents dir: %w", err)
	}
	written := make([]string, 0, len(plans))
	for _, p := range plans {
		if err := os.WriteFile(p.dest, []byte(p.content), 0o644); err != nil {
			return nil, fmt.Errorf("write agent file %s: %w", p.dest, err)
		}
		written = append(written, p.dest)
	}
	return written, nil
}

// agentFilePlan is a single resolved (template -> file) write the
// command will perform once the user confirms.
type agentFilePlan struct {
	slug    string
	dest    string
	content string
}

// selectAgentTemplates resolves the dispatchable templates to render.
// The reserved _dispatch_preamble row and any template with an empty
// body are excluded — they have no brief to become a system prompt. A
// user template whose body still contains a {{...}} placeholder is
// dropped with a warning (it can't render cleanly into a fixed system
// prompt); a built-in in that state is a packaging bug and surfaces as
// a hard error from RenderAgentFile downstream.
//
// With explicit args, every named slug must resolve to a dispatchable
// template — an unknown or non-dispatchable slug is a hard error.
// install-agent passes no args today (it always renders the full set),
// but the arg path is retained so the helper stays self-contained and
// testable.
func selectAgentTemplates(tmpls []*store.PromptTemplate, args []string) (wanted []*store.PromptTemplate, warnings []string, err error) {
	dispatchable := make(map[string]*store.PromptTemplate)
	var available []string
	for _, t := range tmpls {
		if t.Slug == model.BuiltinTemplatePreamble {
			continue
		}
		if strings.TrimSpace(t.Body) == "" {
			continue
		}
		dispatchable[t.Slug] = t
		available = append(available, t.Slug)
	}
	sort.Strings(available)

	var pick []*store.PromptTemplate
	if len(args) == 0 {
		for _, slug := range available {
			pick = append(pick, dispatchable[slug])
		}
	} else {
		seen := make(map[string]bool, len(args))
		for _, a := range args {
			t, ok := dispatchable[a]
			if !ok {
				return nil, nil, fmt.Errorf("unknown or non-dispatchable template %q (dispatchable: %s)", a, strings.Join(available, ", "))
			}
			if seen[a] {
				continue
			}
			seen[a] = true
			pick = append(pick, t)
		}
	}

	for _, t := range pick {
		if !t.IsBuiltin && strings.Contains(t.Body, "{{") {
			warnings = append(warnings, fmt.Sprintf("template %q body still contains a {{...}} placeholder — {{token}} interpolation does not survive into an agent file; rewrite the body to refer to \"the ticket named in your dispatch prompt\". Skipped.", t.Slug))
			continue
		}
		wanted = append(wanted, t)
	}
	return wanted, warnings, nil
}
