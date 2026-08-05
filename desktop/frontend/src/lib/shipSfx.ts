// useShipSfx — the React facade over the Web Audio ship-sound engine
// (BACI-240, rebuilt on Web Audio in BACI-375). All of the state lives in
// shipSfxEngine.ts; this file exists to (a) keep the Vite-only asset
// import out of the Node-importable engine and (b) expose the engine to
// React with the same `{ play }` shape CardsProvider already consumes.
//
// The sound fires when the Pipeline Shipping column's Shipped count
// genuinely rolls up (BACI-295's count-rise effect in CardsProvider) —
// a ship usually has NO gesture of its own, since an agent or another
// machine moves the card and the count arrives on a poll. That is why
// the engine unlocks eagerly on the user's first click rather than
// trying to play inside the ship event.
//
// BACI-295: `prefers-reduced-motion` is not a gate — that preference is
// about animation, not audio. The ship sound fires whenever the toggle
// is on; the visual flight + odometer roll honour reduced-motion
// separately.
//
// The autoplay contract (Chromium's origin-level sticky activation vs
// WebKit's per-context grant) and the BACI-375 bug it caused are
// documented in shipSfxEngine.ts's header — read that before touching
// the unlock path.

import { useCallback, useEffect, useSyncExternalStore } from 'react';
// Vite resolves binary asset imports to a URL string at build time.
// The bundle ships the MP3 under `/assets/kaching-<hash>.mp3`
// and we get back the resolved URL synchronously.
import shippedKaChingURL from '../assets/kaching.mp3';
import { shouldPlayShipSfx } from './shipSfxGate';
import type { ShipSfxState, ShipSfxStatus } from './shipSfxGate';
import {
  armShipSfx,
  getShipSfxStatus,
  playShipSfx,
  setShipSfxEnabled,
  subscribeShipSfxStatus,
} from './shipSfxEngine';

export { shouldPlayShipSfx };
export type { ShipSfxState, ShipSfxStatus };

export type UseShipSfxResult = {
  // play attempts a single ka-ching playback. No-op on every failure
  // mode (disabled / autoplay-locked / Web Audio unavailable / sound
  // still loading). Always returns synchronously; never throws.
  play: () => void;
};

// useShipSfx pushes the live `enabled` flag into the engine and returns a
// stable `play`. The reference must stay stable — CardsProvider's
// count-rise effect lists it as a dependency.
export function useShipSfx({ enabled }: { enabled: boolean }): UseShipSfxResult {
  useEffect(() => { setShipSfxEnabled(enabled); }, [enabled]);
  useEffect(() => { armShipSfx(shippedKaChingURL); }, []);
  const play = useCallback(() => { playShipSfx(); }, []);
  return { play };
}

// useShipSfxStatus subscribes the Settings pane to the engine's live
// status so a quiet ship sound is diagnosable rather than mysterious
// (BACI-375). The engine caches its snapshot object, so the identity is
// stable between genuine changes — required by useSyncExternalStore.
export function useShipSfxStatus(): ShipSfxStatus {
  return useSyncExternalStore(subscribeShipSfxStatus, getShipSfxStatus, getShipSfxStatus);
}
