import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { act, renderHook } from '@testing-library/react';
import { usePolledResource } from '../usePolledResource';
import { POLL_INTERVAL_MS } from '../useInterval';
import { subscribeErrors } from '../../../errors';
import type { ReportedError } from '../../../errors';

// usePolledResource bolts a background poll onto useAsyncResource, so the suite
// drives Vitest's fake timers (like the useInterval suite) and flushes the
// fetch microtasks between ticks. waitFor is avoided here — its internal timer
// would be faked too and hang — in favour of explicit act + advanceTimersByTime.
async function flush(): Promise<void> {
  // Let the eager / poll fetch's resolved .then chain commit before asserting.
  await act(async () => {
    await Promise.resolve();
  });
}

describe('usePolledResource', () => {
  let warnSpy: ReturnType<typeof vi.spyOn>;
  beforeEach(() => {
    vi.useFakeTimers();
    warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {});
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it('loads eagerly, then re-fetches silently on each interval', async () => {
    let calls = 0;
    const { result } = renderHook(() =>
      usePolledResource(() => Promise.resolve(++calls), 0, [], { intervalMs: 1000 }),
    );

    await flush(); // eager load
    expect(result.current.data).toBe(1);

    await act(async () => {
      vi.advanceTimersByTime(1000);
    });
    await flush();
    expect(result.current.data).toBe(2);

    await act(async () => {
      vi.advanceTimersByTime(2000);
    });
    await flush();
    expect(result.current.data).toBe(4); // two more ticks
  });

  it('defaults to the shared POLL_INTERVAL_MS cadence', async () => {
    let calls = 0;
    renderHook(() => usePolledResource(() => Promise.resolve(++calls), 0, []));

    await flush();
    expect(calls).toBe(1); // eager only

    await act(async () => {
      vi.advanceTimersByTime(POLL_INTERVAL_MS);
    });
    expect(calls).toBe(2); // one poll on the default cadence
  });

  it('the poll is silent: a poll failure logs, never reaching the error modal', async () => {
    const reports: ReportedError[] = [];
    const stop = subscribeErrors(e => reports.push(e));
    let shouldFail = false;
    const { result } = renderHook(() =>
      usePolledResource(
        () => (shouldFail ? Promise.reject(new Error('poll fail')) : Promise.resolve('ok')),
        'init',
        [],
        { intervalMs: 1000, errorHeadline: 'H' },
      ),
    );

    await flush(); // eager load succeeds
    expect(result.current.data).toBe('ok');

    shouldFail = true;
    await act(async () => {
      vi.advanceTimersByTime(1000);
    });
    await flush();

    expect(warnSpy).toHaveBeenCalled(); // silent poll → console.warn
    expect(reports).toHaveLength(0); // nothing reached the modal bus
    stop();
  });

  it('stops polling after unmount', async () => {
    let calls = 0;
    const { unmount } = renderHook(() =>
      usePolledResource(() => Promise.resolve(++calls), 0, [], { intervalMs: 1000 }),
    );

    await flush();
    expect(calls).toBe(1);

    await act(async () => {
      vi.advanceTimersByTime(1000);
    });
    expect(calls).toBe(2);

    unmount();
    await act(async () => {
      vi.advanceTimersByTime(5000);
    });
    expect(calls).toBe(2); // no further polls after the timer is torn down
  });

  it('enabled:false arms nothing; flipping on starts the eager load and the poll', async () => {
    const fetcher = vi.fn(() => Promise.resolve('v'));
    const { result, rerender } = renderHook(
      ({ on }) => usePolledResource(fetcher, 'init', [], { enabled: on, intervalMs: 1000 }),
      { initialProps: { on: false } },
    );

    await act(async () => {
      vi.advanceTimersByTime(3000);
    });
    expect(fetcher).not.toHaveBeenCalled(); // no eager load, no poll

    rerender({ on: true });
    await flush();
    expect(result.current.data).toBe('v');
    expect(fetcher).toHaveBeenCalledTimes(1); // eager load on enable

    await act(async () => {
      vi.advanceTimersByTime(1000);
    });
    expect(fetcher).toHaveBeenCalledTimes(2); // poll now armed
  });
});
