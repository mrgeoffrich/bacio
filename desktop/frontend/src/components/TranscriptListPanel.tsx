import { useState, useEffect, useMemo } from 'react';
import { Link } from 'react-router';
import { reportError } from '../errors';
import * as api from '../api';
import type { JobTranscriptRow } from '../api';
import { formatWhen } from '../lib/formatWhen';
import { transcriptPath } from '../lib/routes';
import { groupTranscripts } from '../lib/transcriptGroups';
import type { GroupByMode, TranscriptGroup } from '../lib/transcriptGroups';

// Silent-refresh cadence — matches the Network panel's 10s poll.
const POLL_INTERVAL_MS = 10_000;

// MODE_OPTIONS is the job-mode <select> the list filters on. The empty value is
// "all modes"; the rest are the dispatch modes a worker runs under. Server-side
// filtering narrows the fetch, so changing the mode re-fetches.
const MODE_OPTIONS = [
  { value: '', label: 'All modes' },
  { value: 'plan', label: 'plan' },
  { value: 'implement', label: 'implement' },
  { value: 'review', label: 'review' },
  { value: 'ship', label: 'ship' },
  { value: 'design', label: 'design' },
  { value: 'fix_review', label: 'fix_review' },
];

// GROUP_BY_OPTIONS drive the "Group by" segmented control (BACI-348). Dispatch
// is today's flat list; Session folds a sitting's dispatch chain under its
// supervisor session; Agent folds one subagent's runs (finer than dispatch).
const GROUP_BY_OPTIONS: { value: GroupByMode; label: string }[] = [
  { value: 'dispatch', label: 'Dispatch' },
  { value: 'session', label: 'Session' },
  { value: 'agent', label: 'Agent' },
];

// summed token total for a row's usage — input + output is the at-a-glance
// number the list shows (cache / thinking detail lives on the full transcript).
function totalTokens(usage: JobTranscriptRow['usage'] | undefined): number {
  if (!usage) return 0;
  return (usage.inputTokens || 0) + (usage.outputTokens || 0);
}

// TranscriptRow is the per-dispatch list row — shared by the flat list and the
// nested rows under a collapsible group so the two stay visually identical.
function TranscriptRow({ activeBoard, r }: { activeBoard: string; r: JobTranscriptRow }) {
  return (
    <Link
      to={transcriptPath(activeBoard, r.dispatchId)}
      className="mk-transcript-row"
    >
      <span className="mk-transcript-row-main">
        {r.issueKey ? (
          <span className="mk-capture-chip">
            {r.issueKey}{r.mode ? ` · ${r.mode}` : ''}
          </span>
        ) : (
          <span className="mk-transcript-nokey">dispatch #{r.dispatchId}{r.mode ? ` · ${r.mode}` : ''}</span>
        )}
        {r.model && <code className="mk-transcript-model">{r.model}</code>}
      </span>
      <span className="mk-transcript-row-meta">
        <span className="mk-transcript-turns">{r.turnCount} {r.turnCount === 1 ? 'turn' : 'turns'}</span>
        <span className="mk-transcript-tokens">{totalTokens(r.usage).toLocaleString()} tok</span>
        <span className="mk-capture-when">{formatWhen(r.lastSeen)}</span>
      </span>
    </Link>
  );
}

// TranscriptListPanel is the Transcripts sub-tab of the Monitor screen
// (BACI-322) — a first-class browser over the per-dispatch transcripts captured
// through the reverse proxy. It lists one row per dispatch that has parsed
// captures (api.listJobTranscripts), scoped to the active repo, with a live
// issue-substring filter and a job-mode <select>; each row links to the
// deep-linkable full-transcript route. BACI-348 adds a "Group by" control that
// folds the same flat rows into collapsible groups by supervisor session or
// subagent identity (a pure client re-fold, no re-fetch). The list fetches on
// mount + mode change and silently re-polls every 10s (the Network panel's
// cadence).
type TranscriptListPanelProps = {
  activeBoard: string;
};

