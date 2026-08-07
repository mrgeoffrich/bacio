// The top-nav's data and the two derivations that read it.
//
// This lives in lib/ rather than in Topbar.tsx because homeView() is
// defined in terms of the nav, and RepoProvider / routes.ts need
// homeView. A component module would drag JSX and the whole ./api seam
// into lib/__tests__'s plain-node smoke suites. nav.ts imports a type
// from routes.ts; routes.ts imports nothing back, so there is no cycle.

import type { NavView } from './routes';

// The nav splits into two groups. `work` is the surfaces every space
// has a use for; `agent` is the agent-driven lifecycle, shown only when
// the space has Agent Mode on. The Topbar draws a hairline between
// them.
export type NavGroupId = 'work' | 'agent';

export type NavItem = { view: NavView; label: string; group: NavGroupId };

// NAV is the ordered top-nav and the single source the digit hotkeys map
// onto by position. Order is load-bearing: App's 1..9 handler indexes
// the FILTERED list, and Topbar renders the same filtered list, so any
// filtering has to happen in navFor() and nowhere else.
//
// The `features` view id is internal and deliberately unchanged by the
// Epics rename — only the label and the URL follow it (see routes.ts).
export const NAV: NavItem[] = [
  { view: 'board', label: 'Kanban', group: 'work' },
  { view: 'features', label: 'Epics', group: 'work' },
  { view: 'docs', label: 'Documents', group: 'work' },
  { view: 'history', label: 'History', group: 'work' },
  { view: 'pipeline', label: 'Agentic Pipeline', group: 'agent' },
  { view: 'agents', label: 'Agents', group: 'agent' },
  { view: 'monitor', label: 'Monitor', group: 'agent' },
];

// NavSurfaces is a structural subset of Board, so navFor(board)
// type-checks with no adaptor. Declared here rather than imported from
// ../api to keep this module clear of the transport seam — that is what
// lets the plain-node smoke suites import it.
export type NavSurfaces = { showAgentSurfaces: boolean; showKanban: boolean };

// DEFAULT_SURFACES is the fallback when no board has resolved yet —
// boards still loading, or a prefix that matches nothing. It mirrors
// normalizeRepoKind's rule that an unknown space reads as a git repo,
// so a slow load never flashes a stripped-down nav on a real repo.
export const DEFAULT_SURFACES: NavSurfaces = { showAgentSurfaces: true, showKanban: false };

// navFor is the nav a space actually gets. Exported and consumed by BOTH
// Topbar (the buttons) and App (the digit hotkeys) — filtering in only
// one of the two would silently desync the keyboard from what's on
// screen.
//
// Epics, Documents and History are never gated, so the result is never
// empty and homeView() below always has something to land on.
export function navFor(surfaces: NavSurfaces): NavItem[] {
  return NAV.filter(item => {
    if (item.group === 'agent') return surfaces.showAgentSurfaces;
    if (item.view === 'board') return surfaces.showKanban;
    return true;
  });
}

// homeView is the view a space lands on when nothing more specific is
// asked for — a bare `/`, a repo switch off a page the new space
// doesn't have, closing an issue with an empty back stack, or the
// `/:prefix/*` catch-all.
//
// An explicit precedence list, NOT "the first surviving nav entry". The
// visual order puts the work group first so the agent tabs read as a
// separate cluster, but a git repo must still land on the Agentic
// Pipeline the way it always has. Decoupling the two means the groups
// can be reordered without silently moving everyone's home.
//
// The precedence reproduces the pre-settings behaviour exactly: a git
// repo on defaults (agent on, Kanban off) → pipeline; a workspace on
// defaults (agent off, Kanban on) → board. With both gates off it falls
// through to Epics.
const HOME_PRECEDENCE: NavView[] = ['pipeline', 'board', 'features'];

export function homeView(surfaces: NavSurfaces): NavView {
  const visible = navFor(surfaces);
  for (const view of HOME_PRECEDENCE) {
    if (visible.some(item => item.view === view)) return view;
  }
  // Unreachable while `features` is ungated, but a total function beats
  // a non-null assertion on a list someone might later filter harder.
  return visible[0]?.view ?? 'docs';
}
