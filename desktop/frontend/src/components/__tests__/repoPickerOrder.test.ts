import { describe, it, expect } from 'vitest';
import { activityByPrefix, rankRepos } from '../repoPickerOrder';
import type { Board, RepoActivity } from '../../api';

// BACI-369: the repo picker's ordering is the ticket, so it gets its own
// tests away from the DOM.

function board(prefix: string): Board {
  return {
    prefix,
    name: prefix.toLowerCase(),
    issueCount: 0,
    syncEnabled: false,
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
