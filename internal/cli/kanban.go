package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mrgeoffrich/bacio/internal/agentmode"
	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/inputio"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// Kanban lanes — `bacio kanban column …` plus `bacio kanban move`.
//
// Lanes are addressed by NAME on this surface (names are unique per
// repo) and by uuid underneath; the translation happens here off ONE
// ListKanbanColumns round trip per command.
//
// "" is a MEANINGFUL column value: OFF THE BOARD. `bacio kanban move`
// therefore offers `--off-board` as an unambiguous spelling, and its
// --json twin requires the `column` key to be PRESENT so a typo can't
// silently sweep a card off the board.

const kanbanLongHelp = `Manage the repo's Kanban board — the human lanes.

The Kanban is a SEPARATE AXIS from an issue's state and from the Agentic
Pipeline. A card is on the board if and only if it sits in a lane:

  • In a workspace, new issues land on the leftmost lane automatically.
  • In a git repo, new issues start OFF the board; put one on with
    ` + "`bacio kanban move <KEY> --column <NAME>`" + `, and take it off again
    with ` + "`--off-board`" + `.

Moving a card between lanes never changes its state, and
` + "`bacio issue state`" + ` never changes its lane. Lanes are per-repo and
fully editable — rename them, reorder them, delete them. Deleting a lane
takes its cards off the board; it never deletes an issue.`

func newKanbanCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "kanban",
		Short: "Manage the Kanban board: lanes, and which lane a card sits in",
		Long:  kanbanLongHelp,
	}
	cmd.AddCommand(newKanbanColumnCmd(), kanbanMoveCmd())
	return cmd
}

func newKanbanColumnCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "column",
		Aliases: []string{"columns", "lane", "lanes"},
		Short:   "Manage the board's lanes",
	}
	cmd.AddCommand(
		kanbanColumnAddCmd(), kanbanColumnRenameCmd(), kanbanColumnMvCmd(),
		kanbanColumnRmCmd(), kanbanColumnListCmd(),
	)
	return cmd
}

// ---------------------------------------------------------------------
// Output shapes
// ---------------------------------------------------------------------

// kanbanColumnRow is what every lane mutation emits — the persisted row,
// minus the internal int64 id and repo_id nobody outside the store can
// use. Lean by construction.
type kanbanColumnRow struct {
	UUID     string `json:"uuid"`
	Name     string `json:"name"`
	Position int    `json:"position"`
}

// kanbanLaneRow is the list shape: a lane plus how many cards sit in it.
// The count is the one thing a board listing is useless without, and it
// is a scalar, not a body — leanness rule #3 is about not shipping bulk
// text on bulk reads, and this stays well inside that.
type kanbanLaneRow struct {
	kanbanColumnRow
	Cards int `json:"cards"`
}

func newKanbanColumnRow(col *model.KanbanColumn) *kanbanColumnRow {
	return &kanbanColumnRow{UUID: col.UUID, Name: col.Name, Position: col.Position}
}

// ---------------------------------------------------------------------
// Lane resolution
// ---------------------------------------------------------------------

// kanbanContext bundles the open client, the resolved repo and the
// repo's lanes — the three things every verb here needs.
type kanbanContext struct {
	client  client.Client
	repo    *model.Repo
	columns []*model.KanbanColumn
}

func openKanbanContext(repo *model.Repo, c client.Client) (*kanbanContext, error) {
	cols, err := c.ListKanbanColumns(context.Background(), repo)
	if err != nil {
		return nil, err
	}
	return &kanbanContext{client: c, repo: repo, columns: cols}, nil
}

// openKanbanContextForCWD is the common entry: resolve the repo the
// normal way (cwd, or the --repo selector) and fetch its lanes.
func openKanbanContextForCWD() (*kanbanContext, func(), error) {
	c, err := openClient()
	if err != nil {
		return nil, func() {}, err
	}
	closer := func() { _ = c.Close() }
	repo, err := resolveRepoC(c)
	if err != nil {
		closer()
		return nil, func() {}, err
	}
	kc, err := openKanbanContext(repo, c)
	if err != nil {
		closer()
		return nil, func() {}, err
	}
	return kc, closer, nil
}

