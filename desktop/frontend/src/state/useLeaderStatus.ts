import { useEffect, useState } from 'react';
import { Events } from '@wailsio/runtime';
import * as api from '../api';
import type { LeaderStatusDTO } from '../api';
import { WEB_MODE } from '../env';
import { useInterval, POLL_INTERVAL_MS } from '../lib/hooks/useInterval';

// useLeaderStatus (BACI-361) owns the UI leader-election readout App.tsx used
// to carry. It is global (not board-keyed) and Topbar-local — the only
// consumer is the topbar crown chip — so it lives as a self-stateful hook
// rather than a context. The transport split moves verbatim: desktop mode
// reads the per-process LeaderService over the Wails Events bus (pushed each
// tick); web mode polls GET /leader on the standard 10s cadence (the server's
// elector heartbeat). Both seed the initial state on mount so the chip
// doesn't wait for the first interval.
export function useLeaderStatus(): LeaderStatusDTO {
  const [leaderState, setLeaderState] = useState<LeaderStatusDTO>({ amLeader: false, holderLabel: '' });

  useEffect(() => {
    api.getLeaderStatus().then(setLeaderState).catch(() => {});
    // Web mode polls via the useInterval below; desktop mode gets pushed
    // ticks over the Wails Events bus instead.
    if (WEB_MODE) return;
    const off = Events.On('leaderStatus', (e) => setLeaderState(e.data));
    return () => { if (typeof off === 'function') off(); };
  }, []);

  // Web mode has no Events bus, so poll GET /leader on the standard cadence.
  // Disabled (no timer armed) in desktop mode, where the push subscription
  // above keeps the chip live.
  useInterval(() => {
    api.getLeaderStatus().then(setLeaderState).catch(() => {});
  }, POLL_INTERVAL_MS, WEB_MODE);

  return leaderState;
}
