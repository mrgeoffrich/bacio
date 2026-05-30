package cli

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/inputio"
)

// issueReorderCmd — `bacio issue reorder <KEY> <position>`. Moves a card
// within its Backlog / Shipping ordering band (position 1 = top).
func issueReorderCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "reorder [KEY] [position]",
		Short: "Move a card within its Backlog/Shipping order (Pipeline)",
		Args:  cobra.RangeArgs(0, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := parseJSONInput(cmd, args, rawInput)
			if err != nil {
				return err
			}
			if raw != nil {
				in, _, err := inputio.DecodeStrict[inputs.IssueReorderInput](raw)
				if err != nil {
					return err
				}
				if in.Key == "" || in.Position < 1 {
					return fmt.Errorf("key and position (>= 1) are required")
				}
				return runIssueReorder(in.Key, in.Position)
			}
			if len(args) != 2 {
				return fmt.Errorf("requires <KEY> <position> positionals or --json")
			}
			pos, err := strconv.Atoi(args[1])
			if err != nil {
				return fmt.Errorf("position must be an integer: %w", err)
			}
			return runIssueReorder(args[0], pos)
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}

func runIssueReorder(key string, position int) error {
	c, err := openClient()
	if err != nil {
		return err
	}
	defer c.Close()
	repo, err := repoForIssueKey(c, key)
	if err != nil {
		return err
	}
	updated, err := c.ReorderIssue(context.Background(), repo, key, position, opts.dryRun)
	if err != nil {
		return err
	}
	if opts.dryRun {
		return emitDryRun(updated)
	}
	return emit(updated)
}

// issueProcessCmd — `bacio issue process …`. Parent for the process-chain
// verbs; `set` assigns a preset.
func issueProcessCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "process",
		Short: "Manage a card's Pipeline process chain",
	}
	cmd.AddCommand(issueProcessSetCmd())
	cmd.AddCommand(issueProcessEditCmd())
	return cmd
}

func issueProcessSetCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "set [KEY] [preset]",
		Short: "Assign a process (job chain) to an in_pipeline card",
		Long: "Assign a process (job chain) to an in_pipeline card. The flag/positional path takes a preset slug; the --json path additionally accepts an explicit ordered stage list via \"stages\" (mutually exclusive with \"process\").",
		Args: cobra.RangeArgs(0, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := parseJSONInput(cmd, args, rawInput)
			if err != nil {
				return err
			}
			var key, process string
			var stages []string
			if raw != nil {
				in, _, err := inputio.DecodeStrict[inputs.IssueProcessInput](raw)
				if err != nil {
					return err
				}
				key, process, stages = in.Key, in.Process, in.Stages
				if key == "" {
					return fmt.Errorf("key is required")
				}
				if (process == "") == (len(stages) == 0) {
					return fmt.Errorf("exactly one of process or stages is required")
				}
			} else {
				if len(args) != 2 {
					return fmt.Errorf("requires <KEY> <preset> positionals or --json")
				}
				key, process = args[0], args[1]
			}
			c, err := openClient()
			if err != nil {
				return err
			}
			defer c.Close()
			repo, err := repoForIssueKey(c, key)
			if err != nil {
				return err
			}
			jobs, err := c.SetIssueProcess(context.Background(), repo, key, process, stages, opts.dryRun)
			if err != nil {
				return err
			}
			if opts.dryRun {
				return emitDryRun(jobs)
			}
			return emit(jobs)
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}

