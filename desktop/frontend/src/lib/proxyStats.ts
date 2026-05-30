// BACI-304 Monitor screen — the shared proxy-stats shape, the time-window
// scope picker, and the tiny display formatters. Lives in one module so
// MonitorView stays lean and the formatters / scope mapping are
// unit-testable from a plain-Node smoke test (proxyStats.smoketest.mjs).
//
// The BACI-108 cross-transport split: api.ts re-exports the Wails-generated
// ProxyFQDNStatDTO under the name `ProxyFQDNStat`; api.http.ts re-exports
// the TS-only interface below under the same name. MonitorView imports
// `ProxyFQDNStat` from `./api` and never knows which transport it's on.

// ProxyFQDNStat is the web-side per-FQDN rollup shape — identical fields
// to the desktop ProxyFQDNStatDTO so the two seams are interchangeable.
// ErrorRate is a fraction in [0,1]; p50Ms / p95Ms are round-trip latency
// percentiles in milliseconds; firstSeen / lastSeen are ISO timestamps.
export interface ProxyFQDNStat {
  host: string;
  requestCount: number;
  bytesIn: number;
  bytesOut: number;
  errorCount: number;
  errorRate: number;
  p50Ms: number;
  p95Ms: number;
  firstSeen: string;
  lastSeen: string;
}

// MonitorScope is the time-window the Monitor screen rolls the stats over.
// The buckets map onto the `/proxy/stats` `?since=` parameter, which is a
// rolling duration lookback (timeparse.Lookback) — not a calendar-Today
// cutoff. So the labels are unambiguous rolling windows: "Last 24h" /
// "Last 7d" / "All-time" (a calendar-Today bucket would need the BACI-312
// local-midnight cutoff the proxy endpoint doesn't have — deferred).
export type MonitorScope = 'day' | 'week' | 'all';

// MONITOR_SCOPES is the canonical ordered list the picker renders. Monitor
// keeps its own list rather than reshaping the Shipped pill's
// Today/Last Week/Forever buckets — the labels and the rolling-vs-calendar
// semantics differ, and the Shipped buckets are load-bearing for that
// feature.
export const MONITOR_SCOPES: { id: MonitorScope; label: string; sinceDays: number }[] = [
  { id: 'day', label: 'Last 24h', sinceDays: 1 },
  { id: 'week', label: 'Last 7d', sinceDays: 7 },
  { id: 'all', label: 'All-time', sinceDays: 0 },
];

// scopeToSinceDays maps a scope id to the `sinceDays` parameter the api
// seam expects. `0` is the sentinel for "no lower bound" (All-time) — both
// the Wails binding and the HTTP twin understand it the same way. An
// unknown id falls back to All-time so a hand-edited value can't break the
// fetch.
export function scopeToSinceDays(id: MonitorScope): number {
  return MONITOR_SCOPES.find(s => s.id === id)?.sinceDays ?? 0;
}

// formatBytes renders a byte count as a short human string (KB/MB/GB, base
// 1024). One decimal place above KB; bytes render whole. Dependency-free,
// matching the formatWhen.ts precedent of a one-liner over a date library.
export function formatBytes(n: number): string {
  if (!Number.isFinite(n) || n < 0) return '—';
  if (n < 1024) return `${n} B`;
  const units = ['KB', 'MB', 'GB', 'TB'];
  let v = n / 1024;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(1)} ${units[i]}`;
}

// formatErrorRate renders an error fraction (0..1) as a whole-number
// percentage. A clean 0 renders "0%" rather than "0.0%".
export function formatErrorRate(rate: number): string {
  if (!Number.isFinite(rate) || rate < 0) return '—';
  return `${Math.round(rate * 100)}%`;
}

// formatMs renders a latency in milliseconds. Sub-second values stay in
// ms; a second or more renders as seconds with one decimal.
export function formatMs(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) return '—';
  if (ms < 1000) return `${ms} ms`;
  return `${(ms / 1000).toFixed(1)} s`;
}
