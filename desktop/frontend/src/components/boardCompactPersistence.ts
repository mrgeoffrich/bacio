// Persistence helpers for the Board's per-column compact preference
// (BACI-191). Parallel to boardCollapsePersistence.ts (BACI-188) —
// same try/catch shape, same per-repo nested-array JSON layout, same
// kebab-cased `bacio-`-prefixed key. The two helpers share the same
// storage *shape* but live on different keys so compact and collapsed
// state are independent (a column can be compact without being
// collapsed). A shared `useLocalStoragePref` hook is still out of scope
// (third new caller with the same shape as boardCollapsePersistence.ts;
// if a fourth shows up the refactor is justified).
//
// Storage shape: a per-repo nested JSON object on the
// `bacio-board-compact-columns` key. The top level maps repo
// prefix → array of compact state strings; we land it as an array
// (not a Set) because JSON has no Set type. Read returns a fresh
// Set<string> per call so callers can mutate freely without
// affecting the on-disk shape. localStorage is always present
// inside the Wails webview, but a hardened browser profile can
// throw on access — every read/write is wrapped and falls back to
// an empty set.

export const STORAGE_KEY = 'bacio-board-compact-columns';

// readCompactMap returns the raw top-level object. Keys are repo
// prefixes; values are arrays of canonical state strings. Defensive
// against missing keys and malformed JSON — anything we can't parse
// reads as {}.
function readCompactMap(): Record<string, string[]> {
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

export function readCompact(repo: string): Set<string> {
  if (!repo) return new Set();
  const map = readCompactMap();
  const v = map[repo];
  if (!Array.isArray(v)) return new Set();
  // Filter out non-string entries so a malformed on-disk payload
  // doesn't surface in the predicate as e.g. Set<number>.
  return new Set(v.filter(s => typeof s === 'string'));
}

export function persistCompact(repo: string, set: Set<string>): void {
  if (!repo) return;
  try {
    const map = readCompactMap();
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
