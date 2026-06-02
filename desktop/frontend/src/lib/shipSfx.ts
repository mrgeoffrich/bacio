// useShipSfx (BACI-240) owns the lazy-loaded HTMLAudioElement that
// sounds the ka-ching on a genuine ship (the Pipeline Shipping-column
// Shipped pill's count rolling up). Exposes a single
// `play()` method gated on:
//
//   - the caller's `enabled` arg (the ui.shipped_sfx user setting);
//   - the browser's autoplay policy — Audio.play() returns a Promise
//     that rejects when the element hasn't been activated by a user
//     gesture. We don't let that rejection escape, but (unlike the
//     original "stay silent" design) we now console.warn it: a
//     swallowed NotAllowedError is otherwise indistinguishable from
//     "the SFX never fired", which made a missing ka-ching undebuggable.
//     The chip is still non-critical — the warn is a breadcrumb, not an
//     error.
//
// Autoplay unlock: a genuine ship frequently has NO gesture of its own
// (an agent or another machine moves a card to `done`; the count rolls
// on a poll). The autoplay policy would swallow exactly those. So we
// prime the element on the first user pointerdown / keydown — a
// volume-0 play→pause inside that gesture marks it as user-activated and
// grants it permission for later gesture-less plays. This is what WebKit
// / the desktop webview needs; Chrome's sticky activation already covers
// most web cases.
//
// BACI-295: `prefers-reduced-motion` is no longer a gate — that
// preference is about animation, not audio. The ship sound fires
// whenever the toggle is on (autoplay policy permitting); the visual
// flight + odometer roll still honour reduced-motion separately.
//
// The Audio element is created lazily (the first enabled play() OR the
// first-gesture unlock, whichever comes first) so opted-out sessions
// never spend the decode cost. Back-to-back plays reset currentTime to
// 0 so the previous play stops cleanly rather than overlapping.
//
// Trigger: App.tsx's shippedCount-rise effect (BACI-295) calls `play()`
// whenever the server-derived Shipped count genuinely rises — the same
// signal that rolls the Pipeline pill's odometer. (Pre-BACI-295 this
// rode shipFlourish.ts's card→done diff via ShippedPopover's flash-edge;
// that's no longer how it's wired.)

import { useCallback, useEffect, useRef } from 'react';
// Vite resolves binary asset imports to a URL string at build time.
// The bundle ships the MP3 under `/assets/kaching-<hash>.mp3`
// and we get back the resolved URL synchronously.
import shippedKaChingURL from '../assets/kaching.mp3';
import { shouldPlayShipSfx, shouldProbeShipSfx } from './shipSfxGate';

export { shouldPlayShipSfx, shouldProbeShipSfx };

// Playback volume. Halved — UI dings are easy to misjudge at full level.
// play() re-asserts this on every fire so a real ka-ching is never left
// at the volume-0 the first-gesture unlock momentarily sets (the unlock
// and a ship can land on the same gesture — e.g. the dev Test +1 button).
const SHIP_SFX_VOLUME = 0.5;

export type UseShipSfxResult = {
  // play attempts a single ka-ching playback. No-op on every failure
  // mode (disabled / autoplay-denied / unavailable Audio constructor).
  // Always returns synchronously; never throws.
  play: () => void;
};

