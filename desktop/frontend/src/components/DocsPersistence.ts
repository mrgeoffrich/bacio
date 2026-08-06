// Persistence helpers for the BACI-204 Documents page's per-repo
// sort preference + the BACI-219 global sidebar-collapsed preference.
// Each preference lives on its own kebab-cased `bacio-`-prefixed key so
// the storage shape stays tidy and individually upgradeable.
//
// BACI-355: the hardened-profile try/catch fallback now lives in the
// shared readLocalStorage / writeLocalStorage primitives. The global
// sidebar pref is driven through useLocalStorage at its call site (via
// `sidebarCollapsedCodec`); the per-repo sort map keeps its keyed
// read/persist pair (it isn't a single key→value pair the hook models),
// now built on the same primitives.

import type { SortKey } from '../lib/docsFilter';
import { readLocalStorage, writeLocalStorage, type LocalStorageCodec } from '../lib/hooks/useLocalStorage.ts';

export const SORT_KEY_KEY = 'bacio-docs-sort';
export const SIDEBAR_COLLAPSED_KEY = 'bacio-docs-sidebar-collapsed';
export const EXPANDED_FOLDERS_KEY = 'bacio-docs-expanded-folders';

// Default sort — Updated (most recent first) matches what the
// BACI-204 plan called the recommended browsing surface.
export const DEFAULT_SORT: SortKey = 'updated';

function readPerRepoMap<T>(key: string): Record<string, T> {
  const raw = readLocalStorage(key);
  if (!raw) return {};
  try {
    const parsed = JSON.parse(raw);
    if (!parsed || typeof parsed !== 'object') return {};
    return parsed as Record<string, T>;
  } catch {
    // Garbage on disk (hand-edited / legacy) — fall back to empty.
    return {};
  }
}

function persistPerRepo<T>(key: string, repo: string, value: T, isDefault: (v: T) => boolean): void {
  if (!repo) return;
  const map = readPerRepoMap<T>(key);
  if (isDefault(value)) {
    // Trim back to the default so the on-disk JSON stays compact
    // across repos — matches the boardCompactPersistence precedent.
    delete map[repo];
  } else {
    map[repo] = value;
  }
  writeLocalStorage(key, JSON.stringify(map));
}

const VALID_SORT_KEYS: SortKey[] = ['updated', 'created', 'name', 'size'];

export function readSort(repo: string): SortKey {
  if (!repo) return DEFAULT_SORT;
  const map = readPerRepoMap<string>(SORT_KEY_KEY);
  const v = map[repo];
  if (typeof v !== 'string') return DEFAULT_SORT;
  if (!VALID_SORT_KEYS.includes(v as SortKey)) return DEFAULT_SORT;
  return v as SortKey;
}

export function persistSort(repo: string, sort: SortKey): void {
  persistPerRepo(SORT_KEY_KEY, repo, sort, v => v === DEFAULT_SORT);
}

// Expanded folders — per-repo, keyed by folder uuid (never the numeric id;
// uuid is the only folder identity that survives a sync round trip, so a
// tree expanded on one machine stays expanded on the other). Stored as a
// plain array so the on-disk JSON stays readable; read back through a Set
// because that's what the rail asks it ("is this node open?").
//
// Stale uuids (a folder deleted elsewhere) are harmless: they simply never
// match a node. They're pruned on the next persist, since the rail only
// ever writes back uuids it can still see.
export function readExpandedFolders(repo: string): string[] {
  if (!repo) return [];
  const map = readPerRepoMap<unknown>(EXPANDED_FOLDERS_KEY);
  const v = map[repo];
  if (!Array.isArray(v)) return [];
  return v.filter((u): u is string => typeof u === 'string' && u !== '');
}

export function persistExpandedFolders(repo: string, uuids: string[]): void {
  persistPerRepo(EXPANDED_FOLDERS_KEY, repo, uuids, v => v.length === 0);
}

// Sidebar collapse — global (not per-repo) preference, mirrors the
// BACI-186 ActivityTray storage shape (raw '1' for collapsed, '0' or
// anything else for expanded). DocsView drives the live value through
// useLocalStorage with this codec; the read/persist pair below is the
// equivalent non-React accessor.
export const sidebarCollapsedCodec: LocalStorageCodec<boolean> = {
  serialize: (collapsed) => (collapsed ? '1' : '0'),
  deserialize: (raw) => raw === '1',
};

export function readSidebarCollapsed(): boolean {
  const raw = readLocalStorage(SIDEBAR_COLLAPSED_KEY);
  return raw === null ? false : sidebarCollapsedCodec.deserialize(raw);
}

export function persistSidebarCollapsed(collapsed: boolean): void {
  writeLocalStorage(SIDEBAR_COLLAPSED_KEY, sidebarCollapsedCodec.serialize(collapsed));
}
