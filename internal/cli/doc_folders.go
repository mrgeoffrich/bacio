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

// Doc folders — `bacio doc folder …` plus `bacio doc mv`.
//
// The whole surface is addressed by slash-separated DISPLAY PATH
// ("Design/API/Auth"), because that is what a human types and what
// `bacio doc folder list` prints. The store and the client are addressed
// by uuid; the translation happens here, off ONE ListDocFolders round
// trip per command via the shared client.FindDocFolderByPath /
// client.DocFolderDisplayPath helpers. There is deliberately no
// "resolve a folder path" endpoint — the whole tree is small and one
// fetch serves resolution, validation and output.
//
// "" is a MEANINGFUL path, not a missing one: it is the tree root. Every
// verb that takes a destination therefore offers `--to-root` as an
// unambiguous spelling alongside the empty string, and its --json twin
// requires the key to be PRESENT (so a typo can't silently re-root a
// subtree).

const docFolderLongHelp = `Organise a repo's documents into a folder tree.

Folders are purely organisational: a document's filename stays flat and
globally unique per repo, so URLs, links and the sync layout are
unaffected by moving pages around. Deleting a folder never deletes a
page — its documents are re-rooted.

Folders are addressed by their slash path, exactly as printed by
` + "`bacio doc folder list`" + `. Path segments are matched exactly and are
case-sensitive. The EMPTY path is the tree root:

  bacio doc folder add Design                        # a root-level folder
  bacio doc folder add API --parent Design           # Design/API
  bacio doc folder mv Design/API --to-root           # promote to the root
  bacio doc mv auth.md --folder Design/API           # file a page
  bacio doc mv auth.md --to-root                     # un-file it`

func newDocFolderCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "folder",
		Short: "Organise documents into a folder tree",
		Long:  docFolderLongHelp,
	}
	cmd.AddCommand(
		docFolderAddCmd(), docFolderRenameCmd(), docFolderMvCmd(),
		docFolderRmCmd(), docFolderListCmd(),
	)
	return cmd
}

// ---------------------------------------------------------------------
// Output shapes
// ---------------------------------------------------------------------

// docFolderRow is the JSON/text shape every folder verb emits. It is the
// model row plus the derived display path — the field the caller
// actually addresses the folder by. Lean by construction: a folder
// carries no heavy body, and the row deliberately omits a document count
// so a listing costs exactly one round trip.
type docFolderRow struct {
	UUID       string `json:"uuid"`
	Name       string `json:"name"`
	Path       string `json:"path"`
	ParentUUID string `json:"parent_uuid,omitempty"`
	Position   int    `json:"position"`
}

// newDocFolderRow builds a row from a folder plus the tree it lives in.
// `path` is passed rather than derived because a dry-run projection is
// NOT in the tree yet (it has no uuid and no row), so the caller
// computes the projected path from the destination it asked for.
func newDocFolderRow(folders []*model.DocFolder, f *model.DocFolder, path string) *docFolderRow {
	row := &docFolderRow{
		UUID:     f.UUID,
		Name:     f.Name,
		Path:     path,
		Position: f.Position,
	}
	if f.ParentID != nil {
		for _, candidate := range folders {
			if candidate.ID == *f.ParentID {
				row.ParentUUID = candidate.UUID
				break
			}
		}
	}
	return row
}

// joinFolderPath appends a leaf name to a parent path. The parent path
// is "" for the tree root, which must not produce a leading separator.
func joinFolderPath(parent, name string) string {
	if parent == "" {
		return name
	}
	return parent + store.FolderPathSeparator + name
}

// docFolderContext is the per-command bundle: an open client, the
// resolved repo, and the repo's whole folder tree. Every verb here needs
// all three, and the tree is what makes path addressing possible without
// a dedicated endpoint.
type docFolderContext struct {
	client  client.Client
	repo    *model.Repo
	folders []*model.DocFolder
}

func openDocFolderContext() (*docFolderContext, func(), error) {
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
	folders, err := c.ListDocFolders(context.Background(), repo)
	if err != nil {
		closer()
		return nil, func() {}, err
	}
	return &docFolderContext{client: c, repo: repo, folders: folders}, closer, nil
}

// find resolves a required folder path. Unlike client.FindDocFolderByPath
// it refuses the root: every verb here that takes a subject path is
// operating ON a folder, and the root is not one.
func (dc *docFolderContext) find(path string) (*model.DocFolder, error) {
	folder, err := client.FindDocFolderByPath(dc.folders, path)
	if err != nil {
		return nil, err
	}
	if folder == nil {
		return nil, fmt.Errorf("the tree root is not a folder; name a folder path (see `bacio doc folder list`)")
	}
	return folder, nil
}

