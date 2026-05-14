import React from 'react';
import Icon from './Icon.jsx';
import { columnStatus } from '../data.js';

export default function IssueDrawer({ card, onClose, onHandToClaude, onShip }) {
  if (!card) return null;
  return (
    <>
      <div className="mk-scrim" onClick={onClose} />
      <aside className="mk-drawer" role="dialog" aria-label={`Issue ${card.id}`}>
        <header className="mk-drawer-head">
          <span className="mk-card-id">{card.id}</span>
          <span className={`mk-pill mk-status-${columnStatus(card.column)}`}>
            {columnStatus(card.column).toUpperCase()}
          </span>
          <div style={{ marginLeft: 'auto', display: 'flex', gap: '4px' }}>
            <button className="mk-icbtn" aria-label="Copy link"><Icon name="link" /></button>
            <button className="mk-icbtn" aria-label="Close" onClick={onClose}><Icon name="x" /></button>
          </div>
        </header>

        <div className="mk-drawer-body">
          <h2 className="mk-drawer-title">{card.title}</h2>

          <div className="mk-drawer-meta">
            <div className="mk-meta-row-grid">
              <span className="mk-meta-key">Assignees</span>
              <span className="mk-meta-val">
                <div className="mk-avatars">
                  {card.assignees.length === 0 && <span className="mk-meta-empty">unassigned</span>}
                  {card.assignees.map((a, i) => (
                    <span key={i} className={`mk-av ${a === 'claude' ? 'is-claude' : ''}`}>{a === 'claude' ? 'c' : a}</span>
                  ))}
                </div>
              </span>

              <span className="mk-meta-key">Tags</span>
              <span className="mk-meta-val">
                {card.tags?.map(t => <span key={t} className="mk-tag">{t}</span>) || <span className="mk-meta-empty">—</span>}
              </span>

              {card.pr && (<>
                <span className="mk-meta-key">PR</span>
                <span className="mk-meta-val mk-mono">{card.pr} · feat/board-drag</span>
              </>)}
              {card.note && (<>
                <span className="mk-meta-key">Block</span>
                <span className="mk-meta-val mk-blocked-note">{card.note}</span>
              </>)}
            </div>
          </div>

          <section className="mk-drawer-section">
            <div className="mk-drawer-label">Description</div>
            <p className="mk-drawer-text">
              On iOS Safari and Chrome Android, dragging a card during a long press introduces ~80ms of jitter.
              Likely the touchmove preventDefault timing — needs a debounce or a switch to pointer events.
            </p>
          </section>

          <section className="mk-drawer-section">
            <div className="mk-drawer-label">Activity</div>
            <ul className="mk-timeline">
              <li className="mk-tl-item">
                <span className="mk-tl-dot is-claude" />
                <span className="mk-tl-text"><b>claude</b> opened <span className="mk-mono">PR #4821</span> · 12m ago</span>
              </li>
              <li className="mk-tl-item">
                <span className="mk-tl-dot is-claude" />
                <span className="mk-tl-text"><b>claude</b> picked this up · 38m ago</span>
              </li>
              <li className="mk-tl-item">
                <span className="mk-tl-dot" />
                <span className="mk-tl-text"><b>jr</b> moved this from Todo → Doing · 1h ago</span>
              </li>
            </ul>
          </section>
        </div>

        <footer className="mk-drawer-foot">
          {card.column !== 'done' ? (
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
