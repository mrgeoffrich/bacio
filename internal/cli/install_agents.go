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

// agentFilesDir is the per-repo directory `install-agents` writes the
// generated subagent files into. Claude Code auto-discovers custom
// subagents from `.claude/agents/`.
const agentFilesDir = ".claude/agents"

func newInstallAgentsCmd() *cobra.Command {
	var assumeYes bool
	cmd := &cobra.Command{
		Use:   "install-agents [mode...]",
		Short: "Generate the per-mode dispatch subagent files in the current repo",
		Long: `Render <repo-root>/.claude/agents/bacio-<mode>-worker.md for every
dispatchable prompt template (BACI-76).

Each generated file is a Claude Code custom subagent: its frontmatter
carries a narrowed tool allowlist and the model, and its body is the
dispatch template's brief verbatim — that body is the subagent's
durable system prompt. The bacio channel's dispatch payload then only
has to name which subagent to spawn, instead of pasting the whole
~10K-token brief into every dispatch.

With no arguments, a file is rendered for every dispatchable template
(every prompt_templates row except the reserved _dispatch_preamble).
Pass one or more mode slugs to render only those.

Files are overwritten in place — re-run install-agents after editing a
template body (via 'bacio settings template set' or the Settings panel)
to apply the change.

install-agents prints the planned changes and asks for confirmation
before writing. Pass --yes (-y) to skip the prompt.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if inRemoteMode() {
				return fmt.Errorf("bacio install-agents: not supported in remote mode (writes agent files to the local repo); run this verb against the local DB instead")
			}
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			info, err := git.Detect(cwd)
			if err != nil {
				return err
			}

			c, err := openClient()
			if err != nil {
				return err
			}
			defer c.Close()
			tmpls, err := c.ListPromptTemplates(context.Background())
			if err != nil {
				return err
			}

			wanted, warnings, err := selectAgentTemplates(tmpls, args)
			if err != nil {
				return err
			}
			if len(wanted) == 0 {
				return fmt.Errorf("no dispatchable templates to render — every template either has an empty body or is the reserved %s row", model.BuiltinTemplatePreamble)
			}

			base := filepath.Join(info.Root, agentFilesDir)
			plans := make([]agentFilePlan, 0, len(wanted))
			for _, t := range wanted {
				content, err := model.RenderAgentFile(t.Slug, t.Name, t.Body)
				if err != nil {
					return err
				}
				dest := filepath.Join(base, model.SubagentTypeForTemplate(t.Slug)+".md")
				plans = append(plans, agentFilePlan{slug: t.Slug, dest: dest, content: content})
			}

			if !assumeYes {
				fmt.Fprintf(os.Stderr, "bacio install-agents will write %d subagent file(s):\n", len(plans))
				for _, p := range plans {
					fmt.Fprintf(os.Stderr, "  %s\n", p.dest)
				}
				confirmed, err := confirmPrompt("Proceed?")
				if err != nil {
					return err
				}
				if !confirmed {
					fmt.Fprintln(os.Stderr, "aborted — no changes made")
					return nil
				}
			}

			if err := os.MkdirAll(base, 0o755); err != nil {
				return fmt.Errorf("create agents dir: %w", err)
			}
			written := make([]string, 0, len(plans))
			for _, p := range plans {
				if err := os.WriteFile(p.dest, []byte(p.content), 0o644); err != nil {
					return fmt.Errorf("write agent file %s: %w", p.dest, err)
				}
				written = append(written, p.dest)
			}

			for _, w := range warnings {
				fmt.Fprintf(os.Stderr, "warning: %s\n", w)
			}
			printInstallAgentsBanner(os.Stderr)
			return ok("installed %d dispatch subagent file(s):\n  %s",
				len(written), strings.Join(written, "\n  "))
		},
	}
	cmd.Flags().BoolVarP(&assumeYes, "yes", "y", false, "skip the confirmation prompt and accept the change")
	return cmd
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

// printInstallAgentsBanner reminds the user that the agent files are a
// generated artefact: a template-body edit goes stale until the next
// install-agents run.
func printInstallAgentsBanner(w io.Writer) {
	fmt.Fprintln(w)
	fmt.Fprintln(w, "These files are generated from the prompt_templates rows. After")
	fmt.Fprintln(w, "editing a template body (`bacio settings template set` or the")
	fmt.Fprintln(w, "Settings panel), re-run `bacio install-agents` to apply the change.")
}
