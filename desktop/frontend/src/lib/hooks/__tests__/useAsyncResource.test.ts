import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { act, renderHook, waitFor } from '@testing-library/react';
import { useAsyncResource } from '../useAsyncResource';
import { subscribeErrors } from '../../../errors';
import type { ReportedError } from '../../../errors';

// A promise the test resolves/rejects by hand — lets us assert the loading
// flicker and the stale-load guard at points the fetch is mid-flight.
function deferred<T>() {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

// Capture everything pushed onto the central error bus for the duration of a
// test, so the default reportError path can be asserted without mocking the
// module.
function captureReports(): { reports: ReportedError[]; stop: () => void } {
  const reports: ReportedError[] = [];
  const stop = subscribeErrors(e => reports.push(e));
  return { reports, stop };
}

describe('useAsyncResource', () => {
  let warnSpy: ReturnType<typeof vi.spyOn>;
  beforeEach(() => {
    // Silence the silent-branch console.warn so the test output stays clean,
    // while still letting tests assert it fired.
    warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {});
  });
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('starts at the initial value, flickers loading, then commits the result', async () => {
    const d = deferred<number[]>();
    const { result } = renderHook(() =>
      useAsyncResource<number[]>(() => d.promise, [], []),
    );

    expect(result.current.data).toEqual([]);
    expect(result.current.loading).toBe(true); // eager load in flight

    await act(async () => {
      d.resolve([1, 2, 3]);
    });

    expect(result.current.data).toEqual([1, 2, 3]);
    expect(result.current.loading).toBe(false);
    expect(result.current.error).toBeNull();
  });

  it('re-fetches with a loading flicker when deps change', async () => {
    const { result, rerender } = renderHook(
      ({ id }) => useAsyncResource(() => Promise.resolve(`v${id}`), 'init', [id]),
      { initialProps: { id: 1 } },
    );

    await waitFor(() => expect(result.current.data).toBe('v1'));

    rerender({ id: 2 });
    expect(result.current.loading).toBe(true); // deps change re-arms the flicker
    await waitFor(() => expect(result.current.data).toBe('v2'));
    expect(result.current.loading).toBe(false);
  });

  it('routes a failed load through reportError with the configured headline', async () => {
    const { reports, stop } = captureReports();
    const d = deferred<string>();
    const { result } = renderHook(() =>
      useAsyncResource(() => d.promise, '', [], { errorHeadline: "Couldn't load" }),
    );

    await act(async () => {
      d.reject(new Error('boom'));
    });

    expect(reports.some(r => r.headline === "Couldn't load")).toBe(true);
    expect(result.current.error).toBeInstanceOf(Error);
    expect(result.current.loading).toBe(false);
    stop();
  });

  it('silent: logs the failure to console.warn instead of the error modal', async () => {
    const { reports, stop } = captureReports();
    const d = deferred<string>();
    renderHook(() =>
      useAsyncResource(() => d.promise, '', [], { silent: true, errorHeadline: 'H' }),
    );

    await act(async () => {
      d.reject(new Error('x'));
    });

    expect(warnSpy).toHaveBeenCalled();
    expect(reports).toHaveLength(0); // nothing reached the modal bus
    stop();
  });

  it('onError: a custom handler replaces the default reportError/console.warn', async () => {
    const onError = vi.fn();
    const { reports, stop } = captureReports();
    const d = deferred<string>();
    renderHook(() =>
      useAsyncResource(() => d.promise, '', [], { onError, errorHeadline: 'H' }),
    );

    await act(async () => {
      d.reject(new Error('x'));
    });

    expect(onError).toHaveBeenCalledTimes(1);
    expect(reports).toHaveLength(0);
    expect(warnSpy).not.toHaveBeenCalled();
    stop();
  });

  it('refresh() re-fetches without a loading flicker', async () => {
    let calls = 0;
    const { result } = renderHook(() =>
      useAsyncResource(() => Promise.resolve(++calls), 0, []),
    );

    await waitFor(() => expect(result.current.data).toBe(1));

    act(() => result.current.refresh());
    expect(result.current.loading).toBe(false); // manual refresh keeps data on screen
    await waitFor(() => expect(result.current.data).toBe(2));
  });

  it('enabled:false skips the fetch and stays idle; flipping true loads', async () => {
    const fetcher = vi.fn(() => Promise.resolve('v'));
    const { result, rerender } = renderHook(
      ({ on }) => useAsyncResource(fetcher, 'init', [], { enabled: on }),
      { initialProps: { on: false } },
    );

    expect(result.current.loading).toBe(false);
    expect(result.current.data).toBe('init');
    expect(fetcher).not.toHaveBeenCalled();

    rerender({ on: true });
    await waitFor(() => expect(result.current.data).toBe('v'));
    expect(fetcher).toHaveBeenCalledTimes(1);
  });

  it('a stale load cannot clobber newer data after a deps change', async () => {
    const slow = deferred<string>();
    const fast = deferred<string>();
    const { result, rerender } = renderHook(
      ({ id }) => useAsyncResource(() => (id === 1 ? slow.promise : fast.promise), 'init', [id]),
      { initialProps: { id: 1 } },
    );

    // Switch deps before the first (slow) fetch resolves, then let the new
    // fetch land first.
    rerender({ id: 2 });
    await act(async () => {
      fast.resolve('fast');
    });
    expect(result.current.data).toBe('fast');

    // The superseded slow fetch resolves late — the sequence guard drops it.
    await act(async () => {
      slow.resolve('slow');
    });
    expect(result.current.data).toBe('fast');
  });

  it('refresh() is a no-op while the resource is disabled', async () => {
    const fetcher = vi.fn(() => Promise.resolve('v'));
    const { result, rerender } = renderHook(
      ({ on }) => useAsyncResource(fetcher, 'init', [], { enabled: on }),
      { initialProps: { on: false } },
    );

    act(() => result.current.refresh());
    expect(fetcher).not.toHaveBeenCalled(); // disabled → guarded out

    rerender({ on: true });
    await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(1)); // eager load on enable
    act(() => result.current.refresh());
    expect(fetcher).toHaveBeenCalledTimes(2); // now it fetches
  });

  it('exposes setData for an optimistic local update', async () => {
    const { result } = renderHook(() =>
      useAsyncResource<string[]>(() => Promise.resolve(['a']), [], []),
    );
    await waitFor(() => expect(result.current.data).toEqual(['a']));

    act(() => result.current.setData(['optimistic']));
    expect(result.current.data).toEqual(['optimistic']);
  });
});
