import React from 'react';
import Icon from './Icon.jsx';
import RepoPicker from './RepoPicker.jsx';

// NAV is the ordered top-nav. Exported so App can map the 1–5 digit
// hotkeys onto the same views in the same order.
export const NAV = [
  { view: 'board', label: 'Issues' },
  { view: 'features', label: 'Features' },
  { view: 'docs', label: 'Documents' },
  { view: 'agents', label: 'Agents' },
  { view: 'history', label: 'History' },
];

export default function Topbar({ boards, activeBoard, onPickBoard, onAddRepository, activeView, onChangeView, onOpenPalette, onOpenSettings, leaderState }) {
  const syncEnabled = !!boards.find(b => b.prefix === activeBoard)?.syncEnabled;
  const isLeader = leaderState?.amLeader ?? false;
  return (
    <header className="mk-topbar">
      <div className="mk-brand">
        <img src="/bacio-mark.png" width="22" height="22" alt="" />
        <span className="mk-brand-name">bacio</span>
      </div>

      <div className="mk-segmented">
        {NAV.map(({ view, label }) => (
          <button
            key={view}
            className={`mk-segmented-btn ${activeView === view ? 'is-active' : ''}`}
            onClick={() => onChangeView(view)}
          >
            {label}
          </button>
        ))}
      </div>

      <button className="mk-search" onClick={onOpenPalette}>
        <Icon name="search" />
        <span className="mk-search-text">Search issues, branches, prs</span>
        <span className="mk-kbd">⌘ K</span>
      </button>

      <div className="mk-topbar-right">
        {syncEnabled && <span className="mk-pill mk-sync-badge">Sync Enabled</span>}
        {isLeader && (
          <span className="mk-pill mk-leader-badge" title="This window holds the UI leader lease">Controlling</span>
        )}
        <RepoPicker
          boards={boards}
          activeBoard={activeBoard}
          onPick={onPickBoard}
          onAddRepository={onAddRepository}
        />
        <button className="mk-icbtn" aria-label="Settings" onClick={onOpenSettings}><Icon name="settings" /></button>
      </div>
    </header>
  );
}