// find resolves a lane by name. Exact match wins; failing that, a UNIQUE
// case-insensitive match is accepted so `--column doing` reaches the
// stock `Doing` lane. An ambiguous fold (two lanes differing only in
// case) is an error rather than a coin toss.
//
// This is lookup convenience, not normalisation: what gets STORED is
// always exactly what the user typed on a create/rename, per the
// no-silent-normalisation rule.
func (kc *kanbanContext) find(name string) (*model.KanbanColumn, error) {
	want := strings.TrimSpace(name)
	if want == "" {
		return nil, fmt.Errorf("column name is required")
	}
	for _, col := range kc.columns {
		if col.Name == want {
			return col, nil
		}
	}
	var folded []*model.KanbanColumn
	for _, col := range kc.columns {
		if strings.EqualFold(col.Name, want) {
			folded = append(folded, col)
		}
	}
	switch len(folded) {
	case 1:
		return folded[0], nil
	case 0:
		return nil, fmt.Errorf("%w: no Kanban lane %q in %s (have: %s)",
			store.ErrNotFound, want, kc.repo.Prefix, kc.laneNames())
	default:
		return nil, fmt.Errorf("Kanban lane %q is ambiguous in %s (have: %s); type the name exactly",
			want, kc.repo.Prefix, kc.laneNames())
	}
}

func (kc *kanbanContext) laneNames() string {
	if len(kc.columns) == 0 {
		return "(no lanes yet — add one with `bacio kanban column add <NAME>`)"
	}
	names := make([]string, 0, len(kc.columns))
	for _, col := range kc.columns {
		names = append(names, col.Name)
	}
	return strings.Join(names, ", ")
}

// ---------------------------------------------------------------------
// Lane verbs
// ---------------------------------------------------------------------

func kanbanColumnAddCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "add [NAME]",
		Short: "Add a lane to the right-hand end of the board",
		Args:  cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := parseJSONInput(cmd, args, rawInput)
			if err != nil {
				return err
			}
			if raw != nil {
				in, _, err := inputio.DecodeStrict[inputs.KanbanColumnAddInput](raw)
				if err != nil {
					return err
				}
				if strings.TrimSpace(in.Name) == "" {
					return fmt.Errorf("name is required")
				}
				return addKanbanColumn(in.Name)
			}
			if len(args) != 1 {
				return fmt.Errorf("requires <NAME> positional or --json")
			}
			return addKanbanColumn(args[0])
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}

func addKanbanColumn(name string) error {
	kc, closer, err := openKanbanContextForCWD()
	if err != nil {
		return err
	}
	defer closer()
	// store.ValidateKanbanColumnName runs at the client/store boundary —
	// including on the dry-run path — so the CLI does not duplicate it.
	col, err := kc.client.CreateKanbanColumn(context.Background(), kc.repo, name, opts.dryRun)
	if err != nil {
		return err
	}
	row := newKanbanColumnRow(col)
	if opts.dryRun {
		return emitDryRun(row)
	}
	if opts.output == outputJSON {
		return emit(row)
	}
	return ok("added lane %s at position %d in %s", row.Name, row.Position, kc.repo.Prefix)
}

func kanbanColumnRenameCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "rename [NAME] [NEW-NAME]",
		Short: "Rename a lane (cards keep their lane and their order)",
		Args:  cobra.RangeArgs(0, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := parseJSONInput(cmd, args, rawInput)
			if err != nil {
				return err
			}
			if raw != nil {
				in, _, err := inputio.DecodeStrict[inputs.KanbanColumnRenameInput](raw)
				if err != nil {
					return err
				}
				if strings.TrimSpace(in.Column) == "" || strings.TrimSpace(in.Name) == "" {
					return fmt.Errorf("column and name are required")
				}
				return renameKanbanColumn(in.Column, in.Name)
			}
			if len(args) != 2 {
				return fmt.Errorf("requires <NAME> <NEW-NAME> positionals or --json")
			}
			return renameKanbanColumn(args[0], args[1])
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}

func renameKanbanColumn(current, newName string) error {
	kc, closer, err := openKanbanContextForCWD()
	if err != nil {
		return err
	}
	defer closer()
	col, err := kc.find(current)
	if err != nil {
		return err
	}
	oldName := col.Name
	updated, err := kc.client.RenameKanbanColumn(context.Background(), kc.repo, col.UUID, newName, opts.dryRun)
	if err != nil {
		return err
	}
	row := newKanbanColumnRow(updated)
	if opts.dryRun {
		return emitDryRun(row)
	}
	if opts.output == outputJSON {
		return emit(row)
	}
	return ok("renamed lane %s → %s", oldName, row.Name)
}

