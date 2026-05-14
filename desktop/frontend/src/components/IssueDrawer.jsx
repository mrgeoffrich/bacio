import React, { useState, useEffect } from 'react';
import ReactMarkdown from 'react-markdown';
import Icon from './Icon.jsx';

// prLabel shortens a GitHub PR URL to "owner/repo#N" when it matches the
// familiar shape; anything else falls back to the raw URL.
function prLabel(url) {
  const m = url.match(/github\.com\/([^/]+)\/([^/]+)\/pull\/(\d+)/);
  return m ? `${m[1]}/${m[2]}#${m[3]}` : url;
}

export default function IssueDrawer({ issue, agents, onClose, onSendToAgent, onShip, onEdit }) {
  // Only agents with a persistent identity slug can be targeted from the
  // desktop (DispatchIssue routes by slug); ended sessions are dropped.
  // Busy agents stay in the list but render disabled with a reason, so
  // the user sees *why* they can't be picked rather than them vanishing.
  const dispatchable = (agents || []).filter(a => a.status !== 'ended' && a.agentName);
  const available = dispatchable.filter(a => !a.busy);
  const [selectedAgent, setSelectedAgent] = useState('');
  const [note, setNote] = useState('');

  // Reset the send-to-agent form whenever the drawer opens on a new issue.
  useEffect(() => {
    setSelectedAgent('');
    setNote('');
  }, [issue?.key]);

  if (!issue) return null;

  const isTodo = issue.column === 'todo';
  // Fall back to the first *available* (non-busy) agent so the form is
  // usable even before the user touches the select (agents may load
  // after open). A busy agent is never auto-selected.
  const effectiveAgent = selectedAgent || available[0]?.agentName || '';

  const send = (mode) => {
    if (!effectiveAgent) return;
    onSendToAgent(effectiveAgent, mode, note);
  };

  const hasAttachments = issue.pullRequests.length > 0 || issue.documents.length > 0;

  return (
    <>
      <div className="mk-scrim" onClick={onClose} />
      <aside className="mk-drawer" role="dialog" aria-label={`Issue ${issue.key}`}>
        <header className="mk-drawer-head">
          <span className="mk-card-id">{issue.key}</span>
          <span className={`mk-pill mk-status-${issue.column}`}>{issue.columnLabel}</span>
          <div style={{ marginLeft: 'auto', display: 'flex', gap: '4px', alignItems: 'center' }}>
            <button className="mk-btn-secondary" onClick={onEdit}>Edit</button>
            <button className="mk-icbtn" aria-label="Close" onClick={onClose}><Icon name="x" /></button>
          </div>
        </header>

        <div className="mk-drawer-body">
          <h2 className="mk-drawer-title">{issue.title}</h2>

          <div className="mk-drawer-meta">
            <div className="mk-meta-row-grid">
              <span className="mk-meta-key">Assignees</span>
              <span className="mk-meta-val">
                {issue.assignees.length === 0
                  ? <span className="mk-meta-empty">unassigned</span>
                  : issue.assignees.join(', ')}
              </span>

              <span className="mk-meta-key">Tags</span>
              <span className="mk-meta-val">
                {issue.tags.length > 0
                  ? issue.tags.map(t => <span key={t} className="mk-tag">{t}</span>)
                  : <span className="mk-meta-empty">—</span>}
              </span>
            </div>
          </div>

          <section className="mk-drawer-section">
            <div className="mk-drawer-label">Description</div>
            {issue.description
              ? <div className="mk-drawer-text mk-markdown"><ReactMarkdown>{issue.description}</ReactMarkdown></div>
              : <p className="mk-drawer-text mk-meta-empty">No description.</p>}
          </section>

          {hasAttachments && (
            <section className="mk-drawer-section">
              <div className="mk-drawer-label">Attachments</div>
              <ul className="mk-attachments">
                {issue.pullRequests.map(p => (
                  <li key={p.url} className="mk-attachment">
                    <span className="mk-attachment-badge">PR</span>
                    <a href={p.url} target="_blank" rel="noreferrer" className="mk-attachment-link">
                      {prLabel(p.url)}
                    </a>
                  </li>
                ))}
                {issue.documents.map(d => (
                  <li key={d.filename} className="mk-attachment">
                    <span className="mk-attachment-badge">{d.type || 'doc'}</span>
                    <span className="mk-attachment-name">{d.filename}</span>
                    {d.description && <span className="mk-attachment-why">{d.description}</span>}
                  </li>
                ))}
              </ul>
            </section>
          )}

          {isTodo && (
            <section className="mk-drawer-section">
              <div className="mk-drawer-label">Send to agent</div>
              {dispatchable.length === 0 ? (
                <p className="mk-drawer-text mk-meta-empty">No agents online for this repo.</p>
              ) : available.length === 0 ? (
                <p className="mk-drawer-text mk-meta-empty">
                  No available agents — all online agents are busy.
                </p>
              ) : (
                <div className="mk-send-agent">
                  <select
                    className="mk-send-agent-select"
                    value={effectiveAgent}
                    onChange={(e) => setSelectedAgent(e.target.value)}
                  >
                    {dispatchable.map(a => (
                      <option key={a.sessionId} value={a.agentName} disabled={a.busy}>
                        {a.busy
                          ? `${a.agentName} · busy (${a.busyIssue})`
                          : `${a.agentName} · ${a.status}`}
                      </option>
                    ))}
                  </select>
                  <textarea
                    className="mk-send-agent-note"
                    placeholder="Optional note — the agent already gets the issue and the mode instruction."
                    rows={2}
                    value={note}
                    onChange={(e) => setNote(e.target.value)}
                  />
                  <div className="mk-send-agent-actions">
                    <button className="mk-btn-secondary" onClick={() => send('plan')}>Send (Plan)</button>
                    <button className="mk-btn-primary" onClick={() => send('implement')}>
                      <Icon name="claude" /> Send (Implement)
                    </button>
                  </div>
                </div>
              )}
            </section>
          )}

          {(issue.claimants || []).length > 0 && (
            <section className="mk-drawer-section">
              <div className="mk-drawer-label">
                Claimed by {issue.taken && <span className="mk-pill mk-status-busy">taken</span>}
              </div>
              <ul className="mk-claimant-list">
                {issue.claimants.map((c, i) => (
                  <li key={i} className={`mk-claimant ${c.open ? '' : 'is-released'}`}>
                    <span className="mk-claimant-who">
                      {c.agentName || c.sessionId.slice(0, 12)}
                      <span className="mk-claimant-state">{c.open ? 'open' : 'released'}</span>
                    </span>
                    {c.prompt && <span className="mk-claimant-prompt">{c.prompt}</span>}
                  </li>
                ))}
              </ul>
            </section>
          )}

          <section className="mk-drawer-section">
            <div className="mk-drawer-label">Activity</div>
            {issue.comments.length > 0 ? (
              <ul className="mk-timeline">
                {issue.comments.map((c, i) => (
                  <li key={i} className="mk-tl-item">
                    <span className={`mk-tl-dot ${c.author === 'claude' ? 'is-claude' : ''}`} />
                    <span className="mk-tl-text"><b>{c.author}</b> {c.body}</span>
                  </li>
                ))}
              </ul>
            ) : (
              <p className="mk-drawer-text mk-meta-empty">No comments yet.</p>
            )}
          </section>
        </div>

        <footer className="mk-drawer-foot">
          {issue.column !== 'done' ? (
            <button className="mk-btn-secondary" onClick={onShip}>Ship it</button>
          ) : (
            <button className="mk-btn-secondary" onClick={onClose}>Close</button>
          )}
        </footer>
      </aside>
    </>
  );
}
