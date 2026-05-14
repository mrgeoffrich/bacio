import React from 'react';
import Icon from './Icon.jsx';

export default function IssueDrawer({ issue, onClose, onHandToClaude, onShip }) {
  if (!issue) return null;
  return (
    <>
      <div className="mk-scrim" onClick={onClose} />
      <aside className="mk-drawer" role="dialog" aria-label={`Issue ${issue.key}`}>
        <header className="mk-drawer-head">
          <span className="mk-card-id">{issue.key}</span>
          <span className={`mk-pill mk-status-${issue.column}`}>{issue.columnLabel}</span>
          <div style={{ marginLeft: 'auto', display: 'flex', gap: '4px' }}>
            <button className="mk-icbtn" aria-label="Copy link"><Icon name="link" /></button>
            <button className="mk-icbtn" aria-label="Close" onClick={onClose}><Icon name="x" /></button>
          </div>
        </header>

        <div className="mk-drawer-body">
          <h2 className="mk-drawer-title">{issue.title}</h2>

          <div className="mk-drawer-meta">
            <div className="mk-meta-row-grid">
              <span className="mk-meta-key">Assignees</span>
              <span className="mk-meta-val">
                <div className="mk-avatars">
                  {issue.assignees.length === 0 && <span className="mk-meta-empty">unassigned</span>}
                  {issue.assignees.map((a, i) => (
                    <span key={i} className={`mk-av ${a === 'claude' ? 'is-claude' : ''}`}>{a === 'claude' ? 'c' : a}</span>
                  ))}
                </div>
              </span>

              <span className="mk-meta-key">Tags</span>
              <span className="mk-meta-val">
                {issue.tags.length > 0
                  ? issue.tags.map(t => <span key={t} className="mk-tag">{t}</span>)
                  : <span className="mk-meta-empty">—</span>}
              </span>

              {issue.pullRequests.length > 0 && (<>
                <span className="mk-meta-key">PRs</span>
                <span className="mk-meta-val mk-mono">
                  {issue.pullRequests.map(p => p.url).join(', ')}
                </span>
              </>)}
            </div>
          </div>

          <section className="mk-drawer-section">
            <div className="mk-drawer-label">Description</div>
            {issue.description
              ? <p className="mk-drawer-text">{issue.description}</p>
              : <p className="mk-drawer-text mk-meta-empty">No description.</p>}
          </section>

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
            <>
              <button className="mk-btn-primary" onClick={onHandToClaude}>
                <Icon name="claude" /> Hand to claude
              </button>
              <button className="mk-btn-secondary" onClick={onShip}>Ship it</button>
            </>
          ) : (
            <button className="mk-btn-secondary" onClick={onClose}>Close</button>
          )}
        </footer>
      </aside>
    </>
  );
}