func kanbanColumnMvCmd() *cobra.Command {
	var (
		position int
		rawInput string
	)
	cmd := &cobra.Command{
		Use:   "mv [NAME]",
		Short: "Reorder a lane left/right on the board (--position is 0-based)",
		Long: `Move a lane to a new slot on the board.

--position is 0-BASED and dense: 0 is the leftmost lane, and the other
lanes re-densify around the moved one so the board is always a gapless
0..n-1. Out-of-range values are clamped.

(Note: ` + "`bacio issue reorder`" + `, which orders cards within a Pipeline
band, is 1-based. These are different surfaces.)`,
		Args: cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := parseJSONInput(cmd, args, rawInput, "position")
			if err != nil {
				return err
			}
			if raw != nil {
				in, present, err := inputio.DecodeStrict[inputs.KanbanColumnMvInput](raw)
				if err != nil {
					return err
				}
				if strings.TrimSpace(in.Column) == "" {
					return fmt.Errorf("column is required")
				}
				if _, ok := present["position"]; !ok {
					return fmt.Errorf("position is required (0-based; 0 is the leftmost lane)")
				}
				return moveKanbanColumn(in.Column, in.Position)
			}
			if len(args) != 1 {
				return fmt.Errorf("requires <NAME> positional or --json")
			}
			if !cmd.Flags().Changed("position") {
				return fmt.Errorf("--position is required (0-based; 0 is the leftmost lane)")
			}
			return moveKanbanColumn(args[0], position)
		},
	}
	cmd.Flags().IntVar(&position, "position", 0, "0-based slot on the board; 0 is the leftmost lane")
	addInputFlag(cmd, &rawInput)
	return cmd
}

func moveKanbanColumn(name string, position int) error {
	if position < 0 {
		return fmt.Errorf("position must be >= 0, got %d", position)
	}
	kc, closer, err := openKanbanContextForCWD()
	if err != nil {
		return err
	}
	defer closer()
	col, err := kc.find(name)
	if err != nil {
		return err
	}
	from := col.Position
	updated, err := kc.client.ReorderKanbanColumn(context.Background(), kc.repo, col.UUID, position, opts.dryRun)
	if err != nil {
		return err
	}
	row := newKanbanColumnRow(updated)
	if opts.dryRun {
		return emitDryRun(row)
	}
	if opts.output == outputJSON {
		return emit(row)
	}
	// Only the moved lane comes back — the siblings re-densify
	// underneath, so a caller rendering board order re-reads the list.
	return ok("moved lane %s from position %d to %d (run `bacio kanban column list` for the new board order)",
		row.Name, from, row.Position)
}

func kanbanColumnRmCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "rm [NAME]",
		Short: "Delete a lane (its cards come off the board; no issue is deleted)",
		Long: `Delete a lane.

Every card in it comes OFF the board — kanban_column_id goes back to
NULL. The issues themselves are untouched: same state, same feature,
same everything, just no longer on the board. Put them back with
` + "`bacio kanban move <KEY> --column <NAME>`" + `.

Rehearse with --dry-run to see how many cards would come off.`,
		Args: cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := agentmode.DenyIfEnabled("kanban column rm"); err != nil {
				return err
			}
			raw, err := parseJSONInput(cmd, args, rawInput)
			if err != nil {
				return err
			}
			if raw != nil {
				in, _, err := inputio.DecodeStrict[inputs.KanbanColumnRmInput](raw)
				if err != nil {
					return err
				}
				if strings.TrimSpace(in.Column) == "" {
					return fmt.Errorf("column is required")
				}
				return removeKanbanColumn(in.Column)
			}
			if len(args) != 1 {
				return fmt.Errorf("requires <NAME> positional or --json")
			}
			return removeKanbanColumn(args[0])
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}

func removeKanbanColumn(name string) error {
	kc, closer, err := openKanbanContextForCWD()
	if err != nil {
		return err
	}
	defer closer()
	col, err := kc.find(name)
	if err != nil {
		return err
	}
	deleted, preview, err := kc.client.DeleteKanbanColumn(context.Background(), kc.repo, col.UUID, opts.dryRun)
	if err != nil {
		return err
	}
	if opts.dryRun {
		return emitDryRun(preview)
	}
	if opts.output == outputJSON {
		return emit(newKanbanColumnRow(deleted))
	}
	return ok("deleted lane %s", deleted.Name)
}

func kanbanColumnListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the board's lanes in left-to-right order, with card counts",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			kc, closer, err := openKanbanContextForCWD()
			if err != nil {
				return err
			}
			defer closer()
			counts, err := kc.cardCounts()
			if err != nil {
				return err
			}
			rows := make([]*kanbanLaneRow, 0, len(kc.columns))
			for _, col := range kc.columns {
				rows = append(rows, &kanbanLaneRow{
					kanbanColumnRow: *newKanbanColumnRow(col),
					Cards:           counts[col.ID],
				})
			}
			return emit(rows)
		},
	}
}