// issueProcessEditCmd — `bacio issue process edit <KEY> <mode…>`. Edits
// the pending tail of an in_pipeline card's chain (BACI-294): the
// completed / running / cancelled jobs stay as a locked prefix; the
// positional modes (or --json "stages") become the new re-ordered pending
// tail, re-sequenced after the prefix. Ship may only be the final stage;
// duplicate modes are allowed (re-loops).
func issueProcessEditCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "edit [KEY] [mode...]",
		Short: "Edit the pending tail of an in_pipeline card's job chain",
		Long:  "Edit the pending tail of an in_pipeline card's job chain (Pipeline). The completed/running/cancelled jobs stay as a locked prefix; the positional modes (or --json \"stages\") replace the pending jobs, re-sequenced after the prefix. Ship may only be the final stage; duplicate modes are allowed.",
		Args:  cobra.MinimumNArgs(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := parseJSONInput(cmd, args, rawInput)
			if err != nil {
				return err
			}
			var key string
			var stages []string
			if raw != nil {
				in, _, err := inputio.DecodeStrict[inputs.IssueProcessEditInput](raw)
				if err != nil {
					return err
				}
				key, stages = in.Key, in.Stages
				if key == "" {
					return fmt.Errorf("key is required")
				}
				if len(stages) == 0 {
					return fmt.Errorf("stages must list at least one job mode")
				}
			} else {
				if len(args) < 2 {
					return fmt.Errorf("requires <KEY> <mode...> positionals or --json")
				}
				key, stages = args[0], args[1:]
			}
			c, err := openClient()
			if err != nil {
				return err
			}
			defer c.Close()
			repo, err := repoForIssueKey(c, key)
			if err != nil {
				return err
			}
			jobs, err := c.EditIssueProcessTail(context.Background(), repo, key, stages, opts.dryRun)
			if err != nil {
				return err
			}
			if opts.dryRun {
				return emitDryRun(jobs)
			}
			return emit(jobs)
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}

// issueShipCmd — `bacio issue ship <KEY>`. Hands an in_pipeline card off
// to Shipping (to_be_shipped). Dispatches no agent — the ship agent
// fires from the Shipping column / auto-ship.
func issueShipCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "ship [KEY]",
		Short: "Hand off an in_pipeline card to Shipping (Pipeline)",
		Args:  cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := parseJSONInput(cmd, args, rawInput)
			if err != nil {
				return err
			}
			var key string
			if raw != nil {
				in, _, err := inputio.DecodeStrict[inputs.IssueShipInput](raw)
				if err != nil {
					return err
				}
				if in.Key == "" {
					return fmt.Errorf("key is required")
				}
				key = in.Key
			} else {
				if len(args) != 1 {
					return fmt.Errorf("requires <KEY> positional or --json")
				}
				key = args[0]
			}
			c, err := openClient()
			if err != nil {
				return err
			}
			defer c.Close()
			repo, err := repoForIssueKey(c, key)
			if err != nil {
				return err
			}
			updated, err := c.ShipIssue(context.Background(), repo, key, opts.dryRun)
			if err != nil {
				return err
			}
			if opts.dryRun {
				return emitDryRun(updated)
			}
			return emit(updated)
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}

// issueAutoShipCmd — `bacio issue auto-ship <on|off>`. Toggles the
// per-repo Shipping-column auto-ship setting.
func issueAutoShipCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "auto-ship [on|off]",
		Short: "Toggle Shipping-column auto-ship for the repo (Pipeline)",
		Args:  cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := parseJSONInput(cmd, args, rawInput)
			if err != nil {
				return err
			}
			var enabled bool
			if raw != nil {
				in, _, err := inputio.DecodeStrict[inputs.RepoAutoShipInput](raw)
				if err != nil {
					return err
				}
				enabled = in.Enabled
			} else {
				if len(args) != 1 {
					return fmt.Errorf("requires on|off positional or --json")
				}
				switch strings.ToLower(args[0]) {
				case "on", "true":
					enabled = true
				case "off", "false":
					enabled = false
				default:
					return fmt.Errorf("expected on|off, got %q", args[0])
				}
			}
			c, err := openClient()
			if err != nil {
				return err
			}
			defer c.Close()
			repo, err := resolveRepoC(c)
			if err != nil {
				return err
			}
			val, err := c.SetRepoAutoShip(context.Background(), repo, enabled, opts.dryRun)
			if err != nil {
				return err
			}
			out := map[string]any{"auto_ship": val}
			if opts.dryRun {
				return emitDryRun(out)
			}
			return emit(out)
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}
