import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { renderHook } from '@testing-library/react';
import { useInterval } from '../useInterval';

// useInterval is a timer primitive, so the whole suite drives Vitest's fake
// timers and asserts on call counts as virtual time advances.
describe('useInterval', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it('fires the callback once per delay while mounted', () => {
    const cb = vi.fn();
    renderHook(() => useInterval(cb, 1000));

    expect(cb).not.toHaveBeenCalled(); // no leading-edge fire
    vi.advanceTimersByTime(1000);
    expect(cb).toHaveBeenCalledTimes(1);
    vi.advanceTimersByTime(3000);
    expect(cb).toHaveBeenCalledTimes(4);
  });

  it('swaps to the latest callback without resetting the timer phase', () => {
    const first = vi.fn();
    const second = vi.fn();
    const { rerender } = renderHook(({ cb }) => useInterval(cb, 1000), {
      initialProps: { cb: first },
    });

    // Advance partway through the interval, then swap the callback.
    vi.advanceTimersByTime(600);
    rerender({ cb: second });

    // The timer keeps its phase: the tick lands at 1000ms total (not 1600ms),
    // and it is the *new* callback that runs — proving the swap didn't re-arm
    // the timer. Had it reset, nothing would have fired by 1000ms.
    vi.advanceTimersByTime(400);
    expect(first).not.toHaveBeenCalled();
    expect(second).toHaveBeenCalledTimes(1);
  });

  it('stops firing after unmount', () => {
    const cb = vi.fn();
    const { unmount } = renderHook(() => useInterval(cb, 1000));

    vi.advanceTimersByTime(1000);
    expect(cb).toHaveBeenCalledTimes(1);

    unmount();
    vi.advanceTimersByTime(5000);
    expect(cb).toHaveBeenCalledTimes(1); // no further fires after cleanup
  });

  it('respects enabled: stays idle while false, runs once re-enabled, stops when disabled again', () => {
    const cb = vi.fn();
    const { rerender } = renderHook(
      ({ enabled }) => useInterval(cb, 1000, enabled),
      { initialProps: { enabled: false } },
    );

    vi.advanceTimersByTime(5000);
    expect(cb).not.toHaveBeenCalled(); // disabled — no timer armed

    rerender({ enabled: true });
    vi.advanceTimersByTime(1000);
    expect(cb).toHaveBeenCalledTimes(1);

    rerender({ enabled: false });
    vi.advanceTimersByTime(5000);
    expect(cb).toHaveBeenCalledTimes(1); // disabling tears the timer down
  });
});
