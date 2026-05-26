// Persistence helpers for the BACI-204 Documents page's per-repo
// sort preference + the BACI-219 global sidebar-collapsed preference.
// Same try/catch shape as boardCompactPersistence.ts and
// activityTrayPersistence.ts; each preference lives on its own
// kebab-cased `bacio-`-prefixed key so the storage shape stays tidy
// and individually upgradeable.
//
// We now have three callers reading/writing on this pattern
// (ActivityTray, DocsPersistence sort, DocsPersistence sidebar) — a
// shared `useLocalStoragePref` hook would be the right refactor, but
// landing it inside this small UX tweak slows review without
// unblocking any new behaviour. Follow-up issue territory.

import type { SortKey } from '../lib/docsFilter';

export const SORT_KEY_KEY = 'bacio-docs-sort';
export const SIDEBAR_COLLAPSED_KEY = 'bacio-docs-sidebar-collapsed';

// Default sort — Updated (most recent first) matches what the
// BACI-204 plan called the recommended browsing surface.
export const DEFAULT_SORT: SortKey = 'updated';

function readPerRepoMap<T>(key: string): Record<string, T> {
  try {
    const raw = globalThis.localStorage?.getItem(key);
    if (!raw) return {};
    const parsed = JSON.parse(raw);
    if (!parsed || typeof parsed !== 'object') return {};
    return parsed as Record<string, T>;
  } catch {
    return {};
  }
}

function persistPerRepo<T>(key: string, repo: string, value: T, isDefault: (v: T) => boolean): void {
  if (!repo) return;
  try {
    const map = readPerRepoMap<T>(key);
    if (isDefault(value)) {
      // Trim back to the default so the on-disk JSON stays compact
      // across repos — matches the boardCompactPersistence precedent.
      delete map[repo];
    } else {
      map[repo] = value;
    }
    globalThis.localStorage?.setItem(key, JSON.stringify(map));
  } catch {
    /* non-fatal — the preference just won't survive the next reload */
  }
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

// Sidebar collapse — global (not per-repo) preference, mirrors the
// BACI-186 ActivityTray storage shape (raw '1' for collapsed, '0' or
// anything else for expanded). Hardened-storage throws fall back to
// the default (expanded).
export function readSidebarCollapsed(): boolean {
  try {
    return globalThis.localStorage?.getItem(SIDEBAR_COLLAPSED_KEY) === '1';
  } catch {
    return false;
  }
}

export function persistSidebarCollapsed(collapsed: boolean): void {
  try {
    globalThis.localStorage?.setItem(SIDEBAR_COLLAPSED_KEY, collapsed ? '1' : '0');
  } catch {
    /* non-fatal — the preference just won't survive the next reload */
  }
}
