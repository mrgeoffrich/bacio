import { useCallback, useEffect, useRef, useState } from 'react';
import type { DependencyList, Dispatch, SetStateAction } from 'react';
import { reportError } from '../../errors';

// useAsyncResource is the canonical replacement for the
// `useState(initial) + useEffect(fetch) + .catch(reportError)` triad that was
// copy-pasted across ~26 self-fetching components and App's App-level
// refreshers (BACI-356). It owns the data / loading / error state, fetches
// eagerly on mount and whenever `deps` change (with a loading flicker), and
// hands back a manual `refresh()` plus the raw `setData` setter so an
// optimistic mutation can flip local state before the server confirms — the
// same shape the hand-rolled effects exposed, minus the boilerplate.
//
// Two correctness details the hand-rolled copies each re-implemented (and
// occasionally got wrong) live here once: a monotonic request sequence so a
// slow stale fetch can never clobber newer data (the `let cancelled = false`
// guard), and the latest-callback-ref pattern (Dan Abramov's, same as
// useInterval / useLocalStorage) so an inline `fetcher` / options object never
// churns the eager effect — it re-runs only on a real `deps` / `enabled`
// change, not when the parent re-renders with a fresh closure.

export type AsyncResourceOptions = {
  // Skip fetching (eager load + manual refresh) while false. `data` is left
  // as-is — callers that want to blank a disabled resource render their own
  // empty state (the "select a repo" placeholders already do). `loading`
  // drops to false. Flipping back to true re-runs the eager load.
  enabled?: boolean;
  // Modal headline passed to reportError when a load fails. Ignored when the
  // failure is silenced (`silent`) or a custom `onError` is supplied.
  errorHeadline?: string;
  // Route a failed load to console.warn instead of the error modal — the
  // established convention for best-effort polls and live badges, where a
  // flapping network over a sleeping laptop shouldn't spam the user.
  silent?: boolean;
  // Fully custom failure handler, replacing the reportError / console.warn
  // default (e.g. swallow entirely for a best-effort count the dropdown
  // surfaces failures for anyway).
  onError?: (err: unknown) => void;
};

export type AsyncResource<T> = {
  data: T;
  loading: boolean;
  error: unknown;
  // Re-fetch now. Pass `{ silent: true }` to log a failure instead of
  // routing it through the modal (the poll path uses this); omit for the
  // configured error policy. Never toggles the loading flicker — only the
  // eager (deps-change) load does, so a manual refresh doesn't flash a
  // skeleton over already-rendered data.
  refresh: (opts?: { silent?: boolean }) => void;
  // The raw state setter, for optimistic local updates before a mutation's
  // server round-trip confirms. The next load reconciles against the truth.
  setData: Dispatch<SetStateAction<T>>;
};

export function useAsyncResource<T>(
  fetcher: () => Promise<T>,
  initial: T,
  deps: DependencyList,
  options: AsyncResourceOptions = {},
): AsyncResource<T> {
  const { enabled = true, errorHeadline, silent = false, onError } = options;

  const [data, setData] = useState<T>(initial);
  const [loading, setLoading] = useState<boolean>(enabled);
  const [error, setError] = useState<unknown>(null);

  // Latest-value refs so the stable `run` / `refresh` callbacks and the eager
  // effect always read the freshest fetcher + error policy without listing
  // them as dependencies (which would re-arm everything every render).
  const fetcherRef = useRef(fetcher);
  const onErrorRef = useRef(onError);
  const headlineRef = useRef(errorHeadline);
  const silentRef = useRef(silent);
  useEffect(() => {
    fetcherRef.current = fetcher;
    onErrorRef.current = onError;
    headlineRef.current = errorHeadline;
    silentRef.current = silent;
  });

  // Monotonic request sequence: a load captures the value at its start and
  // commits only if it's still the most recent. A newer load (the eager
  // re-run on a deps change, a poll, a manual refresh) bumps the counter,
  // invalidating any fetch still in flight — so a slow response never
  // overwrites fresher data. A separate mounted ref drops a fetch that
  // resolves after unmount, when no newer load exists to supersede it.
  const seqRef = useRef(0);
  const mountedRef = useRef(true);
  useEffect(() => () => {
    mountedRef.current = false;
  }, []);

  // run() performs one fetch. `showLoading` drives the loading flicker (only
  // the eager load wants it; a silent poll / post-mutation refresh keep the
  // current data on screen). `perCallSilent` lets the poll path silence a
  // failure even when the hook's configured policy is to surface it.
  const run = useCallback((showLoading: boolean, perCallSilent: boolean) => {
    const seq = ++seqRef.current;
    if (showLoading) setLoading(true);
    fetcherRef.current()
      .then(result => {
        if (seq !== seqRef.current || !mountedRef.current) return;
        setData(result);
        setError(null);
        setLoading(false);
      })
      .catch(err => {
        if (seq !== seqRef.current || !mountedRef.current) return;
        setError(err);
        setLoading(false);
        if (onErrorRef.current) {
          onErrorRef.current(err);
        } else if (perCallSilent || silentRef.current) {
          console.warn(headlineRef.current ?? 'resource refresh failed', err);
        } else {
          reportError(err, headlineRef.current ? { headline: headlineRef.current } : {});
        }
      });
  }, []);

  const refresh = useCallback(
    (opts?: { silent?: boolean }) => run(false, opts?.silent ?? false),
    [run],
  );

  // Eager load on mount + whenever `deps` change, with the loading flicker.
  // Disabled resources (no board picked) skip the fetch and drop loading,
  // bumping the sequence so a load in flight when the resource is disabled
  // can't commit. A deps change re-runs this, and the fresh `run` supersedes
  // any earlier in-flight fetch.
  useEffect(() => {
    if (!enabled) {
      seqRef.current++;
      setLoading(false);
      return;
    }
    run(true, false);
    // `deps` is the caller-supplied dependency list (spread); `run` is stable.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [enabled, ...deps]);

  return { data, loading, error, refresh, setData };
}
