import { useMemo } from 'react';
import type { FeatureSummary } from '../../api';
import { mockFeatures } from './mockFeatures';

// FeatureFiltering is the derived view useFeatureFiltering hands the
// FeaturesView shell: the full population (real + optional mock), the
// per-state chip counts, and the visible list after the state + search
// filters are applied.
export type FeatureFiltering = {
  allFeatures: FeatureSummary[];
  counts: Record<string, number>;
  visible: FeatureSummary[];
};

// useFeatureFiltering owns the pure list derivations carved out of
// FeaturesView (BACI-363): the optional `?mock=1` append, the per-state
// counts that power the filter-chip badges, and the visible list after the
// state filter + free-text search. No effects, no fetches — every value is a
// memo over its inputs, so it's the testable core of the left rail.
export function useFeatureFiltering(
  features: FeatureSummary[],
  showMock: boolean,
  filter: string,
  search: string,
): FeatureFiltering {
  const allFeatures = useMemo(
    () => (showMock ? [...features, ...mockFeatures()] : features),
    [features, showMock],
  );

  // Per-state counts power the filter-chip badges so a quick glance
  // shows how the population breaks down.
  const counts = useMemo(() => {
    const acc: Record<string, number> = {
      all: allFeatures.length,
      active: 0,
      done: 0,
      cancelled: 0,
    };
    for (const f of allFeatures) {
      const s = f.state || 'active';
      if (acc[s] !== undefined) acc[s] += 1;
    }
    return acc;
  }, [allFeatures]);

  const visible = useMemo(() => {
    const byState =
      filter === 'all'
        ? allFeatures
        : allFeatures.filter((f) => (f.state || 'active') === filter);
    const q = search.trim().toLowerCase();
    if (!q) return byState;
    return byState.filter(
      (f) =>
        (f.title || '').toLowerCase().includes(q) ||
        (f.slug || '').toLowerCase().includes(q),
    );
  }, [allFeatures, filter, search]);

  return { allFeatures, counts, visible };
}
