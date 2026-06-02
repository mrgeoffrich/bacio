// Single source of truth for the BACI-221 Today / Last Week / Forever
// scope picker on the Pipeline Shipping-column Shipped popover. App.tsx owns the active
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

// ScopeSinceParams is the cutoff handback scopeSinceParams returns. At
// most one field is meaningful per scope: `today` carries an absolute
// `sinceTs` (RFC3339 of local-midnight-in-tz) and a sinceDays of 0;
// `week`/`forever` carry a `sinceDays` (7 / 0) and an empty `sinceTs`.
// Both transports send `?since_ts=` when sinceTs is non-empty, else
// `?since=` derived from sinceDays. A request carries at most one cutoff.
export interface ScopeSinceParams {
  sinceDays: number;
  sinceTs: string;
}

// tzOffsetMs returns the UTC offset (in milliseconds) of the given IANA
// zone at the instant `at`. Computed by formatting `at` into the zone's
// wall-clock components and diffing against the same components read as
// UTC — the standard Intl-based offset probe, correct across DST
// boundaries (the offset is taken at `at`, not the host's offset). A
// positive result means the zone is ahead of UTC (e.g. +11h for Sydney
// in summer).
function tzOffsetMs(timeZone: string, at: Date): number {
  // 'en-CA' yields ISO-ish YYYY-MM-DD; hourCycle h23 avoids a "24" hour.
  const dtf = new Intl.DateTimeFormat('en-CA', {
    timeZone,
    hourCycle: 'h23',
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  });
  const parts = dtf.formatToParts(at);
  const get = (t: string) => parts.find((p) => p.type === t)?.value ?? '0';
  const asUTC = Date.UTC(
    Number(get('year')),
    Number(get('month')) - 1,
    Number(get('day')),
    Number(get('hour')),
    Number(get('minute')),
    Number(get('second')),
  );
  // asUTC is the zone's wall-clock reinterpreted as UTC; the gap to the
  // real instant is the zone's offset. Round to whole seconds so a
  // sub-second drift doesn't leak into the timestamp.
  return Math.round((asUTC - at.getTime()) / 1000) * 1000;
}

// resolveZone returns the zone to compute midnight against: the stored
// `tz` when set, otherwise the browser's own zone. An empty / unset
// ui.timezone must never break "Today" (the setting auto-detects + fills
// on first run, but the count must work in the gap before it does).
function resolveZone(tz: string): string {
  const trimmed = (tz || '').trim();
  if (trimmed) return trimmed;
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
  } catch {
    return 'UTC';
  }
}

// localMidnightTs returns the RFC3339 / UTC timestamp of the most recent
// local midnight in `timeZone` — the absolute lower bound for the "shipped
// today" count. It reads today's calendar date in the zone, then finds the
// UTC instant of 00:00 on that date, taking the zone's offset *at that
// midnight* so it's correct across DST transitions (a spring-forward day's
// midnight uses the pre-jump offset).
function localMidnightTs(timeZone: string): string {
  const now = new Date();
  // Today's Y/M/D as seen in the target zone.
  const ymd = new Intl.DateTimeFormat('en-CA', {
    timeZone,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  }).formatToParts(now);
  const get = (t: string) => ymd.find((p) => p.type === t)?.value ?? '0';
  const y = Number(get('year'));
  const m = Number(get('month'));
  const d = Number(get('day'));
  // First approximation: midnight as if the wall-clock were UTC, shifted
  // by the offset at `now`. Then re-probe the offset at that candidate
  // instant and correct, so a midnight that straddles a DST change lands
  // on the right side of the jump.
  const wallMidnightUTC = Date.UTC(y, m - 1, d, 0, 0, 0);
  const offAtNow = tzOffsetMs(timeZone, now);
  let midnight = new Date(wallMidnightUTC - offAtNow);
  const offAtMidnight = tzOffsetMs(timeZone, midnight);
  if (offAtMidnight !== offAtNow) {
    midnight = new Date(wallMidnightUTC - offAtMidnight);
  }
  return midnight.toISOString();
}

// scopeSinceParams (BACI-312) is the single source of truth for the
// cutoff a given scope sends to the server. `today` resolves to an
// absolute local-midnight-in-`tz` timestamp (so "Today" means the calendar
// day, not a rolling 24h window); `week` / `forever` keep using the
// relative `sinceDays` path unchanged. `tz` is the stored ui.timezone (or
// "" before auto-detect fills it — see resolveZone's browser fallback).
export function scopeSinceParams(s: ShippedScope, tz: string): ScopeSinceParams {
  if (s === 'today') {
    return { sinceDays: 0, sinceTs: localMidnightTs(resolveZone(tz)) };
  }
  // week / forever keep the relative-window path verbatim — only `today`
  // graduated to an absolute cutoff.
  return { sinceDays: scopeSinceDays(s), sinceTs: '' };
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
