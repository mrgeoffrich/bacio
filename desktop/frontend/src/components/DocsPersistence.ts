// Persistence helpers for the BACI-204 Documents page's per-repo
// preferences: hideTranscripts toggle, sort key, plus the rail's
// transient query. Same try/catch shape as boardCompactPersistence.ts;
// each preference lives on its own kebab-cased `bacio-`-prefixed key
// so the storage shape stays tidy and individually upgradeable. A
// shared `useLocalStoragePref` hook is deliberately out of scope —
// CLAUDE.md flagged it as the right refactor at the third caller, but
// landing it inside this redesign PR slows review without unblocking
// any new behaviour. Follow-up issue territory.

import type { SortKey } from '../lib/docsFilter';

export const HIDE_TRANSCRIPTS_KEY = 'bacio-docs-hide-transcripts';
export const SORT_KEY_KEY = 'bacio-docs-sort';

// Defaults — chosen to match what the plan describes as the
// recommended browsing surface: transcripts hidden (they're audit
// material) and sorted by Updated (most recent first).
export const DEFAULT_HIDE_TRANSCRIPTS = true;
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

export function readHideTranscripts(repo: string): boolean {
  if (!repo) return DEFAULT_HIDE_TRANSCRIPTS;
  const map = readPerRepoMap<boolean>(HIDE_TRANSCRIPTS_KEY);
  const v = map[repo];
  if (typeof v !== 'boolean') return DEFAULT_HIDE_TRANSCRIPTS;
  return v;
}

export function persistHideTranscripts(repo: string, value: boolean): void {
  persistPerRepo(HIDE_TRANSCRIPTS_KEY, repo, value, v => v === DEFAULT_HIDE_TRANSCRIPTS);
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
