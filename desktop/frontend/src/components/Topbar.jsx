import React from 'react';
import Icon from './Icon.jsx';

export default function Topbar({ boardName, onOpenPalette, onNewIssue, agentRuns }) {
  return (
    <header className="mk-topbar">
      <nav className="mk-crumbs">
        <span className="mk-crumb-root">orchestrator-os</span>
        <span className="mk-crumb-sep">/</span>
        <span className="mk-crumb-cur">{boardName}</span>
      </nav>

      <button className="mk-search" onClick={onOpenPalette}>
        <Icon name="search" />
        <span className="mk-search-text">Search issues, branches, prs</span>
        <span className="mk-kbd">⌘ K</span>
      </button>

      <div className="mk-topbar-right">
        <span className="mk-agent-pill">
          <span className="mk-pulse-dot" />
          claude · {agentRuns} running
        </span>
        <button className="mk-icbtn" aria-label="Filter"><Icon name="filter" /></button>
        <button className="mk-icbtn" aria-label="Notifications"><Icon name="bell" /></button>
        <button className="mk-btn-primary" onClick={onNewIssue}>
          <Icon name="plus" /> New issue
        </button>
      </div>
    </header>
  );
}
