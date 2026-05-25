// Persistence helpers for the per-repo set of activity-pinned kanban
// cards (BACI-192). Sibling of activityTrayPersistence.ts (the
// collapse/expand pref) — same try/catch shape, same kebab-cased
// `bacio-`-prefixed key, helpers live next to the component that reads
// them. The pin set is *per-repo* because an issue key (`MINI-42`) is
// only meaningful inside its repo; pinning on one project shouldn't
// follow the user across to another.
//
// Storage shape: JSON-encoded string array of issue keys under the
// per-repo key `bacio-activity-pinned-<repo-prefix>`. Reads tolerate
// any kind of malformed JSON / non-array shape by returning an empty
// Set (same defensive shape as the board-scroll map in Board.jsx).
// Writes silently swallow localStorage throws — the pin set is a UI
// affordance, not authoritative state, so a hardened browser profile
// dropping writes is non-fatal.

const KEY_PREFIX = 'bacio-activity-pinned-';

function storageKey(repoPrefix: string): string {
  return `${KEY_PREFIX}${repoPrefix}`;
}

export function readPinnedKeys(repoPrefix: string): Set<string> {
  if (!repoPrefix) return new Set();
  try {
    const raw = localStorage.getItem(storageKey(repoPrefix));
    if (!raw) return new Set();
    const parsed = JSON.parse(raw);
    if (!Array.isArray(parsed)) return new Set();
    const out = new Set<string>();
    for (const k of parsed) {
      if (typeof k === 'string' && k) out.add(k);
    }
    return out;
  } catch {
    return new Set();
  }
}

export function persistPinnedKeys(repoPrefix: string, keys: Set<string>): void {
  if (!repoPrefix) return;
  try {
    if (keys.size === 0) {
      localStorage.removeItem(storageKey(repoPrefix));
      return;
    }
    localStorage.setItem(storageKey(repoPrefix), JSON.stringify(Array.from(keys)));
  } catch {
    /* non-fatal — the pin set just won't survive a relaunch */
  }
}
