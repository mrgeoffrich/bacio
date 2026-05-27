import React from 'react';
import { useLocation, useNavigate } from 'react-router';
import Icon from './Icon.jsx';
import RepoPicker from './RepoPicker.jsx';
import ShippedPopover from './ShippedPopover.jsx';
import Tooltip from './Tooltip.jsx';
import { WEB_MODE } from '../env';
import { viewPath, viewFromPath } from '../lib/routes';

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

// formatSyncTime renders an ISO timestamp as a short local string for
// the sync badge's hover tooltip. Falls back to the raw string if the
// date can't be parsed.
function formatSyncTime(iso) {
  if (!iso) return '';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleString();
}

export default function Topbar({ boards, activeBoard, onPickBoard, onAddRepository, onBeforeNavigate, onOpenPalette, onOpenSettings, onOpenSync, onOpenComposer, leaderState, agentCounts, shippedCount, shippedScope, onShippedScopeChange, onOpenIssue, flyingShipKey, shipFlashing, onShipFlightDone, audioEnabled }) {
  // BACI-203: the active view is derived from the URL, not a prop.
  // useLocation re-renders on every navigation so the segmented
  // button's `is-active` class stays in lockstep. The breadcrumb
  // pill is derived from the same pathname — when the workspace
  // route is mounted, the path matches `/issues/:key` and the key is
  // pulled directly off the path so the breadcrumb doesn't need a
  // separate prop.
  const location = useLocation();
  const navigate = useNavigate();
  const activeView = viewFromPath(location.pathname) || 'board';
  const issueMatch = location.pathname.match(/^\/issues\/([^/]+)$/);
  const openIssueKey = issueMatch ? issueMatch[1] : null;
  const board = boards.find(b => b.prefix === activeBoard);
  const syncEnabled = !!board?.syncEnabled;
  // BACI-89: live status indicator with idle / in-progress / failed
  // variants. BACI-108: always rendered (even unconfigured repos),
  // always clickable — a one-click route into the standalone Sync
  // view. BACI-238: the affordance is a compact icon button rather
  // than a text pill — the Refresh glyph stays constant; the
  // surrounding chrome carries the state (pistachio when
  // idle-enabled, pulsing review while syncing, blocked-red on
  // failure, muted when sync isn't configured yet), and the tooltip
  // carries the per-state hover copy (last-synced time, in-progress
  // hint, error detail).
  const syncInProgress = !!board?.syncInProgress;
  const syncLastError = board?.syncLastError || '';
  const syncLastAt = board?.syncLastAt || '';
  let syncBtnClass = 'mk-sync-btn';
  let syncBtnLabel;
  let syncBtnTooltip;
  if (syncInProgress) {
    syncBtnClass += ' is-syncing';
    syncBtnLabel = 'Syncing…';
    syncBtnTooltip = 'Background sync in progress · click to open Sync settings';
  } else if (syncLastError) {
    syncBtnClass += ' is-error';
    syncBtnLabel = 'Sync failed';
    syncBtnTooltip = `Last sync failed: ${syncLastError} · click to open Sync settings`;
  } else if (syncEnabled) {
    syncBtnClass += ' is-enabled';
    syncBtnLabel = 'Sync enabled';
    syncBtnTooltip = syncLastAt
      ? `Last synced ${formatSyncTime(syncLastAt)} · click to open Sync settings`
      : 'Background sync configured · click to open Sync settings';
  } else {
    syncBtnClass += ' is-unconfigured';
    syncBtnLabel = 'Sync';
    syncBtnTooltip = 'Sync not configured for this repo · click to open Sync settings';
  }
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
              onClick={() => {
                if (onBeforeNavigate) onBeforeNavigate();
                navigate(viewPath(view));
              }}
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
          onClick={() => navigate(-1)}
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
        <ShippedPopover
          activeBoard={activeBoard}
          shippedCount={shippedCount ?? 0}
          scope={shippedScope}
          onScopeChange={onShippedScopeChange}
          onOpenIssue={onOpenIssue}
          flyingShipKey={flyingShipKey}
          shipFlashing={shipFlashing}
          onShipFlightDone={onShipFlightDone}
          audioEnabled={audioEnabled}
        />
        {/* BACI-166: + opens the IssueComposer modal. Hidden on the
            cross-repo "all" pseudo-board — the composer needs a real
            prefix to create against. */}
        {onOpenComposer && activeBoard && activeBoard !== 'all' && (
          <Tooltip label="New issue (⌘N)">
            <button
              type="button"
              className="mk-icbtn"
              aria-label="New issue"
              onClick={onOpenComposer}
            >
              <Icon name="plus" />
            </button>
          </Tooltip>
        )}
        <Tooltip label={syncBtnTooltip}>
          <button
            type="button"
            className={syncBtnClass}
            aria-label={syncBtnLabel}
            onClick={onOpenSync}
          >
            <Icon name="refresh" />
          </button>
        </Tooltip>
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
