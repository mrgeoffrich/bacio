import { useColumnSetPref } from './kanbanPersistence';
import type { ColumnSetPref } from './kanbanPersistence';

// Persistence for the Kanban board's per-lane collapsed/expanded preference
// (BACI-188, ported to the pivot's lane axis). The storage shape, codec and
// toggle hook live in ./kanbanPersistence — this module owns only the key and
// the semantics, so collapsed and compact stay independent preferences on
// independent keys (a lane can be compact without being collapsed).
//
// Key changed from the pre-pivot `bacio-board-collapsed-columns`: the set
// used to hold `model.State` strings and now holds lane uuids, so the old
// payload is meaningless here rather than merely stale.

export const STORAGE_KEY = 'bacio-kanban-collapsed-columns';

// useCollapsedColumns returns the lane uuids the user has collapsed in this
// repo, plus the collapse / expand toggles. Empty lanes are NOT folded in —
// emptiness is intrinsic and the board renders those with a drop hint
// instead, so this set is purely the user's explicit choice.
export function useCollapsedColumns(repo: string): ColumnSetPref {
  return useColumnSetPref(STORAGE_KEY, repo);
}
