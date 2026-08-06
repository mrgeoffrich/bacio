import { useCallback, useMemo } from 'react';
import type { LocalStorageCodec } from '../../lib/hooks/useLocalStorage';
import { readLocalStorage, useLocalStorage, writeLocalStorage } from '../../lib/hooks/useLocalStorage';

// Shared persistence plumbing for the Kanban board's per-lane UI
// preferences (collapsed / compact) and its horizontal scroll offset.
//
// The two lane-preference modules beside this one (boardCollapsePersistence /
// boardCompactPersistence) differ only in their storage key, so the map
// shape, the codec and the read/add/remove hook live here once. When the
// pre-pivot board owned these they each hand-rolled the same try/catch
// read/persist pair and a `useState + useEffect(re-seed on repo switch)`
// dance in Board.jsx; `useLocalStorage` (BACI-355) is exactly the
// abstraction those files' comments said was "still out of scope", so the
// port lands on it: the whole per-repo map is the persisted value and the
// active repo's set is *derived*, which makes the repo-switch re-seed fall
// out for free instead of needing an effect.
//
// **Lanes are addressed by uuid, not by state.** The pre-pivot board keyed
// these sets on a `model.State` string; a Kanban lane is a user-created row
// with a uuid, so the value space of the map changed completely. New keys
// (`bacio-kanban-*` rather than `bacio-board-*`) therefore replace the old
// ones — reusing them would silently mix stale state strings in with lane
// uuids.

// ColumnSetMap is the on-disk shape: repo prefix → the lane uuids that are
// flagged for this preference. An array (not a Set) because JSON has no Set.
export type ColumnSetMap = Record<string, string[]>;

// columnSetMapCodec round-trips the map through JSON, tolerating anything
// malformed on disk by falling back to an empty map. Defensive on both
// levels: a non-object top level and non-string entries are both dropped, so
// a hand-edited / half-written payload can never surface as e.g. Set<number>.
export const columnSetMapCodec: LocalStorageCodec<ColumnSetMap> = {
  serialize: (map) => JSON.stringify(map),
  deserialize: (raw) => {
    try {
      const parsed: unknown = JSON.parse(raw);
      if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return {};
      const out: ColumnSetMap = {};
      for (const [repo, value] of Object.entries(parsed as Record<string, unknown>)) {
        if (!Array.isArray(value)) continue;
        out[repo] = value.filter((entry): entry is string => typeof entry === 'string');
      }
      return out;
    } catch {
      return {};
    }
  },
};

// columnSetFor reads one repo's flagged lane uuids out of the map. Returns a
// fresh Set per call so callers may mutate it freely; the hook below memoises
// it so a render pass doesn't churn a new identity into memo'd children.
export function columnSetFor(map: ColumnSetMap, repo: string): Set<string> {
  if (!repo) return new Set();
  const entry = map[repo];
  return new Set(Array.isArray(entry) ? entry : []);
}

// withColumnSet writes one repo's set back into the map, trimming the entry
// entirely when the set empties so the persisted JSON stays tidy across
// repos (the same reason the pre-pivot helpers deleted the empty key).
export function withColumnSet(map: ColumnSetMap, repo: string, set: Set<string>): ColumnSetMap {
  const next = { ...map };
  if (set.size === 0) {
    delete next[repo];
  } else {
    next[repo] = Array.from(set);
  }
  return next;
}

// ColumnSetPref is what a lane-preference hook hands back: the active repo's
// current set plus the two idempotent toggles. `add` / `remove` are stable
// identities so the lane components they're passed to stay memo-friendly.
export type ColumnSetPref = {
  set: Set<string>;
  add: (uuid: string) => void;
  remove: (uuid: string) => void;
};

// useColumnSetPref is the shared implementation behind useCollapsedColumns /
// useCompactColumns. The persisted value is the WHOLE per-repo map, so
// switching repos re-derives the active set with no effect and no stale
// carry-over. Both toggles return the previous map unchanged on a redundant
// flip, which also skips the localStorage write.
export function useColumnSetPref(storageKey: string, repo: string): ColumnSetPref {
  const [map, setMap] = useLocalStorage<ColumnSetMap>(storageKey, {}, columnSetMapCodec);

  const set = useMemo(() => columnSetFor(map, repo), [map, repo]);

  const add = useCallback((uuid: string) => {
    if (!repo || !uuid) return;
    setMap(prev => {
      const current = columnSetFor(prev, repo);
      if (current.has(uuid)) return prev;
      current.add(uuid);
      return withColumnSet(prev, repo, current);
    });
  }, [repo, setMap]);

  const remove = useCallback((uuid: string) => {
    if (!repo || !uuid) return;
    setMap(prev => {
      const current = columnSetFor(prev, repo);
      if (!current.has(uuid)) return prev;
      current.delete(uuid);
      return withColumnSet(prev, repo, current);
    });
  }, [repo, setMap]);

  return useMemo(() => ({ set, add, remove }), [set, add, remove]);
}

// ─── Horizontal scroll offset ────────────────────────────────────────
//
// BACI-119's behaviour, ported: the board's scrollLeft is persisted per repo
// so navigating away (Documents / an issue / …) and back lands the user where
// they were instead of resetting to 0. The board element unmounts on every
// nav switch, so React can't preserve the offset — it has to be restored
// explicitly on mount. Keyed by repo prefix because switching repos changes
// the lane / card set, so a global offset would point at a stale position.
//
// This one stays an imperative read/write pair rather than a useLocalStorage
// value: the offset is written from a debounced scroll handler and read in a
// layout effect, neither of which wants a React state round-trip.

export const SCROLL_STORAGE_KEY = 'bacio-kanban-scroll';

function readScrollMap(): Record<string, number> {
  const raw = readLocalStorage(SCROLL_STORAGE_KEY);
  if (!raw) return {};
  try {
    const parsed: unknown = JSON.parse(raw);
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return {};
    return parsed as Record<string, number>;
  } catch {
    return {};
  }
}

export function readKanbanScroll(repo: string): number {
  if (!repo) return 0;
  const value = readScrollMap()[repo];
  return typeof value === 'number' && Number.isFinite(value) ? value : 0;
}

export function persistKanbanScroll(repo: string, offset: number): void {
  if (!repo) return;
  const map = readScrollMap();
  map[repo] = Math.max(0, Math.round(offset));
  writeLocalStorage(SCROLL_STORAGE_KEY, JSON.stringify(map));
}
