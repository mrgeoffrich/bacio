import { describe, it, expect, vi, beforeEach } from 'vitest';
import { act, renderHook } from '@testing-library/react';
import { useRescueDispatch } from '../useRescueDispatch';
import * as api from '../../../api';

// useRescueDispatch owns the BACI-190 rescue flow lifted out of AgentsView.
// The suite stubs the `api` seam's rescueDispatch and exercises the happy
// path (refresh fired, in-flight cleared, no error), the failure path
// (per-dispatch error captured, refresh skipped), and the prior-error reset
// on retry.

vi.mock('../../../api', () => ({
  rescueDispatch: vi.fn(),
}));

const rescueDispatch = vi.mocked(api.rescueDispatch);

describe('useRescueDispatch', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('fires the refresh and clears in-flight on success', async () => {
    rescueDispatch.mockResolvedValue({} as Awaited<ReturnType<typeof api.rescueDispatch>>);
    const onRefresh = vi.fn();
    const { result } = renderHook(() => useRescueDispatch(onRefresh));

    await act(async () => {
      await result.current.rescue(842);
    });

    expect(rescueDispatch).toHaveBeenCalledWith(842);
    expect(onRefresh).toHaveBeenCalledTimes(1);
    expect(result.current.rescuing.has(842)).toBe(false);
    expect(result.current.rescueError[842]).toBeUndefined();
  });

  it('captures the error per-dispatch and skips the refresh on failure', async () => {
    rescueDispatch.mockRejectedValue(new Error('no idle supervisor'));
    const onRefresh = vi.fn();
    const { result } = renderHook(() => useRescueDispatch(onRefresh));

    await act(async () => {
      await result.current.rescue(842);
    });

    expect(onRefresh).not.toHaveBeenCalled();
    expect(result.current.rescueError[842]).toBe('no idle supervisor');
    expect(result.current.rescuing.has(842)).toBe(false);
  });

  it('clears a prior error for the same dispatch when re-rescued', async () => {
    rescueDispatch.mockRejectedValueOnce(new Error('first fail'));
    const { result } = renderHook(() => useRescueDispatch());

    await act(async () => {
      await result.current.rescue(842);
    });
    expect(result.current.rescueError[842]).toBe('first fail');

    rescueDispatch.mockResolvedValue({} as Awaited<ReturnType<typeof api.rescueDispatch>>);
    await act(async () => {
      await result.current.rescue(842);
    });
    expect(result.current.rescueError[842]).toBeUndefined();
  });

  it('tracks an in-flight dispatch until its call settles', async () => {
    let release: () => void = () => {};
    rescueDispatch.mockImplementation(
      () =>
        new Promise<Awaited<ReturnType<typeof api.rescueDispatch>>>((resolve) => {
          release = () => resolve({} as Awaited<ReturnType<typeof api.rescueDispatch>>);
        }),
    );
    const { result } = renderHook(() => useRescueDispatch());

    let pending!: Promise<void>;
    act(() => {
      pending = result.current.rescue(842);
    });
    expect(result.current.rescuing.has(842)).toBe(true);

    await act(async () => {
      release();
      await pending;
    });
    expect(result.current.rescuing.has(842)).toBe(false);
  });
});
