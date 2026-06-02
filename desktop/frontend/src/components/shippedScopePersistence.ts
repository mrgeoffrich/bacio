// Persistence helper for the BACI-221 Pipeline Shipping-column Shipped scope picker.
// Mirrors `activityTrayPersistence.ts` (single global string on a
// kebab-cased `bacio-` key) — *not* the per-repo nested-object shape
// `boardCompactPersistence.ts` uses, because the scope is a personal
// preference that doesn't change per repo. Same try/catch wrapping so
// a hardened browser profile (rejected localStorage access) falls back
// to the default.
//
// Default scope is `'week'` so the first-launch behaviour matches the
// pre-BACI-221 pill exactly (the existing 7-day count). The picker
// re-writes the key on every change; the read happens once on mount in
// App.tsx so a relaunch lands on the same scope.

import { isShippedScope, type ShippedScope } from './shippedScope.ts';

export const STORAGE_KEY = 'bacio-shipped-scope';
export const DEFAULT_SCOPE: ShippedScope = 'week';

export function readShippedScope(): ShippedScope {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    return isShippedScope(raw) ? raw : DEFAULT_SCOPE;
  } catch {
    return DEFAULT_SCOPE;
  }
}

export function persistShippedScope(scope: ShippedScope): void {
  try {
    localStorage.setItem(STORAGE_KEY, scope);
  } catch {
    /* non-fatal — the preference just won't survive a relaunch */
  }
}
