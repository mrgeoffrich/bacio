// Persistence helper for the BACI-221 Pipeline Shipping-column Shipped scope picker.
// Mirrors `activityTrayPersistence.ts` (single global string on a
// kebab-cased `bacio-` key) — *not* the per-repo nested-object shape
// `boardCompactPersistence.ts` uses, because the scope is a personal
// preference that doesn't change per repo. The hardened-profile fallback
// now lives in the shared readLocalStorage / writeLocalStorage primitives
// (BACI-355); `shippedScopeCodec` carries the scope-specific validation.
//
// Default scope is `'week'` so the first-launch behaviour matches the
// pre-BACI-221 pill exactly (the existing 7-day count). App.tsx drives the
// live value through useLocalStorage with this codec; the read/persist pair
// below is the equivalent non-React accessor.

import { isShippedScope, type ShippedScope } from './shippedScope.ts';
import { readLocalStorage, writeLocalStorage, type LocalStorageCodec } from '../lib/hooks/useLocalStorage.ts';

export const STORAGE_KEY = 'bacio-shipped-scope';
export const DEFAULT_SCOPE: ShippedScope = 'week';

// The scope is its own on-disk string; deserialize just rejects a legacy /
// hand-edited value that isn't one of the three canonical scopes.
export const shippedScopeCodec: LocalStorageCodec<ShippedScope> = {
  serialize: (scope) => scope,
  deserialize: (raw) => (isShippedScope(raw) ? raw : DEFAULT_SCOPE),
};

export function readShippedScope(): ShippedScope {
  const raw = readLocalStorage(STORAGE_KEY);
  return raw === null ? DEFAULT_SCOPE : shippedScopeCodec.deserialize(raw);
}

export function persistShippedScope(scope: ShippedScope): void {
  writeLocalStorage(STORAGE_KEY, shippedScopeCodec.serialize(scope));
}
