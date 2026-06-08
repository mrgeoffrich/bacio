import React, { useState, useMemo } from 'react';
import * as api from '../api';
import type { ProxyFQDNStat } from '../api';
import { formatWhen } from '../lib/formatWhen';
import {
  MONITOR_SCOPES,
  scopeToSinceDays,
  formatBytes,
  formatErrorRate,
  formatMs,
} from '../lib/proxyStats';
import type { MonitorScope } from '../lib/proxyStats';
import { usePolledResource } from '../lib/hooks/usePolledResource';
import MonitorCaptureSheet from './MonitorCaptureSheet';

// A sortable column over the per-FQDN stats table. `key` is the
// ProxyFQDNStat field; `sortValue` extracts the value to sort on;
// `render` formats the cell; `numeric` right-aligns and flips the default
// sort direction to descending (busiest/biggest first).
type Column = {
  key: keyof ProxyFQDNStat;
  label: string;
  numeric: boolean;
  sortValue: (s: ProxyFQDNStat) => string | number;
  render: (s: ProxyFQDNStat) => React.ReactNode;
};

// COLUMNS drives both the sortable header and the per-row cells (see the
// Column type above for what each field does).
const COLUMNS: Column[] = [
  { key: 'host', label: 'Host', numeric: false, sortValue: s => s.host, render: s => s.host },
  { key: 'requestCount', label: 'Reqs', numeric: true, sortValue: s => s.requestCount, render: s => s.requestCount.toLocaleString() },
  { key: 'bytesIn', label: 'In', numeric: true, sortValue: s => s.bytesIn, render: s => formatBytes(s.bytesIn) },
  { key: 'bytesOut', label: 'Out', numeric: true, sortValue: s => s.bytesOut, render: s => formatBytes(s.bytesOut) },
  { key: 'errorRate', label: 'Err %', numeric: true, sortValue: s => s.errorRate, render: s => formatErrorRate(s.errorRate) },
  { key: 'p50Ms', label: 'P50', numeric: true, sortValue: s => s.p50Ms, render: s => formatMs(s.p50Ms) },
  { key: 'p95Ms', label: 'P95', numeric: true, sortValue: s => s.p95Ms, render: s => formatMs(s.p95Ms) },
  { key: 'lastSeen', label: 'Last Seen', numeric: false, sortValue: s => s.lastSeen, render: s => formatWhen(s.lastSeen) },
];

// NetworkPanel is the Network sub-tab of the Monitor screen (BACI-322) — the
// raw per-FQDN reverse-proxy traffic table, lifted verbatim from the old
// single-screen MonitorView (BACI-303/304/308). It is a read-only sortable
// table over the per-FQDN rollup of proxy_requests, plus the right-docked
// capture drill-down sheet. The proxy_requests table is cross-cutting (no
// repo_id), so the stats are global — the panel takes no repo. The view
// auto-refreshes every 10s while mounted (silent — no loading flicker on the
// poll).
export default function NetworkPanel() {
  const [scope, setScope] = useState<MonitorScope>('day'); // default Last 24h
  // Default sort is busiest-first (requestCount desc) — exactly the order
  // the server already returns, so the initial render is a no-op sort.
  const [sortKey, setSortKey] = useState<keyof ProxyFQDNStat>('requestCount');
  const [sortDir, setSortDir] = useState<'asc' | 'desc'>('desc');
  // BACI-308 drill-down: the FQDN the capture sheet is open on (null = closed).
  const [selectedHost, setSelectedHost] = useState<string | null>(null);

  // Eager load on scope change with a loading flicker, then a silent 10s
  // refresh that keeps the table fresh without a flash. The hook's stale-load
  // guard replaces the old cancellation flag, and its latest-fetcher ref reads
  // the current `scope` on each poll so the timer needn't be re-armed.
  const { data: stats, loading } = usePolledResource<ProxyFQDNStat[]>(
    () => api.listProxyStats(scopeToSinceDays(scope)),
    [],
    [scope],
    { errorHeadline: "Couldn't load proxy stats" },
  );

  // Click a header to sort on it: first click on a column picks its
  // default direction (descending for numeric/biggest-first, ascending
  // for text); clicking the active column again toggles direction.
  const onSort = (col: Column) => {
    if (sortKey === col.key) {
      setSortDir(d => (d === 'asc' ? 'desc' : 'asc'));
    } else {
      setSortKey(col.key);
      setSortDir(col.numeric ? 'desc' : 'asc');
    }
  };

  const sorted = useMemo(() => {
    const col = COLUMNS.find(c => c.key === sortKey);
    if (!col) return stats;
    const rows = [...stats];
    rows.sort((a, b) => {
      const av = col.sortValue(a);
      const bv = col.sortValue(b);
      let cmp;
      if (typeof av === 'number' && typeof bv === 'number') {
        cmp = av - bv;
      } else {
        cmp = String(av).localeCompare(String(bv));
      }
      return sortDir === 'asc' ? cmp : -cmp;
    });
    return rows;
  }, [stats, sortKey, sortDir]);

  return (
    <div className="mk-monitor-panel">
      <div className="mk-monitor-panel-bar">
        <label className="mk-monitor-scope">
          <span className="mk-monitor-scope-label">Window</span>
          <select
            value={scope}
            onChange={e => setScope(e.target.value as MonitorScope)}
          >
            {MONITOR_SCOPES.map(s => (
              <option key={s.id} value={s.id}>{s.label}</option>
            ))}
          </select>
        </label>
      </div>

      <div className={`mk-monitor-split${selectedHost ? ' is-open' : ''}`}>
        <div className="mk-monitor-table">
          <div className="mk-monitor-head">
            {COLUMNS.map(col => {
              const active = sortKey === col.key;
              return (
                <button
                  key={col.key}
                  type="button"
                  className={`mk-monitor-th${col.numeric ? ' is-numeric' : ''}${active ? ' is-sorted' : ''}`}
                  onClick={() => onSort(col)}
                  aria-sort={active ? (sortDir === 'asc' ? 'ascending' : 'descending') : 'none'}
                >
                  {col.label}
                  {active && <span className="mk-monitor-caret">{sortDir === 'asc' ? '▲' : '▼'}</span>}
                </button>
              );
            })}
          </div>
          {loading ? (
            <div className="mk-monitor-empty">Loading…</div>
          ) : sorted.length === 0 ? (
            <div className="mk-monitor-empty">No proxy traffic captured.</div>
          ) : (
            sorted.map(s => (
              <button
                key={s.host}
                type="button"
                className={`mk-monitor-row${selectedHost === s.host ? ' is-selected' : ''}`}
                onClick={() => setSelectedHost(s.host)}
                aria-pressed={selectedHost === s.host}
              >
                {COLUMNS.map(col => (
                  <span
                    key={col.key}
                    className={`mk-monitor-cell${col.numeric ? ' is-numeric' : ''}${col.key === 'host' ? ' mk-mono' : ''}`}
                  >
                    {col.render(s)}
                  </span>
                ))}
              </button>
            ))
          )}
        </div>

        {selectedHost && (
          <MonitorCaptureSheet
            host={selectedHost}
            onClose={() => setSelectedHost(null)}
          />
        )}
      </div>
    </div>
  );
}