// cardCounts tallies on-board cards per lane. There is no per-lane
// filter on the shared issue list query (deliberately — one shape), so
// this is one list of the repo's on-board cards tallied client-side.
// Descriptions are stripped: the count is all we want.
func (kc *kanbanContext) cardCounts() (map[int64]int, error) {
	issues, err := kc.client.ListIssues(context.Background(), client.IssueFilter{Repo: kc.repo})
	if err != nil {
		return nil, err
	}
	counts := make(map[int64]int, len(kc.columns))
	for _, iss := range issues {
		if iss.KanbanColumnID != nil {
			counts[*iss.KanbanColumnID]++
		}
	}
	return counts, nil
}

// ---------------------------------------------------------------------
// `bacio kanban move` — place one card
// ---------------------------------------------------------------------

func kanbanMoveCmd() *cobra.Command {
	var (
		column   string
		offBoard bool
		position int
		rawInput string
	)
	cmd := &cobra.Command{
		Use:   "move [KEY]",
		Short: "Put a card in a lane (or take it off the board with --off-board)",
		Long: `Place one card on the Kanban board.

The destination is required and may be "off the board", because that is
a real destination rather than a missing one:

  bacio kanban move MINI-42 --column Doing
  bacio kanban move MINI-42 --column Doing --position 0   # top of the lane
  bacio kanban move MINI-42 --off-board                   # take it off

--position is 0-based top-to-bottom within the lane; the lane
re-densifies to 0..n-1 around the moved card. Omit it to append to the
bottom.

This changes only the card's lane. Its state, feature, assignee and
Pipeline chain are untouched — the Kanban is a separate axis.`,
		Args: cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := parseJSONInput(cmd, args, rawInput, "column", "off-board", "position")
			if err != nil {
				return err
			}
			if raw != nil {
				in, present, err := inputio.DecodeStrict[inputs.KanbanMoveInput](raw)
				if err != nil {
					return err
				}
				if strings.TrimSpace(in.IssueKey) == "" {
					return fmt.Errorf("issue_key is required")
				}
				if !isIssueKey(in.IssueKey) {
					return fmt.Errorf("issue_key %q must be canonical (e.g. \"MINI-42\")", in.IssueKey)
				}
				// Presence, not emptiness: "" means OFF the board, so an
				// omitted key must not silently sweep the card off it.
				if _, ok := present["column"]; !ok {
					return fmt.Errorf(`column is required; pass "" to take the card off the board`)
				}
				return moveIssueToKanbanColumn(in.IssueKey, in.Column, in.Position)
			}
			if len(args) != 1 {
				return fmt.Errorf("requires <KEY> positional or --json")
			}
			dest, err := resolveDestinationFlag(cmd, "column", column, offBoard, "--column <NAME>", "--off-board")
			if err != nil {
				return err
			}
			var pos *int
			if cmd.Flags().Changed("position") {
				pos = &position
			}
			return moveIssueToKanbanColumn(args[0], dest, pos)
		},
	}
	cmd.Flags().StringVar(&column, "column", "", "destination lane name (pass an empty string, or --off-board, to take the card off the board)")
	cmd.Flags().BoolVar(&offBoard, "off-board", false, "take the card off the Kanban board (the explicit spelling of --column \"\")")
	cmd.Flags().IntVar(&position, "position", 0, "0-based slot in the destination lane (default: append to the bottom)")
	addInputFlag(cmd, &rawInput)
	return cmd
}

func moveIssueToKanbanColumn(key, columnName string, position *int) error {
	c, err := openClient()
	if err != nil {
		return err
	}
	defer c.Close()
	// The issue key carries its own prefix, so a card can be moved from
	// anywhere without --repo. Falls back to the cwd/--repo repo for the
	// bare-number shortcut.
	repo, err := repoForIssueKey(c, key)
	if err != nil {
		return err
	}
	kc, err := openKanbanContext(repo, c)
	if err != nil {
		return err
	}
	columnUUID := ""
	target := "off the board"
	if strings.TrimSpace(columnName) != "" {
		col, ferr := kc.find(columnName)
		if ferr != nil {
			return ferr
		}
		columnUUID = col.UUID
		target = "lane " + col.Name
	}
	updated, err := c.MoveIssueToKanbanColumn(context.Background(), repo, client.IssueKanbanMoveInput{
		IssueKey:   key,
		ColumnUUID: columnUUID,
		Position:   position,
	}, opts.dryRun)
	if err != nil {
		return err
	}
	if opts.dryRun {
		return emitDryRun(updated)
	}
	if opts.output == outputJSON {
		return emit(updated)
	}
	if columnUUID == "" {
		return ok("took %s off the board", updated.Key)
	}
	return ok("moved %s to %s (position %d)", updated.Key, target, updated.KanbanPosition)
}
