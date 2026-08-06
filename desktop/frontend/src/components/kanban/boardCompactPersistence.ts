import { useColumnSetPref } from './kanbanPersistence';
import type { ColumnSetPref } from './kanbanPersistence';

// Persistence for the Kanban board's per-lane compact-cards preference
// (BACI-191, ported to the pivot's lane axis). Parallel to
// boardCollapsePersistence — same shape, same hook, different key — so a lane
// can be compact without being collapsed and vice versa.
//
// Key changed from the pre-pivot `bacio-board-compact-columns` for the same
// reason as its sibling: the set now holds lane uuids, not `model.State`.

export const STORAGE_KEY = 'bacio-kanban-compact-columns';

// useCompactColumns returns the lane uuids whose cards the user has switched
// to the dense layout in this repo, plus the compact / uncompact toggles.
export function useCompactColumns(repo: string): ColumnSetPref {
  return useColumnSetPref(STORAGE_KEY, repo);
}
