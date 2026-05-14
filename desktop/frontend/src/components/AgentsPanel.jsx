import React, { useEffect, useState } from 'react';
import Icon from './Icon.jsx';

// relTime renders a coarse "time since" for the last-seen line.
function relTime(iso) {
  const ms = Date.now() - new Date(iso).getTime();
  const m = Math.floor(ms / 60000);
  if (m < 1) return 'just now';
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  return `${Math.floor(h / 24)}d ago`;
}

// AgentsPanel is the desktop Agents screen: a card per agent session
// connected to the current repo, click-to-expand into its open claims
// and dispatches. Read-only — agents are dispatched work from the issue
// drawer, not from here.
export default function AgentsPanel({ open, agents, onRefresh, onClose }) {
  const [expanded, setExpanded] = useState(null);

  // Refresh whenever the panel is opened so counts are current.
  useEffect(() => {
    if (open) onRefresh();
  }, [open]);

  if (!open) return null;

  return (
    <>
      <div className="mk-scrim" onClick={onClose} />
      <div className="mk-settings mk-agents" role="dialog" aria-label="Agents">
        <header className="mk-settings-head">
          <h2 className="mk-settings-title">Agents</h2>
          <button className="mk-icbtn" aria-label="Refresh" onClick={onRefresh}>
            <Icon name="board" />
          </button>
          <button className="mk-icbtn" aria-label="Close" onClick={onClose}>
            <Icon name="x" />
          </button>
        </header>

        <div className="mk-settings-body">
          {agents.length === 0 && (
            <p className="mk-drawer-text mk-meta-empty">No agent sessions for this repo.</p>
          )}

          {agents.map((a) => {
            const name = a.agentName || a.sessionId.slice(0, 12);
            const isOpen = expanded === a.sessionId;
            return (
              <div key={a.sessionId} className={`mk-agent-card ${isOpen ? 'is-open' : ''}`}>
                <button
                  className="mk-agent-head"
                  onClick={() => setExpanded(isOpen ? null : a.sessionId)}
                >
                  <span className="mk-agent-name">{name}</span>
                  <span className={`mk-pill mk-status-${a.status}`}>{a.status}</span>
                  <span className="mk-agent-meta">
                    {a.model || '—'} · {a.branch || '—'} · seen {relTime(a.lastSeenAt)}
                  </span>
                  <span className="mk-agent-counts">
                    {a.claims.length} claim{a.claims.length === 1 ? '' : 's'} ·{' '}
                    {a.dispatches.length} dispatch{a.dispatches.length === 1 ? '' : 'es'}
                  </span>
                </button>

                {isOpen && (
                  <div className="mk-agent-detail">
                    <div className="mk-agent-detail-label">Open claims</div>
                    {a.claims.length === 0 ? (
                      <div className="mk-meta-empty">none</div>
                    ) : (
                      a.claims.map((c) => (
                        <div key={c.issueKey} className="mk-mono">{c.issueKey}</div>
                      ))
                    )}

                    <div className="mk-agent-detail-label">Dispatches</div>
                    {a.dispatches.length === 0 ? (
                      <div className="mk-meta-empty">none</div>
                    ) : (
                      a.dispatches.map((d) => (
                        <div key={d.id} className="mk-agent-dispatch">
                          <span className="mk-mono">#{d.id}</span>
                          <span className="mk-pill">{d.status}</span>
                          {d.mode && <span className="mk-tag">{d.mode}</span>}
                          <span className="mk-mono">{d.issueKey || '—'}</span>
                        </div>
                      ))
                    )}
                  </div>
                )}
              </div>
            );
          })}
        </div>
      </div>
    </>
  );
}
