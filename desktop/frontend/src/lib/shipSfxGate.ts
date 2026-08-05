// Pure decision functions for the ship-SFX (BACI-240 / BACI-375).
// Lives in a sibling module so the Node smoketest can import it without
// pulling shipSfx.ts (which imports the asset URL via Vite — a
// runtime path Node doesn't know how to resolve).
//
// shipSfxEngine.ts holds the Web Audio state; this file holds the two
// decisions that state feeds — "may we attempt playback at all?" and
// "what should the Settings pane say about why it's quiet?".

// shouldPlayShipSfx returns true only when every gate passes:
//   - the user enabled the toggle;
//   - the AudioContext constructor is reachable (a non-browser env /
//     locked-down profile fails here).
//
// BACI-295: `prefers-reduced-motion` is deliberately NOT a gate. That
// preference governs animation, not audio — the visual ship flight +
// odometer roll still honour it, but a user who opted into the ship
// sound hears it regardless of their motion preference. (The browser's
// autoplay policy is still the real silencer when no gesture has
// landed; that's handled downstream in shipSfxEngine.ts, not here.)
export function shouldPlayShipSfx(
  enabled: boolean,
  audioContextCtor: unknown,
): boolean {
  if (!enabled) return false;
  if (!audioContextCtor) return false;
  return true;
}

// The state the ship sound is in, most-actionable first:
//   off          — the user's toggle is off; nothing to report.
//   unavailable  — Web Audio is unreachable, or the sound failed to load.
//   blocked      — a gesture has landed but the context still isn't
//                  running: the browser's autoplay policy is refusing.
//   locked       — no gesture yet; a click on the page will unlock it.
//   loading      — context running, sound still being fetched/decoded.
//   ready        — the next ship will play.
export type ShipSfxState = 'off' | 'loading' | 'locked' | 'ready' | 'blocked' | 'unavailable';

// detail carries the underlying reason for the two failure states and is
// empty otherwise. The Settings status line appends it verbatim.
export type ShipSfxStatus = { state: ShipSfxState; detail: string };

export type ShipSfxStatusInput = {
  // The ui.shipped_sfx toggle.
  enabled: boolean;
  // Whether the AudioContext constructor is reachable at all.
  supported: boolean;
  // Whether a user gesture has been seen on the page since load. This is
  // what separates "hasn't had a chance yet" (locked) from "had a chance
  // and was refused" (blocked) — the distinction the pre-BACI-375
  // element path could never make.
  gestureSeen: boolean;
  // The live AudioContext.state, or null before one is constructed.
  contextState: AudioContextState | null;
  // Whether the MP3 has been fetched + decoded.
  bufferReady: boolean;
  // A short human string when the fetch/decode failed; empty otherwise.
  loadError: string;
};

// decideShipSfxStatus derives the Settings-pane status from the engine's
// raw state. Pure — the engine calls it on every publish and the tests
// drive it directly, so the state machine that broke in BACI-375 is now
// the most-tested code in the feature rather than the least.
export function decideShipSfxStatus(input: ShipSfxStatusInput): ShipSfxStatus {
  const { enabled, supported, gestureSeen, contextState, bufferReady, loadError } = input;
  if (!enabled) return { state: 'off', detail: '' };
  if (!supported) return { state: 'unavailable', detail: 'This browser has no Web Audio support.' };
  if (loadError) return { state: 'unavailable', detail: loadError };
  // A closed context is terminal — nothing short of a reload revives it.
  if (contextState === 'closed') return { state: 'unavailable', detail: 'The audio context was closed.' };
  // `interrupted` (WebKit, e.g. a phone call or another app grabbing the
  // audio session) and `suspended` both land here: not running.
  if (contextState !== 'running') {
    return gestureSeen
      ? { state: 'blocked', detail: contextState ? `The audio context is ${contextState}.` : '' }
      : { state: 'locked', detail: '' };
  }
  if (!bufferReady) return { state: 'loading', detail: '' };
  return { state: 'ready', detail: '' };
}