// findParent resolves a DESTINATION path, where "" legitimately means
// the tree root and yields ("", nil) — the uuid the client contract
// spells the root with.
func (dc *docFolderContext) findParent(path string) (uuid string, display string, err error) {
	folder, err := client.FindDocFolderByPath(dc.folders, path)
	if err != nil {
		return "", "", err
	}
	if folder == nil {
		return "", "", nil
	}
	return folder.UUID, client.DocFolderDisplayPath(dc.folders, folder), nil
}

func (dc *docFolderContext) displayPath(f *model.DocFolder) string {
	return client.DocFolderDisplayPath(dc.folders, f)
}

// ---------------------------------------------------------------------
// Verbs
// ---------------------------------------------------------------------

func docFolderAddCmd() *cobra.Command {
	var parent, rawInput string
	cmd := &cobra.Command{
		Use:   "add [NAME]",
		Short: "Create a document folder (at the tree root, or under --parent)",
		Args:  cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := parseJSONInput(cmd, args, rawInput, "parent")
			if err != nil {
				return err
			}
			if raw != nil {
				in, _, err := inputio.DecodeStrict[inputs.DocFolderAddInput](raw)
				if err != nil {
					return err
				}
				if strings.TrimSpace(in.Name) == "" {
					return fmt.Errorf("name is required")
				}
				return addDocFolder(in.Name, in.Parent)
			}
			if len(args) != 1 {
				return fmt.Errorf("requires <NAME> positional or --json")
			}
			return addDocFolder(args[0], parent)
		},
	}
	cmd.Flags().StringVar(&parent, "parent", "", "slash path of the containing folder (default: the tree root)")
	addInputFlag(cmd, &rawInput)
	return cmd
}

func addDocFolder(name, parentPath string) error {
	dc, closer, err := openDocFolderContext()
	if err != nil {
		return err
	}
	defer closer()
	parentUUID, parentDisplay, err := dc.findParent(parentPath)
	if err != nil {
		return err
	}
	// Name validation is store.ValidateFolderName, run at the client /
	// store boundary — not re-implemented here — so the CLI, the REST
	// surface and the desktop app all reject the same strings.
	folder, err := dc.client.CreateDocFolder(context.Background(), dc.repo, client.DocFolderCreateInput{
		Name:       name,
		ParentUUID: parentUUID,
	}, opts.dryRun)
	if err != nil {
		return err
	}
	row := newDocFolderRow(dc.folders, folder, joinFolderPath(parentDisplay, folder.Name))
	if opts.dryRun {
		return emitDryRun(row)
	}
	if opts.output == outputJSON {
		return emit(row)
	}
	return ok("created folder %s in %s", row.Path, dc.repo.Prefix)
}

func docFolderRenameCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "rename [PATH] [NEW-NAME]",
		Short: "Rename a document folder in place (its parent is untouched)",
		Args:  cobra.RangeArgs(0, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := parseJSONInput(cmd, args, rawInput)
			if err != nil {
				return err
			}
			if raw != nil {
				in, _, err := inputio.DecodeStrict[inputs.DocFolderRenameInput](raw)
				if err != nil {
					return err
				}
				if strings.TrimSpace(in.Path) == "" || strings.TrimSpace(in.Name) == "" {
					return fmt.Errorf("path and name are required")
				}
				return renameDocFolder(in.Path, in.Name)
			}
			if len(args) != 2 {
				return fmt.Errorf("requires <PATH> <NEW-NAME> positionals or --json")
			}
			return renameDocFolder(args[0], args[1])
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}

func renameDocFolder(path, newName string) error {
	dc, closer, err := openDocFolderContext()
	if err != nil {
		return err
	}
	defer closer()
	folder, err := dc.find(path)
	if err != nil {
		return err
	}
	oldPath := dc.displayPath(folder)
	updated, err := dc.client.RenameDocFolder(context.Background(), dc.repo, folder.UUID, newName, opts.dryRun)
	if err != nil {
		return err
	}
	// The rename does not move the folder, so the new path is the old
	// one with its final segment swapped.
	parentPath := ""
	if idx := strings.LastIndex(oldPath, store.FolderPathSeparator); idx >= 0 {
		parentPath = oldPath[:idx]
	}
	row := newDocFolderRow(dc.folders, updated, joinFolderPath(parentPath, updated.Name))
	if opts.dryRun {
		return emitDryRun(row)
	}
	if opts.output == outputJSON {
		return emit(row)
	}
	return ok("renamed folder %s → %s", oldPath, row.Path)
}

