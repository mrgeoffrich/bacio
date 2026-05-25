import React, { useState, useEffect } from 'react';
import { reportError } from '../errors';
import * as api from '../api';
import { formatWhen } from '../lib/formatWhen';

const PAGE_SIZE = 50;

// HistoryView is the desktop audit-log browser — the paged mirror of the TUI's
// History tab. It shows the repo's history newest-first, one fixed-size page
// at a time, with Prev/Next paging; it never loads the whole log at once.
export default function HistoryView({ activeBoard }) {
  const [page, setPage] = useState(0);
  const [data, setData] = useState(null); // HistoryPage
  const [loading, setLoading] = useState(false);

  const repoSelected = !!activeBoard;

  // Reset to the first page whenever the repo changes.
  useEffect(() => { setPage(0); }, [activeBoard]);

  // Load the current page.
  useEffect(() => {
    if (!repoSelected) {
      setData(null);
      return;
    }
    setLoading(true);
    api.listHistory(activeBoard, page, PAGE_SIZE)
      .then(p => {
        setData(p);
        setLoading(false);
      })
      .catch(err => {
        reportError(err, { headline: "Couldn't load history" });
        setLoading(false);
      });
  }, [activeBoard, page, repoSelected]);

  if (!repoSelected) {
    return (
      <div className="mk-history">
        <div className="mk-history-empty">Select a repository to view its history.</div>
      </div>
    );
  }

  const entries = data?.entries ?? [];
  const hasMore = data?.hasMore ?? false;

  return (
    <div className="mk-history">
      <header className="mk-history-bar">
        <h2 className="mk-history-title">History</h2>
        <div className="mk-history-pager">
          <button
            className="mk-btn-secondary"
            disabled={page === 0 || loading}
            onClick={() => setPage(p => Math.max(0, p - 1))}
          >
            Prev
          </button>
          <span className="mk-history-page">Page {page + 1}</span>
          <button
            className="mk-btn-secondary"
            disabled={!hasMore || loading}
            onClick={() => setPage(p => p + 1)}
          >
            Next
          </button>
        </div>
      </header>

      <div className="mk-history-table">
        <div className="mk-history-head">
          <span>When</span>
          <span>Actor</span>
          <span>Op</span>
          <span>Target</span>
          <span>Details</span>
        </div>
        {loading ? (
          <div className="mk-history-empty">Loading…</div>
        ) : entries.length === 0 ? (
          <div className="mk-history-empty">No history on this page.</div>
        ) : (
          entries.map(e => (
            <div key={e.id} className="mk-history-row">
              <span className="mk-history-when">{formatWhen(e.createdAt)}</span>
              <span className="mk-history-actor">{e.actor}</span>
              <span className="mk-history-op">{e.op}</span>
              <span className="mk-history-target mk-mono">
                {e.targetLabel ? `[${e.targetLabel}]` : '—'}
              </span>
              <span className="mk-history-details">{e.details || '—'}</span>
            </div>
          ))
        )}
      </div>
    </div>
  );
}
