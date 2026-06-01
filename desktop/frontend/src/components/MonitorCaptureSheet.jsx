import React, { useState, useEffect } from 'react';
import { reportError } from '../errors';
import * as api from '../api';
import { formatWhen } from '../lib/formatWhen';
import { formatBytes, formatMs } from '../lib/proxyStats';
import JobTranscriptBody from '../lib/transcript/JobTranscriptBody';

// MonitorCaptureSheet is the BACI-308 right-docked drill-down beside the
// MonitorView FQDN table (Option A — master-detail side sheet). It walks three
// body states for the selected host:
//
//   list      → the host's captures, newest-first (api.listProxyCaptures),
//               each row carrying its dispatch's issue-key + mode chip.
//   capture   → one capture's detail: a parsed summary for an Anthropic turn
//               (api.anthropicCapture) plus the raw .http body
//               (api.getProxyCaptureRaw). A capture that isn't a parseable
//               Anthropic turn (a count_tokens probe, an error, a non-Anthropic
//               host) shows the raw body alone — a 404 on the parsed read is
//               "no parsed detail", not an error toast.
//   transcript → the whole dispatch's assembled transcript
//               (api.jobTranscript), adapted into the reused <TranscriptView>.
//
// The sheet is a flex sibling of the table (the table shrinks left, same
// docked-panel idiom as the rest of the app), not a portal/modal. The caller
// owns selectedHost and the open/close; the sheet owns its inner navigation.
export default function MonitorCaptureSheet({ host, onClose }) {
  // Inner navigation: null = the host's capture list; a capture row = its
  // detail; { dispatchId } = the job transcript for that capture's dispatch.
  const [selectedCapture, setSelectedCapture] = useState(null);
  const [transcriptFor, setTranscriptFor] = useState(null); // dispatchId | null

  // Reset the inner navigation whenever the host changes — a fresh host
  // always opens on its capture list.
  useEffect(() => {
    setSelectedCapture(null);
    setTranscriptFor(null);
  }, [host]);

  const goBackToList = () => {
    setSelectedCapture(null);
    setTranscriptFor(null);
  };

  return (
    <aside className="mk-monitor-sheet" aria-label={`Captures for ${host}`}>
      <header className="mk-monitor-sheet-head">
        <div className="mk-monitor-sheet-crumbs">
          <button type="button" className="mk-monitor-crumb" onClick={goBackToList}>
            {host}
          </button>
          {selectedCapture && !transcriptFor && (
            <span className="mk-monitor-crumb is-current"> / capture #{selectedCapture.id}</span>
          )}
          {transcriptFor && (
            <span className="mk-monitor-crumb is-current"> / job transcript</span>
          )}
        </div>
        <button
          type="button"
          className="mk-monitor-sheet-close"
          onClick={onClose}
          aria-label="Close capture detail"
        >
          ✕
        </button>
      </header>

      <div className="mk-monitor-sheet-body">
        {transcriptFor ? (
          <JobTranscriptBody dispatchId={transcriptFor} />
        ) : selectedCapture ? (
          <CaptureDetailBody
            capture={selectedCapture}
            onViewTranscript={
              selectedCapture.dispatchId
                ? () => setTranscriptFor(selectedCapture.dispatchId)
                : null
            }
          />
        ) : (
          <CaptureListBody host={host} onSelect={setSelectedCapture} />
        )}
      </div>
    </aside>
  );
}

// CaptureListBody fetches and renders the host's captures, newest-first. Each
// row is clickable into its detail; the issue-key + mode chip (borrowed from
// the design's Option D) gives the job context at a glance.
function CaptureListBody({ host, onSelect }) {
  const [rows, setRows] = useState([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    api.listProxyCaptures(host)
      .then(list => {
        if (cancelled) return;
        setRows(list);
        setLoading(false);
      })
      .catch(err => {
        if (cancelled) return;
        reportError(err, { headline: "Couldn't load captures" });
        setLoading(false);
      });
    return () => { cancelled = true; };
  }, [host]);

  if (loading) return <div className="mk-monitor-empty">Loading…</div>;
  if (rows.length === 0) {
    return <div className="mk-monitor-empty">No captures for this host.</div>;
  }
  return (
    <div className="mk-capture-list">
      <div className="mk-capture-list-note">Showing latest {rows.length}</div>
      {rows.map(r => (
        <button
          key={r.id}
          type="button"
          className="mk-capture-row"
          onClick={() => onSelect(r)}
        >
          <span className="mk-capture-row-main">
            <span className={`mk-capture-status${r.status >= 400 || r.status === 0 ? ' is-error' : ''}`}>
              {r.status || '—'}
            </span>
            <code className="mk-capture-path">{r.method} {r.path}</code>
          </span>
          <span className="mk-capture-row-meta">
            {r.issueKey && (
              <span className="mk-capture-chip">
                {r.issueKey}{r.mode ? ` · ${r.mode}` : ''}
              </span>
            )}
            {r.isAnthropic && <span className="mk-capture-tag">anthropic</span>}
            <span className="mk-capture-bytes">{formatBytes(r.bytesOut)}</span>
            <span className="mk-capture-when">{formatWhen(r.startedAt)}</span>
          </span>
        </button>
      ))}
    </div>
  );
}

