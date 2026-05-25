// Persistence helpers for the Board's per-column collapsed/expanded
// preference (BACI-188). Extends the existing per-consumer pattern
// from App.jsx (`readTheme`/`persistTheme`, `readActiveRepo`/
// `persistActiveRepo`), Board.jsx (`readBoardScroll`/
// `persistBoardScroll`), and activityTrayPersistence.ts — same
// try/catch shape, same kebab-cased `bacio-`-prefixed key, helpers
// live next to the component that reads them.
//
// Storage shape: a per-repo nested JSON object on the
// `bacio-board-collapsed-columns` key. The top level maps repo
// prefix → array of collapsed state strings; we land it as an array
// (not a Set) because JSON has no Set type. Read returns a fresh
// Set<string> per call so callers can mutate freely without
// affecting the on-disk shape. localStorage is always present
// inside the Wails webview, but a hardened browser profile can
// throw on access — every read/write is wrapped and falls back to
// an empty set.
//
// A shared `useLocalStoragePref` hook is still out of scope (the
// comment at activityTrayPersistence.ts:6-9 — second new caller, but
// the storage shape differs enough — per-repo nested JSON, not a
// single boolean string — that the abstraction would have to carry
// two unrelated codecs to pay back).

export const STORAGE_KEY = 'bacio-board-collapsed-columns';

// readCollapsedMap returns the raw top-level object. Keys are repo
// prefixes; values are arrays of canonical state strings. Defensive
// against missing keys and malformed JSON — anything we can't parse
// reads as {}.
function readCollapsedMap(): Record<string, string[]> {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return {};
    const parsed = JSON.parse(raw);
    if (!parsed || typeof parsed !== 'object') return {};
    return parsed as Record<string, string[]>;
  } catch {
    return {};
  }
}

export function readCollapsed(repo: string): Set<string> {
  if (!repo) return new Set();
  const map = readCollapsedMap();
  const v = map[repo];
  if (!Array.isArray(v)) return new Set();
  // Filter out non-string entries so a malformed on-disk payload
  // doesn't surface in the predicate as e.g. Set<number>.
  return new Set(v.filter(s => typeof s === 'string'));
}

export function persistCollapsed(repo: string, set: Set<string>): void {
  if (!repo) return;
  try {
    const map = readCollapsedMap();
    if (set.size === 0) {
      // Trim the empty entry so the JSON shape stays tidy across
      // repos — same reason BACI-119 caps the scroll map.
      delete map[repo];
    } else {
      map[repo] = Array.from(set);
    }
    localStorage.setItem(STORAGE_KEY, JSON.stringify(map));
  } catch {
    /* non-fatal — the preference just won't survive the next reload */
  }
}
