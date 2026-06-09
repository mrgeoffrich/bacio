import { useCallback } from 'react';
import * as api from '../../api';
import type { FeatureDetail, FeatureSummary } from '../../api';
import { useOptimisticMutation } from '../../lib/hooks/useOptimisticMutation';

// UpdateOptions is one await-then-apply property mutation: the server
// round-trip, the failure headline, and an optional follow-on side effect
// run after the detail is reconciled but before the summary-list refresh.
export type UpdateOptions = {
  persist: () => Promise<FeatureDetail>;
  errorHeadline: string;
  after?: () => void;
};

// FeaturePropertyUpdate is the reconcile + mutate pair useFeaturePropertyUpdate
// hands the detail pane.
export type FeaturePropertyUpdate = {
  // Reconcile the server's returned FeatureDetail: apply it locally, run an
  // optional follow-on side effect (e.g. refreshing the board cards), then
  // best-effort refresh the summary list so the left-rail pills re-render.
  // Exposed for FeaturePropertyToggle, whose optimistic flip owns its own
  // persist promise and calls this on success.
  reconcile: (updated: FeatureDetail, after?: () => void) => void;
  // The await-then-apply property mutation: persist, then reconcile on
  // success, reporting a failure through the central error bus. Used by the
  // tri-state State toggle, which can't optimistic-flip a boolean; the boolean
  // toggles route through FeaturePropertyToggle's useOptimisticToggle instead.
  update: (opts: UpdateOptions) => void;
};

// useFeaturePropertyUpdate bundles the repeated set->api->reconcile->refresh->
// error dance the feature-property toggles each hand-rolled (BACI-357 /
// BACI-363). The mutation half threads through useOptimisticMutation — it owns
// the reportError-on-failure half — while reconcile applies the server's
// returned FeatureDetail and best-effort refreshes the summary list so the
// left-rail pills stay in sync without waiting for the next selection.
export function useFeaturePropertyUpdate(
  activeBoard: string,
  onDetailChange: (detail: FeatureDetail) => void,
  onFeaturesChange: (features: FeatureSummary[]) => void,
): FeaturePropertyUpdate {
  const mutate = useOptimisticMutation();

  // Best-effort summary-list refresh after a property change so the left-rail
  // pills re-render; a failure is non-fatal — the next selection refresh picks
  // it up (matches the pre-BACI-357 inner try/catch).
  const refreshFeatures = useCallback(() => {
    api.listFeatures(activeBoard).then(onFeaturesChange).catch(() => {});
  }, [activeBoard, onFeaturesChange]);

  const reconcile = useCallback(
    (updated: FeatureDetail, after?: () => void) => {
      onDetailChange(updated);
      after?.();
      refreshFeatures();
    },
    [onDetailChange, refreshFeatures],
  );

  const update = useCallback(
    (opts: UpdateOptions) => {
      mutate({
        persist: opts.persist,
        onSuccess: (updated) => reconcile(updated, opts.after),
        errorHeadline: opts.errorHeadline,
      });
    },
    [mutate, reconcile],
  );

  return { reconcile, update };
}
