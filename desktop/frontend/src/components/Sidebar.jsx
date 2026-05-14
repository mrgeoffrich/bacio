import React from 'react';
import Icon from './Icon.jsx';
import { boards } from '../data.js';

export default function Sidebar({ collapsed, onToggle, activeBoard, onPickBoard, agentRuns }) {
  return (
    <aside className={`mk-sidebar ${collapsed ? 'is-collapsed' : ''}`}>
      <div className="mk-sb-head">
        <button className="mk-sb-logo" onClick={onToggle} title="Toggle sidebar">
          <img src="/bacio-mark.png" width="24" height="24" alt="" />
          {!collapsed && <span className="mk-sb-wordmark">bacio</span>}
        </button>
      </div>

      {!collapsed && (
        <>
          <div className="mk-sb-section">
            <div className="mk-sb-label">Boards</div>
            {boards.map(b => (
              <button
                key={b.id}
                className={`mk-sb-item ${b.id === activeBoard ? 'is-active' : ''}`}
                onClick={() => onPickBoard(b.id)}
              >
                <Icon name="board" />
                <span>{b.name}</span>
                <span className="mk-sb-count">{b.count}</span>
              </button>
            ))}
            <button className="mk-sb-item mk-sb-add">
              <Icon name="plus" />
              <span>New board</span>
            </button>
          </div>

          <div className="mk-sb-section">
            <div className="mk-sb-label">Views</div>
            <button className="mk-sb-item"><Icon name="inbox" /><span>Inbox</span><span className="mk-sb-count" style={{ color: 'var(--coral-600)' }}>3</span></button>
            <button className="mk-sb-item"><Icon name="user" /><span>Mine</span></button>
            <button className="mk-sb-item"><Icon name="claude" /><span>claude is on</span><span className="mk-sb-count">{agentRuns}</span></button>
          </div>

          <div className="mk-sb-foot">
            <div className="mk-claude-card">
              <div className="mk-claude-row">
                <span className="mk-pulse-dot" />
                <span className="mk-claude-label">claude</span>
                <span className="mk-claude-count">{agentRuns} running</span>
              </div>
              <div className="mk-claude-meta">last shipped 4m ago · BAC-185</div>
            </div>
          </div>
        </>
      )}
    </aside>
  );
}
