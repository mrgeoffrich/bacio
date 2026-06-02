import React, { useState, useEffect, useMemo } from 'react';
import { Link } from 'react-router';
import { reportError } from '../errors';
import * as api from '../api';
import { formatWhen } from '../lib/formatWhen';
import { transcriptPath } from '../lib/routes';

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

// summed token total for a row's usage — input + output is the at-a-glance
// number the list shows (cache / thinking detail lives on the full transcript).
function totalTokens(usage) {
  if (!usage) return 0;
  return (usage.inputTokens || 0) + (usage.outputTokens || 0);
}

// TranscriptListPanel is the Transcripts sub-tab of the Monitor screen
// (BACI-322) — a first-class browser over the per-dispatch transcripts captured
// through the reverse proxy. It lists one row per dispatch that has parsed
// captures (api.listJobTranscripts), scoped to the active repo, with a live
// issue-substring filter and a job-mode <select>; each row links to the
// deep-linkable full-transcript route. The list fetches on mount + mode change
// and silently re-polls every 10s (the Network panel's cadence).
export default function TranscriptListPanel({ activeBoard }) {
  const [rows, setRows] = useState([]);
  const [loading, setLoading] = useState(true);
  const [mode, setMode] = useState(''); // server-side mode filter
  const [issueFilter, setIssueFilter] = useState(''); // client-side substring

  // Fetch on mount / repo / mode change (with a loading flicker) and on a 10s
  // interval (silent). Re-armed whenever repo or mode changes.
  useEffect(() => {
    if (!activeBoard) {
      setRows([]);
      setLoading(false);
      return undefined;
    }
    let cancelled = false;
    const load = (silent) => {
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
        ) : (
          visible.map(r => (
            <Link
              key={r.dispatchId}
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
          ))
        )}
      </div>
    </div>
  );
}
