package inputs

// Kanban payloads.
//
// The Kanban board is a SEPARATE AXIS from an issue's `state`: a card is
// on the board if and only if it sits in a lane. A git repo's issues
// start off the board and are opted in explicitly; a workspace's issues
// land on the leftmost lane at create time. Moving a card between lanes
// never changes its state, and `bacio issue state` never changes its lane.
//
// Lanes are addressed on the CLI by NAME (lane names are unique per
// repo), never by uuid or id. Matching is exact first, then falls back
// to a unique case-insensitive match, so `--column doing` finds the
// stock `Doing` lane. The CLI resolves the name to the lane's uuid off
// one `ListKanbanColumns` round trip before calling the client.

// KanbanColumnAddInput is the payload for `bacio kanban column add
// --json`. The new lane is appended to the right-hand end of the board.
type KanbanColumnAddInput struct {
	Name string `json:"name"`
}

// KanbanColumnRenameInput is the payload for `bacio kanban column rename
// --json`. Column names the existing lane; Name is what it becomes.
// Cards keep their lane membership and their order.
type KanbanColumnRenameInput struct {
	Column string `json:"column"`
	Name   string `json:"name"`
}

// KanbanColumnMvInput is the payload for `bacio kanban column mv --json`
// — reorder a lane left/right on the board.
//
// Position is 0-BASED and dense: 0 is the leftmost lane, and the other
// lanes re-densify around the moved one so the board is always a gapless
// 0..n-1. Out-of-range values are clamped. (Note that `bacio issue
// reorder`, which orders cards within a Pipeline band, is 1-based — the
// two are different surfaces.)
type KanbanColumnMvInput struct {
	Column   string `json:"column"`
	Position int    `json:"position"`
}

// KanbanColumnRmInput is the payload for `bacio kanban column rm --json`.
// Deleting a lane takes its cards OFF the board — the issues themselves
// are never deleted, and their state is untouched. Rehearse with
// `--dry-run` to see how many cards would come off.
type KanbanColumnRmInput struct {
	Column string `json:"column"`
}

// KanbanMoveInput is the payload for `bacio kanban move --json` — place
// one card on the board.
//
// `column` is REQUIRED but may be empty: "" takes the card OFF the board
// entirely (the only way to un-opt a card that was dragged on). Because
// "" is a real destination, the decoder checks the key's PRESENCE rather
// than its emptiness — omitting `column` is an error, passing
// `"column": ""` is the documented way to say "off the board".
//
// Position is the 0-based top-to-bottom slot in the target lane; the
// lane re-densifies to 0..n-1 around it. Omit it to append to the bottom
// of the lane.
type KanbanMoveInput struct {
	IssueKey string `json:"issue_key"`
	Column   string `json:"column"`
	Position *int   `json:"position,omitempty"`
}
