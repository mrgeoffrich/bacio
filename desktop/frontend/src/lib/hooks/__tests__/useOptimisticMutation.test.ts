import { describe, it, expect, afterEach, vi } from 'vitest';
import { renderHook } from '@testing-library/react';
import { runOptimisticMutation, useOptimisticMutation } from '../useOptimisticMutation';
import { subscribeErrors } from '../../../errors';
import type { ReportedError } from '../../../errors';

// A promise the test resolves/rejects by hand so the optimistic-apply window
// (after apply, before the persist settles) can be asserted.
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

describe('runOptimisticMutation', () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('applies the optimistic update, then reconciles on success', async () => {
    const order: string[] = [];
    const d = deferred<string>();
    runOptimisticMutation({
      optimisticUpdate: () => { order.push('apply'); },
      persist: () => d.promise,
      onSuccess: result => { order.push(`success:${result}`); },
      rollback: () => { order.push('rollback'); },
      errorHeadline: 'H',
    });

    // The optimistic update fires synchronously, before the persist settles.
    expect(order).toEqual(['apply']);

    d.resolve('srv');
    await d.promise;
    await Promise.resolve(); // let the .then microtask run

    expect(order).toEqual(['apply', 'success:srv']);
  });

  it('rolls back and reports the error on failure', async () => {
    const { reports, stop } = captureReports();
    const order: string[] = [];
    const d = deferred<string>();
    runOptimisticMutation({
      optimisticUpdate: () => { order.push('apply'); },
      persist: () => d.promise,
      onSuccess: () => { order.push('success'); },
      rollback: () => { order.push('rollback'); },
      errorHeadline: "Couldn't move card",
    });

    d.reject(new Error('boom'));
    await d.promise.catch(() => {});
    await Promise.resolve();

    expect(order).toEqual(['apply', 'rollback']); // never reaches success
    expect(reports.some(r => r.headline === "Couldn't move card")).toBe(true);
    stop();
  });

  it('aborts entirely when optimisticUpdate returns false', async () => {
    const persist = vi.fn(() => Promise.resolve('x'));
    runOptimisticMutation({
      optimisticUpdate: () => false,
      persist,
      errorHeadline: 'H',
    });
    await Promise.resolve();
    expect(persist).not.toHaveBeenCalled(); // no round-trip on a no-op guard
  });

  it('proceeds when optimisticUpdate is omitted (await-then-apply form)', async () => {
    const d = deferred<string>();
    const onSuccess = vi.fn();
    runOptimisticMutation({
      persist: () => d.promise,
      onSuccess,
    });
    d.resolve('updated');
    await d.promise;
    await Promise.resolve();
    expect(onSuccess).toHaveBeenCalledWith('updated');
  });

  it('onError replaces the default reportError', async () => {
    const { reports, stop } = captureReports();
    const onError = vi.fn();
    const d = deferred<string>();
    runOptimisticMutation({
      persist: () => d.promise,
      onError,
      errorHeadline: 'ignored when onError set',
    });
    d.reject(new Error('x'));
    await d.promise.catch(() => {});
    await Promise.resolve();
    expect(onError).toHaveBeenCalledTimes(1);
    expect(reports).toHaveLength(0); // nothing reached the modal bus
    stop();
  });

  it('useOptimisticMutation returns the stable orchestrator', () => {
    const { result, rerender } = renderHook(() => useOptimisticMutation());
    const first = result.current;
    rerender();
    expect(result.current).toBe(first); // stable identity across renders
    expect(result.current).toBe(runOptimisticMutation);
  });
});
