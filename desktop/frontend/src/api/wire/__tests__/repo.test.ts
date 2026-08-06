import { describe, it, expect } from 'vitest';
import { reshapeRepoActivity, normalizeRepoKind } from '../repo';

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

// The pivot: `kind` is a string-literal union in the contract, not a Wails
// enum, so exactly one function decides how the wire's free-form string
// narrows. Both transports call it.
describe('normalizeRepoKind', () => {
  it('passes through the two real discriminators', () => {
    expect(normalizeRepoKind('git')).toBe('git');
    expect(normalizeRepoKind('workspace')).toBe('workspace');
  });

  it("reads a legacy empty kind as 'git'", () => {
    expect(normalizeRepoKind('')).toBe('git');
  });

  it("falls back to 'git' for an absent or unknown kind", () => {
    // A mis-typed kind must never hide the Agentic Pipeline nav on a repo
    // that really is git-backed — 'git' is the safe default in both
    // directions.
    expect(normalizeRepoKind(undefined)).toBe('git');
    expect(normalizeRepoKind('Workspace')).toBe('git');
    expect(normalizeRepoKind('nonsense')).toBe('git');
  });
});
