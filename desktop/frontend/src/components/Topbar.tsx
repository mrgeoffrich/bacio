import React from 'react';
import { useLocation, useNavigate } from 'react-router';
import Icon from './Icon';
import RepoPicker from './RepoPicker';
import Tooltip from './Tooltip';
import NotificationBell from './NotificationBell';
import ShippedPopover from './ShippedPopover';
import { WEB_MODE } from '../env';
import { navFor, DEFAULT_SURFACES } from '../lib/nav';
import type { NavItem } from '../lib/nav';
import { viewPath, viewFromPath } from '../lib/routes';
import { syncBadgeState } from '../lib/syncBadge';
import { useActiveRepo } from '../state/RepoProvider';
import { useAgents } from '../state/AgentsProvider';
import { useCards } from '../state/CardsProvider';
import { usePreferences } from '../state/PreferencesProvider';
import { useLeaderStatus } from '../state/useLeaderStatus';
import { useNotifications } from '../state/useNotifications';

// BACI-361: the topbar reads its live data from the state hooks rather than
// the ~18 props App used to drill in. Only the three shell-owned overlay
// controls (close-settings-before-navigate, open-settings, open-sync) stay
// props — the Shell owns those flags.
type TopbarProps = {
  onBeforeNavigate?: () => void;
  onOpenSettings: () => void;
  onOpenSync: () => void;
};