// CaptureDetailBody shows one capture: a parsed summary strip for an Anthropic
// turn (best-effort — a 404 means "not a parsed turn", shown as a banner, not
// an error) plus the raw .http body. The "View job transcript" affordance is
// hidden when the capture has no dispatch.
function CaptureDetailBody({ capture, onViewTranscript }) {
  const [raw, setRaw] = useState(null);
  const [rawState, setRawState] = useState('loading'); // 'loading' | 'ready' | 'missing'
  const [parsed, setParsed] = useState(null);
  const [parsedState, setParsedState] = useState(
    capture.isAnthropic ? 'loading' : 'none',
  );

  // Raw .http body (the always-available view; cancels on re-select).
  useEffect(() => {
    let cancelled = false;
    setRawState('loading');
    setRaw(null);
    api.getProxyCaptureRaw(capture.id)
      .then(text => {
        if (cancelled) return;
        setRaw(text);
        setRawState('ready');
      })
      .catch(() => {
        // A raw 404 (no file / pruned) is "raw unavailable", not a transport
        // error — show the banner rather than a toast.
        if (cancelled) return;
        setRawState('missing');
      });
    return () => { cancelled = true; };
  }, [capture.id]);

  // Parsed summary — only for an Anthropic turn. A 404 means the capture
  // wasn't parseable (a count_tokens probe / error); fall back silently.
  useEffect(() => {
    if (!capture.isAnthropic) { setParsedState('none'); return undefined; }
    let cancelled = false;
    setParsedState('loading');
    setParsed(null);
    api.anthropicCapture(capture.id)
      .then(msg => {
        if (cancelled) return;
        setParsed(msg);
        setParsedState('ready');
      })
      .catch(() => {
        if (cancelled) return;
        setParsedState('none');
      });
    return () => { cancelled = true; };
  }, [capture.id, capture.isAnthropic]);

  return (
    <div className="mk-capture-detail">
      <div className="mk-capture-detail-meta">
        <code className="mk-capture-path">{capture.method} {capture.path}</code>
        <span className="mk-capture-detail-facts">
          {capture.status || '—'} · {formatBytes(capture.bytesOut)} out · {formatMs(capture.durationMs)}
        </span>
        {capture.issueKey && (
          <span className="mk-capture-chip">
            {capture.issueKey}{capture.mode ? ` · ${capture.mode}` : ''}
          </span>
        )}
        {onViewTranscript && (
          <button type="button" className="mk-btn-secondary" onClick={onViewTranscript}>
            View job transcript
          </button>
        )}
      </div>

      {parsedState === 'ready' && parsed && (
        <div className="mk-capture-summary">
          <span>{parsed.model || 'unknown model'}</span>
          {parsed.stop_reason && <span>stop: {parsed.stop_reason}</span>}
          <span>
            {parsed.usage?.input_tokens ?? 0} in / {parsed.usage?.output_tokens ?? 0} out tokens
          </span>
        </div>
      )}
      {parsedState === 'none' && capture.isAnthropic && (
        <div className="mk-capture-banner">No parsed turn for this capture — showing the raw exchange.</div>
      )}
      {!capture.isAnthropic && (
        <div className="mk-capture-banner">Not an Anthropic message turn — showing the raw exchange.</div>
      )}

      {rawState === 'loading' && <div className="mk-monitor-empty">Loading raw capture…</div>}
      {rawState === 'missing' && (
        <div className="mk-capture-banner">Raw capture unavailable (no file or pruned).</div>
      )}
      {rawState === 'ready' && (
        <pre className="mk-capture-raw">{raw}</pre>
      )}
    </div>
  );
}

