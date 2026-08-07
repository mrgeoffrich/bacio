import { describe, it, expect } from 'vitest';
import { NAV, navFor, homeView, DEFAULT_SURFACES } from '../nav';
import type { NavSurfaces } from '../nav';

// The four flag combinations, named by what they mean.
const GIT_DEFAULT: NavSurfaces = { showAgentSurfaces: true, showKanban: false };
const WORKSPACE_DEFAULT: NavSurfaces = { showAgentSurfaces: false, showKanban: true };
const BOTH_ON: NavSurfaces = { showAgentSurfaces: true, showKanban: true };
const BOTH_OFF: NavSurfaces = { showAgentSurfaces: false, showKanban: false };
const ALL = [GIT_DEFAULT, WORKSPACE_DEFAULT, BOTH_ON, BOTH_OFF];

const views = (s: NavSurfaces) => navFor(s).map(i => i.view);

describe('NAV', () => {
  it('groups every entry and labels it', () => {
    for (const item of NAV) {
      expect(item.group === 'work' || item.group === 'agent').toBe(true);
      expect(item.label.length).toBeGreaterThan(0);
    }
  });

  it('keeps the two groups contiguous so one divider suffices', () => {
    // Topbar draws a separator wherever consecutive items change group.
    // More than one transition would render more than one hairline.
    const transitions = NAV.filter((item, i) => i > 0 && NAV[i - 1].group !== item.group);
    expect(transitions).toHaveLength(1);
  });

  it('labels the features entry "Epics" while keeping the internal view id', () => {
    // The Epics rename is display + URL only; the view id is load-bearing
    // across App's routes, RepoProvider's legacy page words and the CSS.
    const epics = NAV.find(i => i.label === 'Epics');
    expect(epics?.view).toBe('features');
  });

  it('names the two boards distinctly', () => {
    expect(NAV.find(i => i.view === 'pipeline')?.label).toBe('Agentic Pipeline');
    expect(NAV.find(i => i.view === 'board')?.label).toBe('Kanban');
  });
});

describe('navFor', () => {
  it('shows everything but the Kanban on a git repo default', () => {
    expect(views(GIT_DEFAULT)).toEqual(['features', 'docs', 'history', 'pipeline', 'agents', 'monitor']);
  });

  it('hides all three agent surfaces on a workspace default', () => {
    expect(views(WORKSPACE_DEFAULT)).toEqual(['board', 'features', 'docs', 'history']);
  });

  it('shows every tab when both gates are on', () => {
    expect(views(BOTH_ON)).toEqual(NAV.map(i => i.view));
  });

  it('still leaves the ungated tabs when both gates are off', () => {
    expect(views(BOTH_OFF)).toEqual(['features', 'docs', 'history']);
  });

  it('never returns an empty nav', () => {
    for (const s of ALL) expect(navFor(s).length).toBeGreaterThan(0);
  });
});

describe('homeView', () => {
  // These four reproduce the pre-settings landing behaviour exactly: a
  // git repo led with the Agentic Pipeline, a workspace with the Kanban.
  it('lands a git repo default on the Agentic Pipeline', () => {
    expect(homeView(GIT_DEFAULT)).toBe('pipeline');
  });

  it('lands a workspace default on the Kanban', () => {
    expect(homeView(WORKSPACE_DEFAULT)).toBe('board');
  });

  it('prefers the Pipeline when both are available', () => {
    expect(homeView(BOTH_ON)).toBe('pipeline');
  });

  it('falls through to Epics when both boards are hidden', () => {
    expect(homeView(BOTH_OFF)).toBe('features');
  });

  // The invariant the whole design rests on: you can never be sent to a
  // page whose tab isn't there. homeView uses an explicit precedence
  // list rather than "first surviving entry", so this is a real
  // constraint between two independent pieces of logic, not a tautology.
  it('always returns a view that is actually in the nav', () => {
    for (const s of ALL) {
      expect(views(s)).toContain(homeView(s));
    }
  });
});

describe('DEFAULT_SURFACES', () => {
  // The fallback for "no board resolved yet". It must read as a git
  // repo, mirroring normalizeRepoKind — otherwise a slow board load
  // would flash a stripped nav on a real repo.
  it('matches the git-repo defaults', () => {
    expect(DEFAULT_SURFACES).toEqual(GIT_DEFAULT);
  });
});
