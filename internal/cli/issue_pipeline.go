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
	return cmd
}

func issueProcessSetCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "set [KEY] [preset]",
		Short: "Assign a preset process (job chain) to an in_pipeline card",
		Args:  cobra.RangeArgs(0, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := parseJSONInput(cmd, args, rawInput)
			if err != nil {
				return err
			}
			var key, process string
			if raw != nil {
				in, _, err := inputio.DecodeStrict[inputs.IssueProcessInput](raw)
				if err != nil {
					return err
				}
				key, process = in.Key, in.Process
				if key == "" || process == "" {
					return fmt.Errorf("key and process are required")
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
			jobs, err := c.SetIssueProcess(context.Background(), repo, key, process, opts.dryRun)
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
