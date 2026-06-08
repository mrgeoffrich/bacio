import type { DependencyList } from 'react';
import { useAsyncResource } from './useAsyncResource';
import type { AsyncResource, AsyncResourceOptions } from './useAsyncResource';
import { useInterval, POLL_INTERVAL_MS } from './useInterval';

// usePolledResource is useAsyncResource with a background poll bolted on — the
// other half of the BACI-356 data-hook pair, for the many self-fetching views
// that load eagerly *and* re-poll on the shared 10s cadence (the Board /
// Agents / Monitor panels, the Transcripts list, the live count badges). It
// composes the two existing primitives: useAsyncResource owns the eager load +
// data/loading/error + manual refresh, and useInterval drives the silent poll.
//
// The eager load (on mount / `deps` change) routes a failure through the error
// modal per the configured policy; the poll is *always* silent (it calls
// refresh({ silent: true })) so a flapping network can't fan a transient
// failure into a stack of modals. `enabled` gates both halves — it tears the
// timer down and skips the eager fetch — so a resource that isn't ready yet
// (no board picked) never arms a timer that would only no-op.
export type PolledResourceOptions = AsyncResourceOptions & {
  // Poll cadence; defaults to the app-wide POLL_INTERVAL_MS so every live
  // readout stays in lockstep with the server's elector heartbeat.
  intervalMs?: number;
};

export function usePolledResource<T>(
  fetcher: () => Promise<T>,
  initial: T,
  deps: DependencyList,
  options: PolledResourceOptions = {},
): AsyncResource<T> {
  const { intervalMs = POLL_INTERVAL_MS, enabled = true, ...rest } = options;

  const resource = useAsyncResource(fetcher, initial, deps, { ...rest, enabled });

  // Silent background poll while enabled. useInterval's latest-callback-ref
  // means the stable `refresh` reads the current fetcher on each tick without
  // the timer ever being re-armed by a parent re-render.
  useInterval(() => resource.refresh({ silent: true }), intervalMs, enabled);

  return resource;
}
