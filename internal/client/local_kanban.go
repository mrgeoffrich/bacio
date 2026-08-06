package client

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// Kanban lanes — the human board, orthogonal to model.State.
//
// A card is on the board iff its kanban_column_id is non-NULL. That NULL
// is what stops the Kanban and the Agentic Pipeline double-rendering the
// same `todo` card: a git repo's issues start OFF the board and are
// opted in explicitly, while a workspace's issues default onto the first
// lane at create time (see localClient.CreateIssue).
//
// Lanes are addressed by **uuid** for the same reasons doc folders are —
// see the header comment in local_docfolder.go — and every mutator
// re-checks repo ownership before touching a row.

// kanbanAppendPosition is the sentinel the "append" path hands to
// store.SetIssueKanbanColumn. The store clamps a card's position to
// [0, n] where n is the target lane's size — n meaning "last" is its
// documented append idiom — so any value past the end lands the card at
// the bottom without a second query to count the lane first.
const kanbanAppendPosition = math.MaxInt32

func (c *localClient) ListKanbanColumns(ctx context.Context, repo *model.Repo) ([]*model.KanbanColumn, error) {
	cols, err := c.store.ListKanbanColumns(repo.ID)
	if err != nil {
		return nil, err
	}
	if cols == nil {
		cols = []*model.KanbanColumn{}
	}
	return cols, nil
}

func (c *localClient) CreateKanbanColumn(ctx context.Context, repo *model.Repo, name string, dryRun bool) (*model.KanbanColumn, error) {
	clean, err := store.ValidateKanbanColumnName(name)
	if err != nil {
		return nil, err
	}
	if dryRun {
		existing, lerr := c.store.ListKanbanColumns(repo.ID)
		if lerr != nil {
			return nil, lerr
		}
		return &model.KanbanColumn{
			RepoID:   repo.ID,
			Name:     clean,
			Position: len(existing), // a new lane appends to the right
		}, nil
	}
	col, err := c.store.CreateKanbanColumn(repo.ID, clean)
	if err != nil {
		return nil, err
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &repo.ID, RepoPrefix: repo.Prefix,
		Op: "kanban_column.create", Kind: "kanban_column",
		TargetID: &col.ID, TargetLabel: col.Name,
		Details: fmt.Sprintf("position=%d", col.Position),
	})
	return col, nil
}

func (c *localClient) RenameKanbanColumn(ctx context.Context, repo *model.Repo, uuid, newName string, dryRun bool) (*model.KanbanColumn, error) {
	col, err := c.kanbanColumnInRepo(repo, uuid)
	if err != nil {
		return nil, err
	}
	clean, err := store.ValidateKanbanColumnName(newName)
	if err != nil {
		return nil, err
	}
	if dryRun {
		projected := *col
		projected.Name = clean
		return &projected, nil
	}
	oldName := col.Name
	if err := c.store.RenameKanbanColumn(col.ID, clean); err != nil {
		return nil, err
	}
	updated, err := c.store.GetKanbanColumnByID(col.ID)
	if err != nil {
		return nil, err
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &repo.ID, RepoPrefix: repo.Prefix,
		Op: "kanban_column.rename", Kind: "kanban_column",
		TargetID: &updated.ID, TargetLabel: updated.Name,
		Details: fmt.Sprintf("%s → %s", oldName, updated.Name),
	})
	return updated, nil
}

func (c *localClient) ReorderKanbanColumn(ctx context.Context, repo *model.Repo, uuid string, position int, dryRun bool) (*model.KanbanColumn, error) {
	col, err := c.kanbanColumnInRepo(repo, uuid)
	if err != nil {
		return nil, err
	}
	if dryRun {
		// The store clamps to [0, n-1]; rehearse the clamp so the
		// projection can't advertise a slot the real call won't produce.
		existing, lerr := c.store.ListKanbanColumns(repo.ID)
		if lerr != nil {
			return nil, lerr
		}
		projected := *col
		projected.Position = clampInt(position, 0, len(existing)-1)
		return &projected, nil
	}
	from := col.Position
	if err := c.store.ReorderKanbanColumn(col.ID, position); err != nil {
		return nil, err
	}
	updated, err := c.store.GetKanbanColumnByID(col.ID)
	if err != nil {
		return nil, err
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &repo.ID, RepoPrefix: repo.Prefix,
		Op: "kanban_column.reorder", Kind: "kanban_column",
		TargetID: &updated.ID, TargetLabel: updated.Name,
		Details: fmt.Sprintf("position %d → %d", from, updated.Position),
	})
	return updated, nil
}

func (c *localClient) DeleteKanbanColumn(ctx context.Context, repo *model.Repo, uuid string, dryRun bool) (*model.KanbanColumn, *KanbanColumnDeletePreview, error) {
	col, err := c.kanbanColumnInRepo(repo, uuid)
	if err != nil {
		return nil, nil, err
	}
	if dryRun {
		n, cerr := c.countCardsInLane(repo, col.ID)
		if cerr != nil {
			return nil, nil, cerr
		}
		return nil, &KanbanColumnDeletePreview{
			Column:      col,
			Cascade:     KanbanColumnCascadeCount{IssuesRemovedFromBoard: n},
			WouldDelete: true,
		}, nil
	}
	n, err := c.countCardsInLane(repo, col.ID)
	if err != nil {
		return nil, nil, err
	}
	if err := c.store.DeleteKanbanColumn(col.ID); err != nil {
		return nil, nil, err
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &repo.ID, RepoPrefix: repo.Prefix,
		Op: "kanban_column.delete", Kind: "kanban_column",
		TargetID: &col.ID, TargetLabel: col.Name,
		Details: fmt.Sprintf("%d card(s) taken off the board", n),
	})
	return col, nil, nil
}

