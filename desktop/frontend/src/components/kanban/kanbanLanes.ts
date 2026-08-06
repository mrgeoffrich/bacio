import type { KanbanColumn } from '../../api';

// Pure lane-CRUD helpers for the Kanban board: where a lane sits, where a
// left/right nudge would put it, and the exact words the delete
// confirmation says. Kept out of the components for the same reason
// kanbanPlacement is — the ordering arithmetic and the wording of an
// irreversible action are both things you want a test on, and neither
// needs a DOM.

// laneIndex is a lane's 0-based slot in board order. The board is served
// in `position` order, so the array index IS the position the store will
// re-densify to — which is why the reorder call below can pass an index
// straight through as the seam's `position` argument.
export function laneIndex(columns: KanbanColumn[], uuid: string): number {
  return columns.findIndex(col => col.uuid === uuid);
}

// laneShiftTarget resolves a "move left" / "move right" nudge to the
// 0-based position `reorderKanbanColumn` wants, or null when the lane is
// already at that end of the board (the menu omits the item then, so this
// is the belt to that braces).
export function laneShiftTarget(
  columns: KanbanColumn[],
  uuid: string,
  delta: number,
): number | null {
  const from = laneIndex(columns, uuid);
  if (from < 0) return null;
  const to = from + delta;
  if (to < 0 || to > columns.length - 1) return null;
  return to;
}

// moveLaneTo is the optimistic half of a reorder: splice the lane out and
// back in at `toIndex`, then renumber every lane's `position` to match its
// new slot. The store re-densifies the same way around its own write and
// answers with the whole refreshed board, so this keeps the optimistic
// snapshot shaped like the answer that will replace it.
//
// Lanes whose position is unchanged are returned by identity so React can
// skip them.
export function moveLaneTo(
  columns: KanbanColumn[],
  uuid: string,
  toIndex: number,
): KanbanColumn[] {
  const from = laneIndex(columns, uuid);
  if (from < 0) return columns;
  const to = Math.max(0, Math.min(toIndex, columns.length - 1));
  if (to === from) return columns;
  const next = [...columns];
  const [moved] = next.splice(from, 1);
  next.splice(to, 0, moved);
  return next.map((col, index) => (col.position === index ? col : { ...col, position: index }));
}

// deleteLaneLines is the body of the delete-lane confirmation, driven by
// `previewDeleteKanbanColumn`'s `issuesRemovedFromBoard`.
//
// The wording is load-bearing. Deleting a lane is NOT a destructive act
// against the work in it: the cards come off the Kanban (their
// `kanban_column_id` goes back to NULL) and the issues themselves are
// untouched — still in the repo, still on the Agentic Pipeline, and
// re-addable to any lane. A dialog that just said "3 cards will be
// removed" would read as "3 issues will be deleted", so the copy says
// what actually happens and says it in the same breath as the count.
export function deleteLaneLines(issuesRemovedFromBoard: number): string[] {
  const removed = 'The lane itself is removed for good.';
  if (issuesRemovedFromBoard <= 0) {
    return ['This lane is empty, so no cards are affected.', removed];
  }
  if (issuesRemovedFromBoard === 1) {
    return [
      '1 card comes off the Kanban board. The issue is not deleted — it stays in the repository and can be put back on a lane at any time.',
      removed,
    ];
  }
  return [
    `${issuesRemovedFromBoard} cards come off the Kanban board. Those issues are not deleted — they stay in the repository and can be put back on a lane at any time.`,
    removed,
  ];
}
