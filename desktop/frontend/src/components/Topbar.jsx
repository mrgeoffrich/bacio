import React from 'react';
import Icon from './Icon.jsx';
import RepoPicker from './RepoPicker.jsx';
import Tooltip from './Tooltip.jsx';
import { WEB_MODE } from '../env';

// NAV is the ordered top-nav. Exported so App can map the digit
// hotkeys onto the same views in the same order. As of BACI-50 the
// Agents tab is also available in web mode — the bacio api ships the
// composite GET /agents/cards endpoint that assembles the AgentCard
// shape server-side.
export const NAV = [
  { view: 'board', label: 'Issues' },
  { view: 'features', label: 'Features' },
  { view: 'docs', label: 'Documents' },
  { view: 'agents', label: 'Agents' },
  { view: 'history', label: 'History' },
];

export default function Topbar({ boards, activeBoard, onPickBoard, onAddRepository, activeView, onChangeView, onOpenPalette, onOpenSettings, leaderState, openIssueKey, onCloseIssue, agentCounts }) {
  const syncEnabled = !!boards.find(b => b.prefix === activeBoard)?.syncEnabled;
  const isLeader = leaderState?.amLeader ?? false;
  // BACI-74: small pills tucked into the Agents button when the active
  // repo has any non-ended sessions. Available = idle or active AND
  // !busy; busy folds waiting in. Hidden entirely when both are zero so
  // the nav stays quiet on an empty repo.
  const available = agentCounts?.available ?? 0;
  const busy = agentCounts?.busy ?? 0;
  const showAgentCounts = (available + busy) > 0;
  const agentCountsLabel = `${available} available, ${busy} busy`;
  return (
    <header className={`mk-topbar${WEB_MODE ? ' is-web' : ''}`}>
      <div className="mk-brand">
        <img src={`${import.meta.env.BASE_URL}bacio-mark.png`} width="22" height="22" alt="" />
        <span className="mk-brand-name">bacio</span>
      </div>

      <div className="mk-segmented">
        {NAV.map(({ view, label }) => {
          const isAgents = view === 'agents';
          const button = (
            <button
              className={`mk-segmented-btn ${activeView === view ? 'is-active' : ''}`}
              onClick={() => onChangeView(view)}
            >
              {label}
              {isAgents && showAgentCounts && (
                <span className="mk-agent-counts-badges" aria-label={agentCountsLabel}>
                  <span className="mk-pill mk-status-idle mk-agent-counts-available">{available}</span>
                  <span className="mk-pill mk-status-busy mk-agent-counts-busy">{busy}</span>
                </span>
              )}
            </button>
          );
          if (isAgents && showAgentCounts) {
            return <Tooltip key={view} label={agentCountsLabel}>{button}</Tooltip>;
          }
          return <React.Fragment key={view}>{button}</React.Fragment>;
        })}
      </div>

      {openIssueKey && (
        <button
          type="button"
          className="mk-breadcrumb"
          onClick={onCloseIssue}
          title="Back (esc)"
        >
          ← {openIssueKey}
        </button>
      )}

      <button className="mk-search" onClick={onOpenPalette}>
        <Icon name="search" />
        <span className="mk-search-text">Search issues, branches, prs</span>
        <span className="mk-kbd">⌘ K</span>
      </button>

      <div className="mk-topbar-right">
        {syncEnabled && <span className="mk-pill mk-sync-badge">Sync Enabled</span>}
        {isLeader && (
          <Tooltip label="This window holds the UI leader lease">
            <span className="mk-pill mk-leader-badge">Controlling</span>
          </Tooltip>
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
