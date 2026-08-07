import { describe, it, expect } from 'vitest';
import { activityByPrefix, rankRepos, groupReposByKind } from '../repoPickerOrder';
import type { Board, RepoActivity, RepoKind } from '../../api';

// BACI-369: the repo picker's ordering is the ticket, so it gets its own
// tests away from the DOM.

function board(prefix: string, kind: RepoKind = 'git'): Board {
  return {
    prefix,
    name: prefix.toLowerCase(),
    kind,
    showAgentSurfaces: kind !== 'workspace',
    showKanban: kind === 'workspace',
    issueCount: 0,
    syncEnabled: false,
    syncBackgroundEnabled: false,
    syncInProgress: false,
  };
}

function activity(prefix: string, lastActivityAt?: string, activeJobs = 0): RepoActivity {
  return { prefix, lastActivityAt, activeJobs };
}

const prefixes = (boards: Board[]) => boards.map((b) => b.prefix);

describe('rankRepos', () => {
  it('floats repos with jobs in flight above more-recently-active idle repos', () => {
    const boards = [board('AAAA'), board('BBBB')];
    const byPrefix = activityByPrefix([
      activity('AAAA', '2026-08-06T02:00:00Z'),
      activity('BBBB', '2026-08-01T02:00:00Z', 1),
    ]);
    expect(prefixes(rankRepos(boards, byPrefix))).toEqual(['BBBB', 'AAAA']);
  });

  it('treats in-flight as a tier, not a magnitude', () => {
    const boards = [board('AAAA'), board('BBBB')];
    const byPrefix = activityByPrefix([
      activity('AAAA', '2026-08-06T02:00:00Z', 1),
      activity('BBBB', '2026-08-01T02:00:00Z', 5),
    ]);
    // Both busy — recency decides, not the job count.
    expect(prefixes(rankRepos(boards, byPrefix))).toEqual(['AAAA', 'BBBB']);
  });

  it('orders idle repos by most recent activity', () => {
    const boards = [board('AAAA'), board('BBBB'), board('CCCC')];
    const byPrefix = activityByPrefix([
      activity('AAAA', '2026-07-01T00:00:00Z'),
      activity('BBBB', '2026-08-05T00:00:00Z'),
      activity('CCCC', '2026-08-01T00:00:00Z'),
    ]);
    expect(prefixes(rankRepos(boards, byPrefix))).toEqual(['BBBB', 'CCCC', 'AAAA']);
  });

  it('sorts repos with no activity entry last, in prefix order', () => {
    const boards = [board('ZZZZ'), board('MMMM'), board('AAAA')];
    const byPrefix = activityByPrefix([activity('MMMM', '2026-08-05T00:00:00Z')]);
    expect(prefixes(rankRepos(boards, byPrefix))).toEqual(['MMMM', 'AAAA', 'ZZZZ']);
  });

  it('leaves the input order unchanged when there is no activity data', () => {
    const boards = [board('AAAA'), board('BBBB'), board('CCCC')];
    expect(prefixes(rankRepos(boards, new Map()))).toEqual(['AAAA', 'BBBB', 'CCCC']);
  });

  it('does not mutate its input array', () => {
    const boards = [board('BBBB'), board('AAAA')];
    const byPrefix = activityByPrefix([activity('AAAA', '2026-08-06T02:00:00Z')]);
    rankRepos(boards, byPrefix);
    expect(prefixes(boards)).toEqual(['BBBB', 'AAAA']);
  });
});

// The pivot's kind grouping. The load-bearing property is that grouping
// is a *partition* layered on top of the frozen rank order, never a
// re-sort — RepoPicker snapshots rankRepos at open time so a poll tick
// can't reshuffle rows under the cursor, and the sections have to
// preserve that.
describe('groupReposByKind', () => {
  it('splits git repos from workspaces', () => {
    const boards = [board('AAAA'), board('WKSP', 'workspace'), board('BBBB')];
    const { git, workspaces } = groupReposByKind(boards);
    expect(prefixes(git)).toEqual(['AAAA', 'BBBB']);
    expect(prefixes(workspaces)).toEqual(['WKSP']);
  });

  it('preserves the incoming order inside each group', () => {
    // The order here is deliberately NOT sorted — it's what a frozen
    // rankRepos snapshot looks like (busy first, then recency).
    const boards = [
      board('ZZZZ'),
      board('WSPB', 'workspace'),
      board('AAAA'),
      board('WSPA', 'workspace'),
      board('MMMM'),
    ];
    const { git, workspaces } = groupReposByKind(boards);
    expect(prefixes(git)).toEqual(['ZZZZ', 'AAAA', 'MMMM']);
    expect(prefixes(workspaces)).toEqual(['WSPB', 'WSPA']);
  });

  it('round-trips a frozen rank order through the partition', () => {
    const boards = [board('AAAA'), board('WKSP', 'workspace'), board('BBBB')];
    const byPrefix = activityByPrefix([
      activity('BBBB', '2026-08-01T00:00:00Z', 1),
      activity('WKSP', '2026-08-06T00:00:00Z'),
      activity('AAAA', '2026-08-02T00:00:00Z'),
    ]);
    const ranked = rankRepos(boards, byPrefix);
    expect(prefixes(ranked)).toEqual(['BBBB', 'WKSP', 'AAAA']);
    const { git, workspaces } = groupReposByKind(ranked);
    // BBBB (busy) still outranks AAAA inside the git section.
    expect(prefixes(git)).toEqual(['BBBB', 'AAAA']);
    expect(prefixes(workspaces)).toEqual(['WKSP']);
  });

  it('returns empty groups rather than undefined when a kind is absent', () => {
    const { git, workspaces } = groupReposByKind([board('AAAA')]);
    expect(prefixes(git)).toEqual(['AAAA']);
    expect(workspaces).toEqual([]);
  });

  it('does not mutate its input array', () => {
    const boards = [board('WKSP', 'workspace'), board('AAAA')];
    groupReposByKind(boards);
    expect(prefixes(boards)).toEqual(['WKSP', 'AAAA']);
  });
});
