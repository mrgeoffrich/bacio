import { describe, it, expect, afterEach, vi } from 'vitest';
import { act, renderHook, waitFor } from '@testing-library/react';
import { useOptimisticToggle } from '../useOptimisticToggle';
import { subscribeErrors } from '../../../errors';
import type { ReportedError } from '../../../errors';

function deferred<T>() {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function captureReports(): { reports: ReportedError[]; stop: () => void } {
  const reports: ReportedError[] = [];
  const stop = subscribeErrors(e => reports.push(e));
  return { reports, stop };
}

describe('useOptimisticToggle', () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('flips optimistically and keeps the new value when the persist succeeds', async () => {
    const persist = vi.fn(() => Promise.resolve());
    const { result } = renderHook(() => useOptimisticToggle(false, persist));

    act(() => result.current.toggle());
    expect(result.current.value).toBe(true); // optimistic flip, before persist settles
    expect(persist).toHaveBeenCalledWith(true);

    await waitFor(() => expect(persist).toHaveBeenCalledTimes(1));
    expect(result.current.value).toBe(true); // stays flipped on success
  });

  it('reverts the optimistic flip when the persist rejects (silent by default)', async () => {
    const { reports, stop } = captureReports();
    const d = deferred<void>();
    const { result } = renderHook(() => useOptimisticToggle(false, () => d.promise));

    act(() => result.current.toggle());
    expect(result.current.value).toBe(true); // optimistic

    await act(async () => {
      d.reject(new Error('PUT failed'));
      await d.promise.catch(() => {});
    });

    expect(result.current.value).toBe(false); // reverted
    expect(reports).toHaveLength(0); // silent — no modal without a headline
    stop();
  });

  it('reports the failure through the error bus when an errorHeadline is given', async () => {
    const { reports, stop } = captureReports();
    const d = deferred<void>();
    const { result } = renderHook(() =>
      useOptimisticToggle(false, () => d.promise, { errorHeadline: "Couldn't save" }),
    );

    act(() => result.current.set(true));
    await act(async () => {
      d.reject(new Error('x'));
      await d.promise.catch(() => {});
    });

    expect(result.current.value).toBe(false); // reverted
    expect(reports.some(r => r.headline === "Couldn't save")).toBe(true);
    stop();
  });

  it('set() to the current value is a no-op (no persist)', () => {
    const persist = vi.fn(() => Promise.resolve());
    const { result } = renderHook(() => useOptimisticToggle(true, persist));

    act(() => result.current.set(true));
    expect(persist).not.toHaveBeenCalled();
  });

  it('setValue seeds the value without persisting', () => {
    const persist = vi.fn(() => Promise.resolve());
    const { result } = renderHook(() => useOptimisticToggle(false, persist));

    act(() => result.current.setValue(true));
    expect(result.current.value).toBe(true);
    expect(persist).not.toHaveBeenCalled(); // seeding bypasses persist
  });

  it('tolerates a synchronous (non-promise) persist return', async () => {
    const persist = vi.fn((next: boolean) => `saved-${next}`);
    const { result } = renderHook(() => useOptimisticToggle(false, persist));

    act(() => result.current.toggle());
    expect(result.current.value).toBe(true);
    await waitFor(() => expect(persist).toHaveBeenCalledWith(true));
    expect(result.current.value).toBe(true); // no throw, stays flipped
  });
});
