import { describe, it, expect } from 'vitest';
import { reshapeRepoActivity } from '../repo';

// BACI-369: the GET /repos/activity snake→camel reshape. Trivial by
// design, but the absent-timestamp case is the one the picker's
// comparator depends on staying `undefined` rather than becoming ''.

describe('reshapeRepoActivity', () => {
  it('maps every field', () => {
    expect(reshapeRepoActivity({
      prefix: 'BACI',
      last_activity_at: '2026-08-06T02:00:00Z',
      active_jobs: 2,
    })).toEqual({
      prefix: 'BACI',
      lastActivityAt: '2026-08-06T02:00:00Z',
      activeJobs: 2,
    });
  });

  it('leaves lastActivityAt undefined when the wire omits it', () => {
    const r = reshapeRepoActivity({ prefix: 'EMPT', active_jobs: 0 });
    expect(r.lastActivityAt).toBeUndefined();
    expect(r.activeJobs).toBe(0);
  });
});