// useShipSfx returns a `{ play }` shape that's safe to call from any
// effect. Caller passes the live `enabled` flag from props/state; the
// hook re-reads it on every play() so a flip lands without remounting.
// A first-gesture listener (mounted once) unlocks the element so a later
// gesture-less ship still dings — see the file header.
export function useShipSfx({ enabled }: { enabled: boolean }): UseShipSfxResult {
  // Track the enabled flag in a ref so the play() callback can read
  // the latest value without becoming a fresh function reference on
  // every flip (which would force the consuming effect to re-run).
  const enabledRef = useRef(enabled);
  useEffect(() => { enabledRef.current = enabled; }, [enabled]);

  // The lazy-loaded HTMLAudioElement. Null until something first needs
  // it — the first enabled play(), the startup probe, or the
  // first-gesture unlock.
  const audioRef = useRef<HTMLAudioElement | null>(null);
  // Set once the element has been primed (by the startup probe, the
  // first-gesture unlock, whichever lands first).
  const unlockedRef = useRef(false);
  // BACI-336: ensures the once-only startup autoplay probe fires at most
  // once across the hook's lifetime, independent of the unlock guard.
  const probedRef = useRef(false);

  // ensureAudio lazily constructs the shared element. Returns null when
  // the Audio constructor is unreachable (non-browser env) or the
  // constructor throws (bogus URL / asset-load race — the next call
  // retries). `new Audio(url)` decodes synchronously in some engines, so
  // we defer it until something actually needs the sound.
  const ensureAudio = useCallback((): HTMLAudioElement | null => {
    if (audioRef.current) return audioRef.current;
    const audioCtor = (typeof Audio !== 'undefined') ? Audio : undefined;
    if (!audioCtor) return null;
    try {
      const el = new audioCtor(shippedKaChingURL);
      el.volume = SHIP_SFX_VOLUME;
      audioRef.current = el;
    } catch {
      audioRef.current = null;
    }
    return audioRef.current;
  }, []);

  // primeElement runs the volume-0 play→pause that marks the element as
  // user-activated (the first-gesture unlock) or registers autoplay
  // permission early (the startup probe). Inaudible (volume 0 — but NOT
  // muted, so it still counts as audio) and self-restoring. Swallows a
  // rejected play() so a strict-autoplay engine's refusal is a no-op
  // rather than an unhandled rejection.
  const primeElement = useCallback((el: HTMLAudioElement) => {
    const prevVolume = el.volume;
    el.volume = 0;
    const restore = () => {
      try { el.pause(); el.currentTime = 0; el.volume = prevVolume; }
      catch { /* fine */ }
    };
    let p: Promise<void> | undefined;
    try { p = el.play(); } catch { restore(); return; }
    if (p && typeof p.then === 'function') p.then(restore, restore);
    else restore();
  }, []);

  // First-gesture autoplay unlock — see the file header. A volume-0
  // play→pause inside the first pointerdown / keydown marks the element
  // as user-activated so a later gesture-less ship still dings.
  useEffect(() => {
    if (typeof window === 'undefined') return;
    const removeListeners = () => {
      window.removeEventListener('pointerdown', unlock, true);
      window.removeEventListener('keydown', unlock, true);
    };
    function unlock() {
      if (unlockedRef.current) return;
      if (!enabledRef.current) return; // don't decode for opted-out users
      const el = ensureAudio();
      if (!el) return;
      unlockedRef.current = true;
      removeListeners();
      primeElement(el);
    }
    window.addEventListener('pointerdown', unlock, true);
    window.addEventListener('keydown', unlock, true);
    return removeListeners;
  }, [ensureAudio, primeElement]);

  // BACI-336: once-only startup autoplay probe. Fired on mount (when the
  // toggle is on) rather than waiting for the first user gesture, so a
  // permissive browser registers autoplay permission as early as
  // possible — before any ship event. Because the hook is mounted once at
  // App level (above <Routes>), the granted permission then survives
  // in-app navigations so a later gesture-less ship still dings. On a
  // strict-autoplay engine the probe's play() rejects silently and the
  // existing first-gesture unlock covers it instead. The probedRef guard
  // keeps it to a single attempt; setting unlockedRef on a successful
  // prime lets the gesture listener early-out so the two never double-fire.
  useEffect(() => {
    if (typeof window === 'undefined') return;
    if (probedRef.current) return;
    const audioCtor = (typeof Audio !== 'undefined') ? Audio : undefined;
    if (!shouldProbeShipSfx(enabledRef.current, audioCtor, probedRef.current)) return;
    const el = ensureAudio();
    if (!el) return;
    probedRef.current = true;
    unlockedRef.current = true;
    primeElement(el);
  }, [ensureAudio, primeElement]);

  const play = useCallback(() => {
    // SSR / Node test guard: window is undefined in non-browser envs.
    if (typeof window === 'undefined') return;

    const audioCtor = (typeof Audio !== 'undefined') ? Audio : undefined;
    if (!shouldPlayShipSfx(enabledRef.current, audioCtor)) return;

    const el = ensureAudio();
    if (!el) return;

    // Re-assert volume (the unlock may have left it at 0 — see the
    // constant) and reset currentTime so back-to-back ships restart
    // cleanly rather than overlapping.
    el.volume = SHIP_SFX_VOLUME;
    try { el.currentTime = 0; } catch { /* some engines refuse pre-load — fine */ }
    const result = el.play();
    if (result && typeof result.then === 'function') {
      result.catch((err: unknown) => {
        // Non-fatal: the pill still rolled. But we warn rather than
        // swallow silently so a missing ka-ching is diagnosable — a
        // blocked NotAllowedError looks identical to "play() never fired".
        const e = err as { name?: string; message?: string } | undefined;
        console.warn(
          '[ship-sfx] ka-ching play() was blocked (likely the browser autoplay policy — no user gesture yet):',
          e?.name ?? err, e?.message ?? '',
        );
      });
    }
  }, [ensureAudio]);

  return { play };
}
