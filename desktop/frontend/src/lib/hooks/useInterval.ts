import { useEffect, useRef } from 'react';

// Shared poll cadence for the live readouts — Board / Agents / brief, the
// notification + shipped-count badges, the leader chip (web mode), the Sync
// registry, and the Monitor panels. 10s keeps the UI roughly in lockstep with
// the server's elector heartbeat without hammering it. Consolidated here
// (BACI-354) from the half-dozen per-file `const POLL_INTERVAL_MS = 10_000`
// copies that used to drift independently.
export const POLL_INTERVAL_MS = 10_000;

// useInterval runs `callback` every `delayMs` milliseconds while `enabled` is
// true, clearing the timer on unmount and whenever the delay or enabled flag
// changes. It follows Dan Abramov's latest-callback-ref pattern: the live
// callback is stashed in a ref refreshed on every render, so each tick reads
// the freshest closure *without* the timer being torn down and re-armed when
// the callback identity changes. A parent re-render (new inline callback,
// fresh captured state) therefore never resets the interval phase — only a real
// cadence change (`delayMs`) or a pause/resume (`enabled`) does.
//
// This is the canonical replacement for the hand-rolled
// `setInterval(..., POLL_INTERVAL_MS)` + `clearInterval` cleanup blocks that
// were copy-pasted across App.tsx and the Monitor / Sync panels, each of which
// risked a stale closure or a missed cleanup.
export function useInterval(
  callback: () => void,
  delayMs: number,
  enabled: boolean = true,
): void {
  const savedCallback = useRef(callback);

  // Keep the ref pointed at the latest callback. In an effect (not inline)
  // so the write is committed, not run during render.
  useEffect(() => {
    savedCallback.current = callback;
  }, [callback]);

  useEffect(() => {
    if (!enabled) return;
    const id = setInterval(() => savedCallback.current(), delayMs);
    return () => clearInterval(id);
  }, [delayMs, enabled]);
}
