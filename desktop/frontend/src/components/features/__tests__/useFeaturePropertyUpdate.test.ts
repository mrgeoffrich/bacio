import { describe, it, expect, vi, beforeEach } from 'vitest';
import { act, renderHook, waitFor } from '@testing-library/react';
import { useFeaturePropertyUpdate } from '../useFeaturePropertyUpdate';
import * as api from '../../../api';
import { subscribeErrors } from '../../../errors';
import type { ReportedError } from '../../../errors';
import type { FeatureDetail, FeatureSummary } from '../../../api';

// useFeaturePropertyUpdate bundles the set->api->reconcile->refresh->error
// dance the feature-property toggles each hand-rolled (BACI-363). The suite
// stubs the `api` seam's listFeatures (the summary-list refresh) and exercises
// the reconcile ordering, the await-then-apply update, and the error path.

vi.mock('../../../api', () => ({
  listFeatures: vi.fn(),
}));

function detail(over: Partial<FeatureDetail> = {}): FeatureDetail {
  return {
    slug: 'alpha',
    title: 'Alpha',
    description: '',
    emoji: '',
    branchName: '',
    state: 'active',
    stateManual: false,
    collectHandoffs: true,
    createdAt: '2026-01-01T00:00:00Z',
    updatedAt: '2026-01-01T00:00:00Z',
    issues: [],
    comments: [],
    documents: [],
    hiddenOnBoard: false,
    ...over,
  };
}

const summaries: FeatureSummary[] = [
  { slug: 'alpha', title: 'Alpha', emoji: '', state: 'done', branchName: '', updatedAt: '2026-01-01T00:00:00Z', hiddenOnBoard: false },
];

function captureReports(): { reports: ReportedError[]; stop: () => void } {
  const reports: ReportedError[] = [];
  const stop = subscribeErrors((e) => reports.push(e));
  return { reports, stop };
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(api.listFeatures).mockResolvedValue(summaries);
});

describe('useFeaturePropertyUpdate', () => {
  it('reconcile applies the detail, runs `after`, then refreshes the summary list — in that order', async () => {
    const onDetailChange = vi.fn();
    const onFeaturesChange = vi.fn();
    const calls: string[] = [];
    onDetailChange.mockImplementation(() => calls.push('detail'));
    const after = vi.fn(() => calls.push('after'));

    const { result } = renderHook(() =>
      useFeaturePropertyUpdate('BACI', onDetailChange, onFeaturesChange),
    );

    const updated = detail({ state: 'done' });
    act(() => result.current.reconcile(updated, after));

    expect(onDetailChange).toHaveBeenCalledWith(updated);
    expect(after).toHaveBeenCalledTimes(1);
    // detail + after are synchronous and ordered; the list refresh is async.
    expect(calls).toEqual(['detail', 'after']);

    await waitFor(() => expect(onFeaturesChange).toHaveBeenCalledWith(summaries));
    expect(api.listFeatures).toHaveBeenCalledWith('BACI');
  });

  it('update persists, then reconciles the detail + refreshes on success', async () => {
    const onDetailChange = vi.fn();
    const onFeaturesChange = vi.fn();
    const updated = detail({ collectHandoffs: false });
    const persist = vi.fn().mockResolvedValue(updated);

    const { result } = renderHook(() =>
      useFeaturePropertyUpdate('BACI', onDetailChange, onFeaturesChange),
    );

    act(() => result.current.update({ persist, errorHeadline: "Couldn't update" }));

    await waitFor(() => expect(onDetailChange).toHaveBeenCalledWith(updated));
    expect(persist).toHaveBeenCalledTimes(1);
    await waitFor(() => expect(onFeaturesChange).toHaveBeenCalledWith(summaries));
  });

  it('update reports a failed persist through the error bus and leaves the detail untouched', async () => {
    const onDetailChange = vi.fn();
    const onFeaturesChange = vi.fn();
    const persist = vi.fn().mockRejectedValue(new Error('boom'));
    const { reports, stop } = captureReports();

    const { result } = renderHook(() =>
      useFeaturePropertyUpdate('BACI', onDetailChange, onFeaturesChange),
    );

    await act(async () => {
      result.current.update({ persist, errorHeadline: "Couldn't update state" });
      await Promise.resolve();
    });

    await waitFor(() =>
      expect(reports.some((r) => r.headline === "Couldn't update state")).toBe(true),
    );
    expect(onDetailChange).not.toHaveBeenCalled();
    expect(onFeaturesChange).not.toHaveBeenCalled();
    stop();
  });
});
