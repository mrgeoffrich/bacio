import { describe, it, expect } from 'vitest';
import { renderHook } from '@testing-library/react';
import { useFeatureFiltering } from '../useFeatureFiltering';
import type { FeatureSummary } from '../../../api';

// useFeatureFiltering is the pure list-derivation core lifted out of
// FeaturesView (BACI-363): the optional ?mock=1 append, the per-state chip
// counts, and the visible list after the state filter + free-text search.
// These tests pin the counts and the visible-list narrowing the left rail
// depends on.

function feat(over: Partial<FeatureSummary> = {}): FeatureSummary {
  return {
    slug: 'alpha',
    title: 'Alpha feature',
    emoji: '',
    state: 'active',
    branchName: '',
    updatedAt: new Date().toISOString(),
    hiddenOnBoard: false,
    ...over,
  };
}

const seed: FeatureSummary[] = [
  feat({ slug: 'alpha', title: 'Alpha feature', state: 'active' }),
  feat({ slug: 'beta', title: 'Beta build', state: 'active' }),
  feat({ slug: 'gamma', title: 'Gamma done', state: 'done' }),
];

describe('useFeatureFiltering', () => {
  it('counts each state population plus the total', () => {
    const { result } = renderHook(() =>
      useFeatureFiltering(seed, false, 'all', ''),
    );
    expect(result.current.counts).toEqual({ all: 3, active: 2, done: 1, cancelled: 0 });
  });

  it('treats a missing state as active in the counts', () => {
    const { result } = renderHook(() =>
      useFeatureFiltering([feat({ state: '' })], false, 'all', ''),
    );
    expect(result.current.counts.active).toBe(1);
    expect(result.current.counts.all).toBe(1);
  });

  it('all shows the full population; a state filter narrows it', () => {
    const all = renderHook(() => useFeatureFiltering(seed, false, 'all', ''));
    expect(all.result.current.visible.map((f) => f.slug)).toEqual(['alpha', 'beta', 'gamma']);

    const done = renderHook(() => useFeatureFiltering(seed, false, 'done', ''));
    expect(done.result.current.visible.map((f) => f.slug)).toEqual(['gamma']);
  });

  it('search narrows by title or slug, case-insensitively, after the state filter', () => {
    const byTitle = renderHook(() => useFeatureFiltering(seed, false, 'all', 'BETA'));
    expect(byTitle.result.current.visible.map((f) => f.slug)).toEqual(['beta']);

    const bySlug = renderHook(() => useFeatureFiltering(seed, false, 'all', 'gam'));
    expect(bySlug.result.current.visible.map((f) => f.slug)).toEqual(['gamma']);

    // Counts ignore search — they reflect the unfiltered state population.
    expect(bySlug.result.current.counts.all).toBe(3);
  });

  it('appends the mock features (one cancelled, two active) when showMock is on', () => {
    const { result } = renderHook(() =>
      useFeatureFiltering(seed, true, 'all', ''),
    );
    expect(result.current.counts.all).toBe(6);
    expect(result.current.counts.cancelled).toBe(1);
    expect(result.current.counts.active).toBe(4);
    expect(result.current.allFeatures.some((f) => f.slug === 'mock-cancelled-spike')).toBe(true);
  });
});