func (c *localClient) MoveIssueToKanbanColumn(ctx context.Context, repo *model.Repo, in IssueKanbanMoveInput, dryRun bool) (*model.Issue, error) {
	iss, err := c.GetIssueByKey(ctx, repo, in.IssueKey)
	if err != nil {
		return nil, err
	}
	if iss.RepoID != repo.ID {
		return nil, fmt.Errorf("issue %s is not in repo %s", iss.Key, repo.Prefix)
	}
	var (
		columnID *int64
		target   = "(off the board)"
	)
	if in.ColumnUUID != "" {
		col, cerr := c.kanbanColumnInRepo(repo, in.ColumnUUID)
		if cerr != nil {
			return nil, cerr
		}
		columnID = &col.ID
		target = col.Name
	}
	position := kanbanAppendPosition
	if in.Position != nil {
		if *in.Position < 0 {
			return nil, fmt.Errorf("position must be >= 0, got %d", *in.Position)
		}
		position = *in.Position
	}
	if dryRun {
		projected := *iss
		projected.KanbanColumnID = columnID
		if columnID == nil {
			projected.KanbanPosition = 0
		} else {
			// Rehearse the store's clamp. `position` is the
			// kanbanAppendPosition sentinel whenever the caller omitted
			// one, so echoing it verbatim would report 2147483647 as the
			// slot the card is about to land in — a projection that is
			// never what the real call produces. Same stance as
			// ReorderKanbanColumn's dry-run above.
			slot, perr := c.projectedKanbanPosition(repo, iss, *columnID, position)
			if perr != nil {
				return nil, perr
			}
			projected.KanbanPosition = slot
		}
		return &projected, nil
	}
	if err := c.store.SetIssueKanbanColumn(iss.ID, columnID, position); err != nil {
		return nil, err
	}
	updated, err := c.store.GetIssueByID(iss.ID)
	if err != nil {
		return nil, err
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &repo.ID, RepoPrefix: repo.Prefix,
		Op: "issue.kanban", Kind: "issue",
		TargetID: &updated.ID, TargetLabel: updated.Key,
		Details: fmt.Sprintf("column=%s position=%d", target, updated.KanbanPosition),
	})
	return updated, nil
}

// ---------------------------------------------------------------------
// Internals
// ---------------------------------------------------------------------

// kanbanColumnInRepo resolves a lane uuid and asserts it belongs to repo.
// See docFolderInRepo for why the repo check is mandatory rather than
// belt-and-braces.
func (c *localClient) kanbanColumnInRepo(repo *model.Repo, uuid string) (*model.KanbanColumn, error) {
	if strings.TrimSpace(uuid) == "" {
		return nil, fmt.Errorf("kanban column uuid is required")
	}
	col, err := c.store.GetKanbanColumnByUUID(uuid)
	if err != nil {
		return nil, err
	}
	if col.RepoID != repo.ID {
		return nil, fmt.Errorf("kanban column %s is not in repo %s", uuid, repo.Prefix)
	}
	return col, nil
}

// projectedKanbanPosition is the slot SetIssueKanbanColumn would actually
// give `iss` in lane `columnID` for the requested `want`.
//
// store.renumberKanbanLaneTx lifts the moved card out of the lane and
// re-inserts it at an index clamped to [0, n] where n is the lane's size
// WITHOUT that card — so a card moving within its own lane sees one fewer
// slot than a card arriving from elsewhere. Mirroring that here is what
// lets --dry-run report the real answer instead of the append sentinel.
//
// countCardsInLane counts archived rows too, matching the store's
// renumber (which deliberately does not skip them).
func (c *localClient) projectedKanbanPosition(repo *model.Repo, iss *model.Issue, columnID int64, want int) (int, error) {
	n, err := c.countCardsInLane(repo, columnID)
	if err != nil {
		return 0, err
	}
	if iss.KanbanColumnID != nil && *iss.KanbanColumnID == columnID {
		n-- // already in this lane; it doesn't occupy a slot it's moving from
	}
	return clampInt(want, 0, n), nil
}

// countCardsInLane counts the issues currently in one lane — the delete
// preview's blast radius. store.IssueFilter's board axis is the boolean
// OnKanban (there is no per-column filter, deliberately: the shared list
// query stays one shape), so the per-lane count is a client-side tally
// over the repo's on-board cards. Archived rows are included: they are
// hidden from the board but their membership is still cleared by the
// delete, so leaving them out would under-report the change.
func (c *localClient) countCardsInLane(repo *model.Repo, columnID int64) (int, error) {
	onKanban := true
	issues, err := c.store.ListIssues(store.IssueFilter{
		RepoID:          &repo.ID,
		OnKanban:        &onKanban,
		IncludeArchived: true,
	})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, iss := range issues {
		if iss.KanbanColumnID != nil && *iss.KanbanColumnID == columnID {
			n++
		}
	}
	return n, nil
}

// placeOnFirstKanbanLane implements the workspace half of the
// "everything on the board by default" rule: a new card in a workspace
// is appended to the leftmost lane.
//
// An empty board is NOT an error. Lanes are user-editable — a workspace
// owner can rename them, reorder them, or delete every last one — and
// refusing to create an issue because the board is empty would make a
// destructive board edit break issue creation. The card simply stays off
// the board, exactly as it would in a git repo, and the user can drag it
// on once a lane exists.
func (c *localClient) placeOnFirstKanbanLane(repo *model.Repo, iss *model.Issue) error {
	lanes, err := c.store.ListKanbanColumns(repo.ID)
	if err != nil {
		return err
	}
	if len(lanes) == 0 {
		return nil
	}
	return c.store.SetIssueKanbanColumn(iss.ID, &lanes[0].ID, kanbanAppendPosition)
}

func clampInt(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
