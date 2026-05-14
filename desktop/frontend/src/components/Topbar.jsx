import React from 'react';
import Icon from './Icon.jsx';

export default function Topbar({ boards, activeBoard, onPickBoard, onOpenPalette, onOpenAgents, onOpenSettings }) {
  return (
    <header className="mk-topbar">
      <div className="mk-brand">
        <img src="/bacio-mark.png" width="22" height="22" alt="" />
        <span className="mk-brand-name">bacio</span>
      </div>

      <div className="mk-repo-select">
        <Icon name="board" />
        <select value={activeBoard} onChange={(e) => onPickBoard(e.target.value)}>
          <option value="all">All repositories</option>
          {boards.map(b => (
            <option key={b.prefix} value={b.prefix}>{b.name}</option>
          ))}
        </select>
      </div>

      <button className="mk-search" onClick={onOpenPalette}>
        <Icon name="search" />
        <span className="mk-search-text">Search issues, branches, prs</span>
        <span className="mk-kbd">⌘ K</span>
      </button>

      <div className="mk-topbar-right">
        <button className="mk-icbtn" aria-label="Agents" onClick={onOpenAgents}><Icon name="claude" /></button>
        <button className="mk-icbtn" aria-label="Notifications"><Icon name="bell" /></button>
        <button className="mk-icbtn" aria-label="Settings" onClick={onOpenSettings}><Icon name="settings" /></button>
      </div>
    </header>
  );
}
