import type { FeatureSummary } from '../../api';

// TEMP demo data — appended to the real feature list when ?mock=1 is
// present in the URL. Used to cover states (notably `cancelled`) the
// live data doesn't have so the filter strip can be exercised. Remove
// together with the showMock branch before merging.
//
// BACI-363: lifted out of FeaturesView so both useFeatureFiltering (the
// list append) and useFeatureDetail (the mock-slug short-circuit) read
// the same dataset.
export function mockFeatures(): FeatureSummary[] {
  const ago = (d: number) => new Date(Date.now() - d * 86_400_000).toISOString();
  return [
    {
      slug: 'mock-cancelled-spike',
      title: 'Spike on alternate sync transport (parked)',
      emoji: '🧪',
      state: 'cancelled',
      branchName: '',
      updatedAt: ago(12),
      hiddenOnBoard: false,
    },
    {
      slug: 'mock-active-redesign',
      title: 'Features view redesign + state filter',
      emoji: '🎨',
      state: 'active',
      branchName: '',
      updatedAt: ago(0),
      hiddenOnBoard: false,
    },
    {
      slug: 'mock-hidden-feature',
      title: 'Internal-only debug dashboard',
      emoji: '🛠',
      state: 'active',
      branchName: '',
      updatedAt: ago(3),
      hiddenOnBoard: true,
    },
  ];
}