func docFolderMvCmd() *cobra.Command {
	var (
		to       string
		toRoot   bool
		rawInput string
	)
	cmd := &cobra.Command{
		Use:   "mv [PATH]",
		Short: "Move a document folder (and its whole subtree) under a new parent",
		Long: `Re-parent a folder. Its subfolders and pages move with it.

The destination is required and may be the tree root. Two equivalent
spellings, because "" is a real destination rather than a missing one:

  bacio doc folder mv Design/API --to Architecture
  bacio doc folder mv Design/API --to-root

Moving a folder inside its own subtree, or past the nesting depth cap,
is refused by the store — including after a --dry-run said otherwise,
since those guards live inside the write transaction.`,
		Args: cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := parseJSONInput(cmd, args, rawInput, "to", "to-root")
			if err != nil {
				return err
			}
			if raw != nil {
				in, present, err := inputio.DecodeStrict[inputs.DocFolderMvInput](raw)
				if err != nil {
					return err
				}
				if strings.TrimSpace(in.Path) == "" {
					return fmt.Errorf("path is required")
				}
				// Presence, not emptiness: "" is the tree root, so an
				// omitted key must not silently mean "re-root it".
				if _, ok := present["to"]; !ok {
					return fmt.Errorf(`to is required; pass "" to move the folder to the tree root`)
				}
				return moveDocFolder(in.Path, in.To)
			}
			if len(args) != 1 {
				return fmt.Errorf("requires <PATH> positional or --json")
			}
			dest, err := resolveDestinationFlag(cmd, "to", to, toRoot, "--to <PARENT-PATH>", "--to-root")
			if err != nil {
				return err
			}
			return moveDocFolder(args[0], dest)
		},
	}
	cmd.Flags().StringVar(&to, "to", "", "slash path of the new parent folder (pass an empty string, or --to-root, for the tree root)")
	cmd.Flags().BoolVar(&toRoot, "to-root", false, "move the folder to the tree root (the explicit spelling of --to \"\")")
	addInputFlag(cmd, &rawInput)
	return cmd
}

func moveDocFolder(path, destPath string) error {
	dc, closer, err := openDocFolderContext()
	if err != nil {
		return err
	}
	defer closer()
	folder, err := dc.find(path)
	if err != nil {
		return err
	}
	parentUUID, parentDisplay, err := dc.findParent(destPath)
	if err != nil {
		return err
	}
	oldPath := dc.displayPath(folder)
	updated, err := dc.client.MoveDocFolder(context.Background(), dc.repo, client.DocFolderMoveInput{
		UUID:          folder.UUID,
		NewParentUUID: parentUUID,
	}, opts.dryRun)
	if err != nil {
		return err
	}
	row := newDocFolderRow(dc.folders, updated, joinFolderPath(parentDisplay, updated.Name))
	// A move re-parents the row, so the cached tree's parent index is
	// stale for this folder; state the destination explicitly.
	row.ParentUUID = parentUUID
	if opts.dryRun {
		return emitDryRun(row)
	}
	if opts.output == outputJSON {
		return emit(row)
	}
	return ok("moved folder %s → %s", oldPath, row.Path)
}

func docFolderRmCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "rm [PATH]",
		Short: "Delete a document folder (subfolders go with it; pages are re-rooted)",
		Long: `Delete a folder.

Descendant folders are deleted with it. Every document anywhere in that
subtree is RE-ROOTED, never deleted — a folder is organisational, so
losing one must never lose a page.

Rehearse with --dry-run to see both counts before committing.`,
		Args: cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := agentmode.DenyIfEnabled("doc folder rm"); err != nil {
				return err
			}
			raw, err := parseJSONInput(cmd, args, rawInput)
			if err != nil {
				return err
			}
			if raw != nil {
				in, _, err := inputio.DecodeStrict[inputs.DocFolderRmInput](raw)
				if err != nil {
					return err
				}
				if strings.TrimSpace(in.Path) == "" {
					return fmt.Errorf("path is required")
				}
				return removeDocFolder(in.Path)
			}
			if len(args) != 1 {
				return fmt.Errorf("requires <PATH> positional or --json")
			}
			return removeDocFolder(args[0])
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}

func removeDocFolder(path string) error {
	dc, closer, err := openDocFolderContext()
	if err != nil {
		return err
	}
	defer closer()
	folder, err := dc.find(path)
	if err != nil {
		return err
	}
	display := dc.displayPath(folder)
	deleted, preview, err := dc.client.DeleteDocFolder(context.Background(), dc.repo, folder.UUID, opts.dryRun)
	if err != nil {
		return err
	}
	if opts.dryRun {
		return emitDryRun(preview)
	}
	if opts.output == outputJSON {
		return emit(newDocFolderRow(dc.folders, deleted, display))
	}
	return ok("deleted folder %s", display)
}

func docFolderListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the repo's document folders, in tree order",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			dc, closer, err := openDocFolderContext()
			if err != nil {
				return err
			}
			defer closer()
			rows := make([]*docFolderRow, 0, len(dc.folders))
			for _, f := range dc.folders {
				rows = append(rows, newDocFolderRow(dc.folders, f, dc.displayPath(f)))
			}
			// Depth-then-position order out of the store already; sort by
			// display path so the text output reads as a tree.
			sortDocFolderRows(rows)
			return emit(rows)
		},
	}
}

