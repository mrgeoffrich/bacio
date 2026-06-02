import React, { useEffect, useState } from 'react';
import { Link } from 'react-router';
import * as api from '../../api';
import { reportError } from '../../errors';
import { formatWhen } from '../../lib/formatWhen';
import { transcriptPath } from '../../lib/routes';

// IssueTranscriptList (BACI-339) is the rail sub-section that surfaces this
// issue's parsed agent transcripts so they're discoverable from the
// workspace without bouncing to the Monitor. It reuses the same
// api.listJobTranscripts(repo, issue) seam the Monitor's TranscriptListPanel
// uses (GET /proxy/jobs, issue-scoped server-side — BACI-322), so no new API.
//
// Unlike the Monitor panel this is a one-shot fetch on mount / issue-key
// change — no 10s poll. A transcript captured while the workspace is open is
// a post-hoc artefact; surfacing it on the next open is fine, and the rail
// shouldn't carry a live poll just for it (decision baked into the plan).
//
// Each row deep-links to the existing full-transcript route via
// transcriptPath; that viewer is out of scope to modify.
export default function IssueTranscriptList({ activeBoard, issueKey }) {
  const [rows, setRows] = useState([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    if (!activeBoard || !issueKey) {
      setRows([]);
      setLoading(false);
      return undefined;
    }
    let cancelled = false;
    setLoading(true);
    api.listJobTranscripts(activeBoard, issueKey)
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
    return () => { cancelled = true; };
  }, [activeBoard, issueKey]);

  return (
    <section className="mk-drawer-section">
      <div className="mk-drawer-label">Transcripts</div>
      {loading ? (
        <p className="mk-meta-empty">Loading…</p>
      ) : rows.length === 0 ? (
        <p className="mk-meta-empty">No transcripts captured.</p>
      ) : (
        <ul className="mk-rail-transcript-list">
          {rows.map(r => (
            <li key={r.dispatchId}>
              <Link
                to={transcriptPath(activeBoard, r.dispatchId)}
                className="mk-rail-transcript-row"
              >
                <span className="mk-rail-transcript-main">
                  {/* agentName is best-effort (empty when the dispatch was
                      deleted); fall back to the dispatch number like the
                      Monitor panel's mk-transcript-nokey. */}
                  {r.mode && <span className="mk-rail-transcript-mode">{r.mode}</span>}
                  <span className="mk-rail-transcript-agent">
                    {r.agentName || `dispatch #${r.dispatchId}`}
                  </span>
                </span>
                <span className="mk-rail-transcript-meta">
                  <span>{r.turnCount} {r.turnCount === 1 ? 'turn' : 'turns'}</span>
                  <span className="mk-rail-transcript-when">{formatWhen(r.lastSeen)}</span>
                </span>
              </Link>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
