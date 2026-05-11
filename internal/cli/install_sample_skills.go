package cli

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	root "github.com/mrgeoffrich/bacio"
	"github.com/mrgeoffrich/bacio/internal/git"
)

const sampleSkillsEmbedRoot = "examples/skills"

func newInstallSampleSkillsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install-sample-skills [name...]",
		Short: "Install bundled workflow sample skills into the current repo",
		Long: `Drop one or more flow-level sample skills into
<repo-root>/.claude/skills/<name>/SKILL.md.

Sample skills complement the canonical bacio skill (installed by
'bacio install-skill') by capturing common end-to-end flows the agent
should follow — file-issue, triage, stand-up, plan-feature.

With no arguments, every bundled sample is installed. Pass one or
more names (e.g. 'bacio install-sample-skills triage stand-up') to
install only those. Existing skill files at the destination are
overwritten so re-running picks up updates from this build of bacio.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if inRemoteMode() {
				return fmt.Errorf("bacio install-sample-skills: not supported in remote mode (writes skill files to the local repo); run this verb against the local DB instead")
			}
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			info, err := git.Detect(cwd)
			if err != nil {
				return err
			}

			available, err := bundledSampleSkills()
			if err != nil {
				return fmt.Errorf("enumerate bundled sample skills: %w", err)
			}
			if len(available) == 0 {
				return fmt.Errorf("no sample skills bundled in this build of bacio")
			}

			wanted, err := selectSampleSkills(available, args)
			if err != nil {
				return err
			}

			base := filepath.Join(info.Root, ".claude", "skills")
			destinations := make([]string, 0, len(wanted))
			for _, name := range wanted {
				content, err := root.SampleSkillsFS.ReadFile(sampleSkillsEmbedRoot + "/" + name + "/SKILL.md")
				if err != nil {
					return fmt.Errorf("read embedded skill %q: %w", name, err)
				}
				dir := filepath.Join(base, name)
				if err := os.MkdirAll(dir, 0o755); err != nil {
					return fmt.Errorf("create skill dir: %w", err)
				}
				dest := filepath.Join(dir, "SKILL.md")
				if err := os.WriteFile(dest, content, 0o644); err != nil {
					return fmt.Errorf("write skill: %w", err)
				}
				destinations = append(destinations, dest)
			}

			return ok("installed %d sample skill(s):\n  %s",
				len(destinations), strings.Join(destinations, "\n  "))
		},
	}
}

// bundledSampleSkills enumerates every <name>/SKILL.md directory under
// the embedded examples/skills tree and returns the names sorted
// alphabetically. Used both to enumerate "all" and to validate explicit
// positional arguments.
func bundledSampleSkills() ([]string, error) {
	entries, err := fs.ReadDir(root.SampleSkillsFS, sampleSkillsEmbedRoot)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := fs.Stat(root.SampleSkillsFS, sampleSkillsEmbedRoot+"/"+e.Name()+"/SKILL.md"); err != nil {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names, nil
}

// selectSampleSkills resolves the user-supplied argument list against
// the bundled set. With no args, returns every available skill in
// alphabetical order. With args, validates each name and dedups.
func selectSampleSkills(available, args []string) ([]string, error) {
	if len(args) == 0 {
		return available, nil
	}
	set := make(map[string]bool, len(available))
	for _, n := range available {
		set[n] = true
	}
	out := make([]string, 0, len(args))
	seen := make(map[string]bool, len(args))
	for _, a := range args {
		if !set[a] {
			return nil, fmt.Errorf("unknown sample skill %q (available: %s)", a, strings.Join(available, ", "))
		}
		if seen[a] {
			continue
		}
		seen[a] = true
		out = append(out, a)
	}
	return out, nil
}
