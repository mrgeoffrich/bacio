import { describe, it, expect } from 'vitest';
import { NAV, navForKind } from '../Topbar';

// NAV is the contract App.tsx maps the digit hotkeys (1..7) onto, in
// order, so its shape and ordering are load-bearing beyond the topbar
// render. Importing Topbar also pulls its whole component graph (which
// reaches the `./api` seam) through the test harness, so a transport
// regression surfaces here too.

describe('Topbar NAV', () => {
  it('lists the top-nav views in the order the digit hotkeys map onto', () => {
    expect(NAV.map(n => n.view)).toEqual([
      'pipeline',
      'board',
      'features',
      'docs',
      'agents',
      'history',
      'monitor',
    ]);
  });

  it('pairs every view with a non-empty label', () => {
    for (const item of NAV) {
      expect(item.view).toBeTruthy();
      expect(item.label).toBeTruthy();
    }
  });

  // The pivot rename: the label distinguishes the agent-driven board from the
  // human one. The `pipeline` view id is untouched — it is load-bearing in
  // routes.ts, App's routes/redirects and RepoProvider's legacy page words.
  it('labels the two boards Agentic Pipeline and Kanban', () => {
    expect(NAV.find(n => n.view === 'pipeline')?.label).toBe('Agentic Pipeline');
    expect(NAV.find(n => n.view === 'board')?.label).toBe('Kanban');
  });
});

describe('Topbar navForKind', () => {
  it('gives a git repo (and an unknown kind) the full nav', () => {
    expect(navForKind('git')).toEqual(NAV);
    expect(navForKind(undefined)).toEqual(NAV);
  });

  // Locked decision D1: a workspace has no working tree, so a dispatched
  // agent would have nowhere to work — the Agentic Pipeline entry is hidden.
  it('hides only the Agentic Pipeline entry on a workspace', () => {
    const views = navForKind('workspace').map(n => n.view);
    expect(views).not.toContain('pipeline');
    expect(views).toContain('board');
    expect(views).toEqual(NAV.filter(n => n.view !== 'pipeline').map(n => n.view));
  });
});
