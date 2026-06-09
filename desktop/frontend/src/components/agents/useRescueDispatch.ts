import { useState } from 'react';
import * as api from '../../api';

// RescueState is the surface useRescueDispatch hands AgentsView / the
// dispatch section: the in-flight + error bookkeeping the Rescue button
// reads, plus the handler it fires.
export type RescueState = {
  // BACI-190 rescue: per-dispatch in-flight set so a double-click on the
  // Rescue button doesn't fire twice. A dispatch id stays in the set until
  // its rescueDispatch call settles.
  rescuing: Set<number>;
  // BACI-190 rescue: most recent rescue error per dispatch so a failure
  // (no idle supervisor / already-acked race) is visible inline without a
  // toast surface. Map: dispatch id → error message.
  rescueError: Record<number, string>;
  rescue: (dispatchID: number) => Promise<void>;
};

// useRescueDispatch owns the BACI-190 rescue flow lifted out of AgentsView:
// it posts a rescue dispatch for a dead worker's stuck dispatch, tracks the
// per-dispatch in-flight + error state the card reads, and triggers the
// caller's refresh on success so the next agents poll clears the NeedsRescue
// flag (a fresh rescue dispatch lands on a different session — the original
// dead-session dispatch keeps its flag until acked).
export function useRescueDispatch(onRefresh?: () => void): RescueState {
  const [rescuing, setRescuing] = useState<Set<number>>(() => new Set());
  const [rescueError, setRescueError] = useState<Record<number, string>>(() => ({}));

  const rescue = async (dispatchID: number) => {
    setRescuing((prev) => {
      const next = new Set(prev);
      next.add(dispatchID);
      return next;
    });
    setRescueError((prev) => {
      if (!(dispatchID in prev)) return prev;
      const next = { ...prev };
      delete next[dispatchID];
      return next;
    });
    try {
      await api.rescueDispatch(dispatchID);
      onRefresh?.();
    } catch (err) {
      setRescueError((prev) => ({
        ...prev,
        [dispatchID]: err instanceof Error ? err.message : String(err),
      }));
    } finally {
      setRescuing((prev) => {
        const next = new Set(prev);
        next.delete(dispatchID);
        return next;
      });
    }
  };

  return { rescuing, rescueError, rescue };
}