export default function Topbar({ onBeforeNavigate, onOpenSettings, onOpenSync }: TopbarProps) {
  const { boards, activeBoard, openIssue: onOpenIssue } = useActiveRepo();
  const { agentCounts } = useAgents();
  const {
    shippedCount,
    shippedScope,
    setShippedScope: onShippedScopeChange,
    shippedRepoScope,
    setShippedRepoScope: onShippedRepoScopeChange,
    flyingShipKey,
    shipFlashing,
    onShipFlightDone,
  } = useCards();
  const { timezone } = usePreferences();
  const leaderState = useLeaderStatus();
  const {
    notifUnreadCount,
    setNotifUnreadCount: onNotifCountChange,
    openNotificationIssue: onOpenNotificationIssue,
  } = useNotifications();
  // BACI-203: the active view is derived from the URL, not a prop.
  // useLocation re-renders on every navigation so the segmented
  // button's `is-active` class stays in lockstep. The breadcrumb
  // pill is derived from the same pathname — when the workspace
  // route is mounted, the path matches `/<prefix>/issues/:key` and the
  // key is pulled directly off the path so the breadcrumb doesn't need a
  // separate prop.
  const location = useLocation();
  const navigate = useNavigate();
  const activeView = viewFromPath(location.pathname) || 'board';
  // BACI-285: the workspace path now carries the repo prefix as its
  // first segment (`/<prefix>/issues/:key`).
  const issueMatch = location.pathname.match(/^\/[^/]+\/issues\/([^/]+)$/);
  const openIssueKey = issueMatch ? issueMatch[1] : null;
  const board = boards.find(b => b.prefix === activeBoard);
  // BACI-89: live status indicator with idle / in-progress / failed
  // variants. BACI-108: always rendered (even unconfigured repos),
  // always clickable — a one-click route into the standalone Sync
  // view. BACI-238: the affordance is a compact icon button rather
  // than a text pill — the Refresh glyph stays constant; the
  // surrounding chrome carries the state (pistachio when
  // idle-enabled, pulsing review while syncing, blocked-red on
  // failure, amber-muted when configured-but-globally-paused, muted
  // when sync isn't set up for this repo), and the tooltip carries the
  // per-state hover copy. BACI-376 moved the variant + copy decision
  // into lib/syncBadge so it can be unit-tested and so the global
  // background-sync toggle stops being conflated with this repo's own
  // sync configuration.
  const syncBadge = syncBadgeState(board);
  const isLeader = leaderState?.amLeader ?? false;
  // BACI-74: small pills tucked into the Agents button when the active
  // repo has any non-ended sessions. Available = idle or active AND
  // !busy; busy folds waiting in. Hidden entirely when both are zero so
  // the nav stays quiet on an empty repo.
  const available = agentCounts?.available ?? 0;
  const busy = agentCounts?.busy ?? 0;
  const showAgentCounts = (available + busy) > 0;
  const agentCountsLabel = `${available} available, ${busy} busy`;
  // Which tabs this space exposes. `board` is undefined while the board
  // list loads, so fall back rather than flashing a stripped nav.
  const items = navFor(board ?? DEFAULT_SURFACES);

  const renderNavButton = (item: NavItem) => {
    const isAgents = item.view === 'agents';
    const button = (
      <button
        className={`mk-segmented-btn ${activeView === item.view ? 'is-active' : ''}`}
        onClick={() => {
          // BACI-285: no-op when no real repo is active (the
          // repo-not-found / no-repos screen) so we don't navigate
          // to a prefix-less `//<view>` path.
          if (!activeBoard) return;
          if (onBeforeNavigate) onBeforeNavigate();
          navigate(viewPath(activeBoard, item.view));
        }}
      >
        {item.label}
        {isAgents && showAgentCounts && (
          <span className="mk-agent-counts-badges" aria-label={agentCountsLabel}>
            <span className="mk-pill mk-status-idle mk-agent-counts-available">{available}</span>
            <span className="mk-pill mk-status-busy mk-agent-counts-busy">{busy}</span>
          </span>
        )}
      </button>
    );
    if (isAgents && showAgentCounts) {
      return <Tooltip key={item.view} label={agentCountsLabel}>{button}</Tooltip>;
    }
    return <React.Fragment key={item.view}>{button}</React.Fragment>;
  };

  return (
    <header className={`mk-topbar${WEB_MODE ? ' is-web' : ''}`}>
      <div className="mk-brand">
        <img src={`${import.meta.env.BASE_URL}bacio-mark.png`} width="22" height="22" alt="" />
        <span className="mk-brand-name">bacio</span>
      </div>

      {/* The agent-driven tabs (Agentic Pipeline / Agents / Monitor) sit
          together and slightly apart from the rest. The divider is drawn
          between consecutive items whose group differs, which means it
          only appears when BOTH groups are non-empty — a space with
          Agent Mode off never shows a dangling hairline. */}
      <div className="mk-segmented">
        {items.map((item, i) => (
          <React.Fragment key={item.view}>
            {i > 0 && items[i - 1].group !== item.group && (
              // A non-interactive span, so it keeps the topbar's
              // --wails-draggable: drag (only button/input/select opt
              // out) — dragging the window by a divider is fine.
              <span className="mk-segmented-sep" aria-hidden="true" />
            )}
            {renderNavButton(item)}
          </React.Fragment>
        ))}
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

      {/* BACI-336: the Shipped · N pill lives in the topbar centre now
          (it used to sit in the Pipeline Shipping column) so the shipped
          count is an always-visible status across every view, not only
          Pipeline. `margin: 0 auto` centres it in the flex row; the
          breadcrumb pill (when an issue is open) eats from its left
          margin, so the centre shifts slightly right with a workspace
          open — an accepted, documented minor shift. */}
      <div className="mk-topbar-center">
        <ShippedPopover
          activeBoard={activeBoard}
          shippedCount={shippedCount}
          scope={shippedScope}
          onScopeChange={onShippedScopeChange}
          repoScope={shippedRepoScope}
          onRepoScopeChange={onShippedRepoScopeChange}
          timezone={timezone}
          onOpenIssue={onOpenIssue}
          flyingShipKey={flyingShipKey}
          shipFlashing={shipFlashing}
          onShipFlightDone={onShipFlightDone}
        />
      </div>

      <div className="mk-topbar-right">
        {/* BACI-287: the notification bell takes the top-right corner the
            `+` (new issue) button used to hold — the `+` moved into the
            Pipeline Backlog column header. The bell is global / cross-repo
            (it lists notifications from every repo), so it isn't gated on
            the active board. */}
        <NotificationBell
          unreadCount={notifUnreadCount}
          onCountChange={onNotifCountChange}
          onOpenIssue={onOpenNotificationIssue}
        />
        <Tooltip label={syncBadge.tooltip}>
          <button
            type="button"
            className={`mk-sync-btn is-${syncBadge.variant}`}
            aria-label={syncBadge.label}
            onClick={onOpenSync}
          >
            <Icon name="refresh" />
          </button>
        </Tooltip>
        {isLeader && (
          // BACI-249: leader-lease indicator is an icon-only button
          // sized like the sibling sync / settings / plus buttons so
          // the topbar's right-hand strip stays a uniform icon rhythm.
          // Non-leader windows still render nothing (same gate as the
          // earlier text pill).
          <Tooltip label="This window holds the UI leader lease">
            <button
              type="button"
              className="mk-icbtn mk-leader-btn"
              aria-label="Controlling window"
            >
              <Icon name="crown" />
            </button>
          </Tooltip>
        )}
        <RepoPicker />
        <button className="mk-icbtn" aria-label="Settings" onClick={onOpenSettings}><Icon name="settings" /></button>
      </div>
    </header>
  );
}
