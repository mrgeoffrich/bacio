import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { Provider as TooltipProvider } from '@radix-ui/react-tooltip';
import Topbar from '../Topbar';
import type { Board, RepoKind } from '../../api';

// The nav's data and derivations moved to lib/nav (and have their own
// suite there); what's left to pin here is the render — that the topbar
// draws exactly the tabs the active space exposes, and exactly one
// group divider when both groups are present.
//
// Topbar reads everything off the state hooks (BACI-361), so this mocks
// them rather than standing up the whole provider chain — same shape as
// the RepoPicker suite.
const hoisted = vi.hoisted(() => ({
  boards: [] as Board[],
  activeBoard: '',
}));

vi.mock('../../state/RepoProvider', () => ({
  useActiveRepo: () => ({
    boards: hoisted.boards,
    activeBoard: hoisted.activeBoard,
    openIssue: vi.fn(),
    pickBoard: vi.fn(),
    addRepository: vi.fn(),
    refreshBoards: vi.fn(),
    patchBoard: vi.fn(),
  }),
}));
vi.mock('../../state/AgentsProvider', () => ({
  useAgents: () => ({ agentCounts: null }),
}));
vi.mock('../../state/CardsProvider', () => ({
  useCards: () => ({
    shippedCount: 0, shippedScope: 'week', setShippedScope: vi.fn(),
    shippedRepoScope: 'all', setShippedRepoScope: vi.fn(),
    flyingShipKey: null, shipFlashing: false, onShipFlightDone: vi.fn(),
  }),
}));
vi.mock('../../state/PreferencesProvider', () => ({
  usePreferences: () => ({ timezone: 'UTC' }),
}));
vi.mock('../../state/useLeaderStatus', () => ({
  useLeaderStatus: () => ({ amLeader: false }),
}));
vi.mock('../../state/useNotifications', () => ({
  useNotifications: () => ({
    notifUnreadCount: 0, setNotifUnreadCount: vi.fn(), openNotificationIssue: vi.fn(),
  }),
}));
// The two popovers pull in the api seam and a poll; stub them out — this
// suite is about the nav strip.
vi.mock('../ShippedPopover', () => ({ default: () => null }));
vi.mock('../NotificationBell', () => ({ default: () => null }));
vi.mock('../RepoPicker', () => ({ default: () => null }));

function board(over: Partial<Board> = {}): Board {
  return {
    prefix: 'BACI',
    name: 'bacio',
    kind: 'git' as RepoKind,
    showAgentSurfaces: true,
    showKanban: false,
    issueCount: 0,
    syncEnabled: false,
    syncBackgroundEnabled: false,
    syncInProgress: false,
    ...over,
  };
}

function renderTopbar(over: Partial<Board> = {}) {
  hoisted.boards = [board(over)];
  hoisted.activeBoard = 'BACI';
  // Topbar derives the active segment from the URL, so it needs a router;
  // the sync badge is a Radix Tooltip, so it needs that provider too —
  // both are supplied by App in the real tree.
  const { container } = render(
    <TooltipProvider>
      <MemoryRouter initialEntries={['/BACI/epics']}>
        <Topbar onOpenSettings={vi.fn()} onOpenSync={vi.fn()} />
      </MemoryRouter>
    </TooltipProvider>,
  );
  return container;
}

const navLabels = (container: HTMLElement) =>
  Array.from(container.querySelectorAll('.mk-segmented-btn')).map(
    el => el.textContent?.trim() ?? '',
  );

const separators = (container: HTMLElement) =>
  container.querySelectorAll('.mk-segmented-sep').length;

describe('Topbar nav', () => {
  beforeEach(() => {
    hoisted.boards = [];
    hoisted.activeBoard = '';
  });

  it('labels the features tab "Epics"', () => {
    const c = renderTopbar();
    expect(navLabels(c)).toContain('Epics');
    // The URL follows the label; the internal view id does not.
    expect(screen.getByText('Epics')).toBeInTheDocument();
  });

  it('renders the work tabs then the agent tabs, in that order', () => {
    const c = renderTopbar({ showKanban: true });
    expect(navLabels(c)).toEqual([
      'Kanban', 'Epics', 'Documents', 'History',
      'Agentic Pipeline', 'Agents', 'Monitor',
    ]);
  });

  it('hides all three agent tabs when Agent Mode is off', () => {
    const c = renderTopbar({ showAgentSurfaces: false, showKanban: true });
    const labels = navLabels(c);
    expect(labels).not.toContain('Agentic Pipeline');
    expect(labels).not.toContain('Agents');
    expect(labels).not.toContain('Monitor');
    expect(labels).toEqual(['Kanban', 'Epics', 'Documents', 'History']);
  });

  it('hides the Kanban tab when Show Kanban Board is off', () => {
    const c = renderTopbar({ showKanban: false });
    expect(navLabels(c)).not.toContain('Kanban');
  });

  // The divider is drawn between consecutive items whose group differs,
  // so an empty group must not leave a hairline dangling at either end.
  it('draws exactly one group divider when both groups are present', () => {
    expect(separators(renderTopbar({ showKanban: true }))).toBe(1);
  });

  it('draws no divider when the agent group is empty', () => {
    expect(separators(renderTopbar({ showAgentSurfaces: false, showKanban: true }))).toBe(0);
  });

  it('draws one divider when only the Kanban is hidden', () => {
    // The work group still has Epics / Documents / History, so the
    // boundary is still real.
    expect(separators(renderTopbar({ showKanban: false }))).toBe(1);
  });
});