// sortDocFolderRows orders rows by display path so parents precede their
// children and siblings keep their stored ordering. Insertion sort over
// a comparison that walks segments: cheap at tree sizes, and stable.
func sortDocFolderRows(rows []*docFolderRow) {
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0 && rows[j].Path < rows[j-1].Path; j-- {
			rows[j], rows[j-1] = rows[j-1], rows[j]
		}
	}
}

// ---------------------------------------------------------------------
// `bacio doc mv` — file a document into a folder
// ---------------------------------------------------------------------

func docMvCmd() *cobra.Command {
	var (
		folder   string
		toRoot   bool
		position int
		rawInput string
	)
	cmd := &cobra.Command{
		Use:   "mv [filename]",
		Short: "File a document into a folder (or back to the tree root)",
		Long: `Move a document between folders.

The document's filename does not change — folders are metadata, so
links, URLs and the sync layout are untouched.

The destination is required and may be the tree root:

  bacio doc mv auth.md --folder Design/API
  bacio doc mv auth.md --to-root

--position is a loose sort key within the folder (siblings may share
one; listings tie-break on filename). Omit it to append.`,
		Args: cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := parseJSONInput(cmd, args, rawInput, "folder", "to-root", "position")
			if err != nil {
				return err
			}
			if raw != nil {
				in, present, err := inputio.DecodeStrict[inputs.DocMvInput](raw)
				if err != nil {
					return err
				}
				if strings.TrimSpace(in.Filename) == "" {
					return fmt.Errorf("filename is required")
				}
				if _, ok := present["folder"]; !ok {
					return fmt.Errorf(`folder is required; pass "" to move the document to the tree root`)
				}
				return moveDocumentToFolder(in.Filename, in.Folder, in.Position)
			}
			if len(args) != 1 {
				return fmt.Errorf("requires <filename> positional or --json")
			}
			dest, err := resolveDestinationFlag(cmd, "folder", folder, toRoot, "--folder <PATH>", "--to-root")
			if err != nil {
				return err
			}
			var pos *int
			if cmd.Flags().Changed("position") {
				pos = &position
			}
			return moveDocumentToFolder(args[0], dest, pos)
		},
	}
	cmd.Flags().StringVar(&folder, "folder", "", "slash path of the destination folder (pass an empty string, or --to-root, for the tree root)")
	cmd.Flags().BoolVar(&toRoot, "to-root", false, "move the document to the tree root (the explicit spelling of --folder \"\")")
	cmd.Flags().IntVar(&position, "position", 0, "sort key within the destination folder (default: append after its current members)")
	addInputFlag(cmd, &rawInput)
	return cmd
}

func moveDocumentToFolder(filename, destPath string, position *int) error {
	// Filename validation is the same strict store-boundary rule every
	// other doc verb runs, so a bad filename fails identically here.
	name, err := validateDocFilename(filename)
	if err != nil {
		return err
	}
	dc, closer, err := openDocFolderContext()
	if err != nil {
		return err
	}
	defer closer()
	folderUUID, folderDisplay, err := dc.findParent(destPath)
	if err != nil {
		return err
	}
	doc, err := dc.client.MoveDocumentToFolder(context.Background(), dc.repo, client.DocumentFolderMoveInput{
		Filename:   name,
		FolderUUID: folderUUID,
		Position:   position,
	}, opts.dryRun)
	if err != nil {
		return err
	}
	if opts.dryRun {
		return emitDryRun(doc)
	}
	if opts.output == outputJSON {
		return emit(doc)
	}
	target := folderDisplay
	if target == "" {
		target = "the tree root"
	}
	return ok("moved %s to %s (position %d)", doc.Filename, target, doc.FolderPosition)
}

// resolveDestinationFlag implements the shared "the empty string is a
// real destination" dance for the flag path, used by `doc folder mv`,
// `doc mv` and `kanban move`.
//
// The value flag and its sentinel (`--to-root` / `--off-board`) are
// mutually exclusive, and exactly ONE of them must be given. Defaulting
// to the root / off the board when neither is present would make a
// forgotten flag quietly destructive — the same reasoning that makes the
// --json twins check the key's presence rather than its emptiness.
func resolveDestinationFlag(cmd *cobra.Command, valueFlag, value string, emptySentinel bool, valueHint, sentinelHint string) (string, error) {
	valueGiven := cmd.Flags().Changed(valueFlag)
	switch {
	case valueGiven && emptySentinel:
		return "", fmt.Errorf("%s and %s are mutually exclusive", valueHint, sentinelHint)
	case emptySentinel:
		return "", nil
	case valueGiven:
		return value, nil
	default:
		return "", fmt.Errorf("provide %s or %s", valueHint, sentinelHint)
	}
}
