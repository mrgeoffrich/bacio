package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	root "github.com/mrgeoffrich/bacio"
	"github.com/mrgeoffrich/bacio/internal/git"
)

func newInstallSkillCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install-skill",
		Short: "Install the bacio Claude Code skill into the current repo",
		Long: `Drop the bundled SKILL.md into <repo-root>/.claude/skills/bacio/, creating
the directory if needed. Overwrites any existing copy with the version
embedded in this build of bacio so re-running picks up doc updates.

See also: 'bacio install-agent' to set the repo up for agent-driven
dispatch (subagent files + hooks + channel).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if inRemoteMode() {
				return fmt.Errorf("bacio install-skill: not supported in remote mode (writes the skill file to the local repo); run this verb against the local DB instead")
			}
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			info, err := git.Detect(cwd)
			if err != nil {
				return err
			}
			dir := filepath.Join(info.Root, ".claude", "skills", "bacio")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return fmt.Errorf("create skill dir: %w", err)
			}
			path := filepath.Join(dir, "SKILL.md")
			if err := os.WriteFile(path, root.SkillMarkdown, 0o644); err != nil {
				return fmt.Errorf("write skill: %w", err)
			}
			return ok("installed bacio skill (%d bytes) at %s", len(root.SkillMarkdown), path)
		},
	}
}
