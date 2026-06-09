import { reportError } from '../../errors';

// useOptimisticMutation encapsulates the snapshot → optimistic local update →
// `await api.*` → reconcile-on-success / rollback-on-failure → reportError
// dance that App's card handlers each hand-rolled (BACI-357). It owns no
// state of its own — the caller still owns `cards` (and drives it through
// `setCards`) — so the hook is a thin orchestrator that sequences the four
// moving parts in the right order and routes a failure through the central
// error bus, making the rollback impossible to forget.
//
// The optimistic update and the rollback are deliberately caller-supplied
// closures rather than threaded snapshot values: the App handlers capture
// their snapshot inside the functional `setCards(cs => …)` updater (so the
// read + write happen atomically against the latest cards) and the rollback
// reads that same closure variable. Keeping that shape intact means each
// migrated handler is the pre-hook code minus the `.then/.catch` boilerplate,
// not a rewrite.
//
// `useOptimisticToggle` is the simpler self-stateful sibling for the boolean
// display toggles; this is the general snapshot/rollback form.

export type OptimisticMutationConfig<R> = {
  // Apply the optimistic local update (e.g. `setCards(cs => …)`) and capture
  // whatever the rollback needs in a closure. Return `false` to abort the
  // whole mutation — a no-op guard (card not found, value already set):
  // nothing is persisted and no error is reported. Returning `true`/void
  // proceeds. Omit entirely for a mutation with no optimistic step (the
  // await-then-apply property toggles).
  optimisticUpdate?: () => boolean | void;
  // The server round-trip. Resolves with the value handed to `onSuccess`.
  persist: () => Promise<R>;
  // Revert the optimistic update on failure, reading the snapshot captured in
  // `optimisticUpdate`. Some sites reconcile via a silent refresh instead of
  // a manual revert — either is just a function here.
  rollback?: () => void;
  // Reconcile on success: a silent refresh, applying the server's returned
  // value, firing a follow-on side effect. Receives the persist result.
  onSuccess?: (result: R) => void;
  // reportError headline on failure — the default error policy. Ignored when
  // `onError` is supplied.
  errorHeadline?: string;
  // Replace the default reportError with a custom failure handler (e.g. a
  // silent swallow). Mirrors useAsyncResource's `onError`.
  onError?: (err: unknown) => void;
};

// runOptimisticMutation is the pure orchestrator (no React state), exported so
// it can be exercised directly in tests; components reach it through the
// `useOptimisticMutation` hook below.
export function runOptimisticMutation<R>(config: OptimisticMutationConfig<R>): void {
  const { optimisticUpdate, persist, rollback, onSuccess, errorHeadline, onError } = config;

  // Abort guard: a no-op optimistic update (nothing changed) skips the
  // round-trip entirely, matching the hand-rolled `if (prevCol === null) return`.
  if (optimisticUpdate && optimisticUpdate() === false) return;

  persist()
    .then(result => {
      onSuccess?.(result);
    })
    .catch(err => {
      // Revert before surfacing the error so the UI is back to truth by the
      // time the modal opens.
      rollback?.();
      if (onError) {
        onError(err);
      } else {
        reportError(err, errorHeadline ? { headline: errorHeadline } : {});
      }
    });
}

// useOptimisticMutation hands back the module-stable orchestrator. It is a hook
// (not a bare import) so call sites read as `const mutate = useOptimisticMutation()`
// alongside the other data hooks, and so it can grow per-call in-flight state
// later without touching any caller.
export function useOptimisticMutation(): typeof runOptimisticMutation {
  return runOptimisticMutation;
}
