// useShipSfx (BACI-240) owns the lazy-loaded HTMLAudioElement that the
// topbar Shipped pill plays on a genuine ship. Exposes a single
// `play()` method gated on:
//
//   - the caller's `enabled` arg (the ui.shipped_sfx user setting);
//   - the OS-level `prefers-reduced-motion` media query;
//   - the browser's autoplay policy — Audio.play() returns a Promise
//     that rejects when the page has not yet had a user gesture, and
//     we swallow that case silently. We never call console.error here
//     because the chip is not a critical path — a silent SFX failure
//     is the right shape.
//
// The Audio element is created lazily on the first enabled play() so
// disabled-by-default sessions never spend the decode cost. Back-to-
// back plays reset currentTime to 0 so the previous play stops cleanly
// rather than overlapping (the brief says "single shared instance,
// either overlap or skip — we skip").
//
// Companion: shipFlourish.ts owns the "did a card just transition into
// done" diff. ShippedPopover wires the flash-edge → useShipSfx.play()
// so the audio is in lockstep with the existing border flash.

import { useCallback, useEffect, useRef } from 'react';
// Vite resolves binary asset imports to a URL string at build time.
// The bundle ships the MP3 under `/assets/kaching-<hash>.mp3`
// and we get back the resolved URL synchronously.
import shippedKaChingURL from '../assets/kaching.mp3';
import { shouldPlayShipSfx } from './shipSfxGate';

export { shouldPlayShipSfx };

export type UseShipSfxResult = {
  // play attempts a single ka-ching playback. No-op on every failure
  // mode (disabled / reduced-motion / autoplay-denied / unavailable
  // Audio constructor). Always returns synchronously; never throws.
  play: () => void;
};

// useShipSfx returns a `{ play }` shape that's safe to call from any
// effect. Caller passes the live `enabled` flag from props/state; the
// hook re-reads it on every play() so a flip lands without remounting.
//
// We deliberately do NOT create the Audio element until the first
// enabled play() — `new Audio(url)` performs the decode synchronously
// in some engines, so deferring it keeps the disabled-by-default user
// from paying for an asset they don't want.
export function useShipSfx({ enabled }: { enabled: boolean }): UseShipSfxResult {
  // Track the enabled flag in a ref so the play() callback can read
  // the latest value without becoming a fresh function reference on
  // every flip (which would force the consuming effect to re-run).
  const enabledRef = useRef(enabled);
  useEffect(() => { enabledRef.current = enabled; }, [enabled]);

  // The lazy-loaded HTMLAudioElement. Null until the first play()
  // that actually fires (passes all gates).
  const audioRef = useRef<HTMLAudioElement | null>(null);

  const play = useCallback(() => {
    // SSR / Node test guard: window is undefined in non-browser envs.
    if (typeof window === 'undefined') return;

    const prefersReducedMotion = !!(
      window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches
    );
    const audioCtor = (typeof Audio !== 'undefined') ? Audio : undefined;
    if (!shouldPlayShipSfx(enabledRef.current, prefersReducedMotion, audioCtor)) return;

    // Lazy-load the Audio element on first play. `new Audio(url)`
    // throws synchronously if the URL is bogus; swallow that case
    // for the same reason we swallow autoplay rejections.
    if (audioRef.current === null) {
      try {
        audioRef.current = new audioCtor!(shippedKaChingURL);
        // Volume halved — UI dings are easy to misjudge at full level.
        audioRef.current.volume = 0.5;
      } catch {
        // Constructor failure: leave audioRef null so the next play
        // tries again (an asset-load race is plausible; permanent
        // failure stays harmlessly silent).
        return;
      }
    }

    const el = audioRef.current!;
    // currentTime reset → restart cleanly on back-to-back ships.
    try { el.currentTime = 0; } catch { /* some engines refuse pre-load — fine */ }
    const result = el.play();
    if (result && typeof result.then === 'function') {
      // Autoplay-denied / NotAllowedError / NotSupportedError —
      // all swallowed. We never await the promise; an unhandled
      // rejection is the only thing to dodge here.
      result.catch(() => { /* silent */ });
    }
  }, []);

  return { play };
}
