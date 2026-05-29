// Single source of truth for the BACI-221 Today / Last Week / Forever
// scope picker on the Pipeline Shipping-column Shipped popover. App.jsx owns the active
// scope state (so the pill count and the popover list can never drift),
// ShippedPopover renders the picker, and both reach for `scopeSinceDays`
// here when shaping the API call. Keeping the type + the cutoff math in
// one tiny module is the same pattern other UI helpers in this folder
// follow (boardCompactPersistence.ts is the closest precedent for
// shared-but-typed UI constants).

export type ShippedScope = 'today' | 'week' | 'forever';

// SHIPPED_SCOPES is the canonical ordered list — used by the picker to
// render its buttons in the same order on both surfaces.
export const SHIPPED_SCOPES: ShippedScope[] = ['today', 'week', 'forever'];

// scopeSinceDays maps a scope to the `sinceDays` parameter the API
// expects. `0` is the sentinel for "no lower bound" (Forever) — both
// the Wails binding and the HTTP twin understand it the same way.
export function scopeSinceDays(s: ShippedScope): number {
  switch (s) {
    case 'today':
      return 1;
    case 'week':
      return 7;
    case 'forever':
      return 0;
  }
}

// scopeLabel is the display text the picker buttons render. Short by
// design — the picker strip is narrow and a long label would wrap.
export function scopeLabel(s: ShippedScope): string {
  switch (s) {
    case 'today':
      return 'Today';
    case 'week':
      return 'Last Week';
    case 'forever':
      return 'Forever';
  }
}

// isShippedScope is the type guard the persistence layer applies on
// the way back in — anything else (legacy string, hand-edited
// localStorage) is treated as "unknown, fall back to default".
export function isShippedScope(value: unknown): value is ShippedScope {
  return value === 'today' || value === 'week' || value === 'forever';
}