export default function TranscriptListPanel({ activeBoard }: TranscriptListPanelProps) {
  const [rows, setRows] = useState<JobTranscriptRow[]>([]);
  const [loading, setLoading] = useState(true);
  const [mode, setMode] = useState(''); // server-side mode filter
  const [issueFilter, setIssueFilter] = useState(''); // client-side substring
  const [groupBy, setGroupBy] = useState<GroupByMode>('dispatch'); // client-side fold
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set()); // collapsed group keys

  // Fetch on mount / repo / mode change (with a loading flicker) and on a 10s
  // interval (silent). Re-armed whenever repo or mode changes.
  useEffect(() => {
    if (!activeBoard) {
      setRows([]);
      setLoading(false);
      return undefined;
    }
    let cancelled = false;
    const load = (silent: boolean) => {
      if (!silent) setLoading(true);
      api.listJobTranscripts(activeBoard, '', mode)
        .then(list => {
          if (cancelled) return;
          setRows(list);
          setLoading(false);
        })
        .catch(err => {
          if (cancelled) return;
          reportError(err, { headline: "Couldn't load transcripts" });
          setLoading(false);
        });
    };
    load(false);
    const id = setInterval(() => load(true), POLL_INTERVAL_MS);
    return () => { cancelled = true; clearInterval(id); };
  }, [activeBoard, mode]);

  // Issue-substring filter is client-side and live (no debounce) — it narrows
  // the already-fetched rows by a case-insensitive match on the issue key.
  const visible = useMemo(() => {
    const needle = issueFilter.trim().toLowerCase();
    if (!needle) return rows;
    return rows.filter(r => (r.issueKey || '').toLowerCase().includes(needle));
  }, [rows, issueFilter]);

  // The fold is a pure re-derivation of the visible rows — instant on a
  // group-by toggle, no re-fetch. Empty for 'dispatch' (rendered flat).
  const groups = useMemo<TranscriptGroup[]>(
    () => groupTranscripts(visible, groupBy),
    [visible, groupBy],
  );

  const toggleGroup = (key: string) => {
    setCollapsed(prev => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  };

  if (!activeBoard) {
    return (
      <div className="mk-monitor-panel">
        <div className="mk-monitor-empty">Select a repository to view its transcripts.</div>
      </div>
    );
  }

  return (
    <div className="mk-monitor-panel">
      <div className="mk-monitor-panel-bar">
        <input
          type="search"
          className="mk-transcript-filter"
          placeholder="Filter by issue (e.g. BACI-302)"
          value={issueFilter}
          onChange={e => setIssueFilter(e.target.value)}
          aria-label="Filter transcripts by issue"
        />
        <div className="mk-group-by" role="tablist" aria-label="Group transcripts by">
          <span className="mk-group-by-label">Group by</span>
          {GROUP_BY_OPTIONS.map(o => (
            <button
              key={o.value}
              type="button"
              role="tab"
              aria-selected={groupBy === o.value}
              className={`mk-group-by-tab${groupBy === o.value ? ' is-active' : ''}`}
              onClick={() => setGroupBy(o.value)}
            >
              {o.label}
            </button>
          ))}
        </div>
        <label className="mk-monitor-scope">
          <span className="mk-monitor-scope-label">Mode</span>
          <select value={mode} onChange={e => setMode(e.target.value)}>
            {MODE_OPTIONS.map(o => (
              <option key={o.value} value={o.value}>{o.label}</option>
            ))}
          </select>
        </label>
      </div>

      <div className="mk-transcript-list">
        {loading ? (
          <div className="mk-monitor-empty">Loading…</div>
        ) : visible.length === 0 ? (
          <div className="mk-monitor-empty">No transcripts captured.</div>
        ) : groupBy === 'dispatch' ? (
          visible.map(r => <TranscriptRow key={r.dispatchId} activeBoard={activeBoard} r={r} />)
        ) : (
          groups.map(g => {
            const isCollapsed = collapsed.has(g.key);
            return (
              <div key={g.key} className="mk-transcript-group">
                <button
                  type="button"
                  className="mk-transcript-group-head"
                  aria-expanded={!isCollapsed}
                  onClick={() => toggleGroup(g.key)}
                >
                  <span className="mk-transcript-group-arrow">{isCollapsed ? '▸' : '▾'}</span>
                  <span className="mk-transcript-group-label">{g.label}</span>
                  <span className="mk-transcript-group-meta">
                    <span>{g.dispatchCount} {g.dispatchCount === 1 ? 'dispatch' : 'dispatches'}</span>
                    <span>{g.turnSum} {g.turnSum === 1 ? 'turn' : 'turns'}</span>
                    <span className="mk-transcript-tokens">{g.tokenSum.toLocaleString()} tok</span>
                    <span className="mk-capture-when">{formatWhen(g.lastSeen)}</span>
                  </span>
                </button>
                {!isCollapsed && (
                  <div className="mk-transcript-group-body">
                    {g.rows.map(r => <TranscriptRow key={r.dispatchId} activeBoard={activeBoard} r={r} />)}
                  </div>
                )}
              </div>
            );
          })
        )}
      </div>

      {/* BACI-348: v1 windowing caveat — the fold only stitches dispatches in
          the fetched window. */}
      {groupBy !== 'dispatch' && !loading && visible.length > 0 && (
        <div className="mk-transcript-caveat">
          Grouping the most recent transcripts — a session whose dispatches fall
          outside the window won&apos;t fully stitch. Widen with <code>--since</code> from the CLI.
        </div>
      )}
    </div>
  );
}
