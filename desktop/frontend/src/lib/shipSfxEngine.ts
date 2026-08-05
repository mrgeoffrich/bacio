// shipSfxEngine — the Web Audio singleton behind the ka-ching that
// sounds when a card ships (BACI-375, replacing the HTMLAudioElement
// path of BACI-240 / BACI-295 / BACI-336).
//
// Why a module singleton and not a provider: this owns a browser-global
// resource plus window-level listeners, the same shape as errors.ts —
// see docs/frontend-architecture.md §5. It also lets the Settings pane
// read the status without threading props through SettingsView.
//
// Why Web Audio and not <audio>: `AudioContext.state` is a first-class,
// readable, *live* unlock signal ('suspended' / 'running' /
// 'interrupted'). With an <audio> element you only learn you're blocked
// by trying and failing, so "the sound is quiet" and "the sound is
// broken" were indistinguishable. That readable state is what makes the
// Settings status line possible.
//
// The autoplay contract, and the bug this replaces: the two engines
// grant autoplay on different axes. Chromium uses **origin-level sticky
// activation** — any past click on the origin permits a later play().
// WebKit (Safari, and the macOS desktop app's WKWebView) grants
// **per-element / per-context**, and only when the unlock is invoked
// inside a real gesture. BACI-336's startup probe marked the element
// unlocked *before* its play() promise settled, so on WebKit the flag
// went true, the attempt was refused, and the first-gesture listener
// then early-returned forever — the element was never played inside a
// gesture and every subsequent ship died on the autoplay policy.
// Invisible in Chrome, fatal in Safari.
//
// The structural fix: **nothing here is ever marked unlocked
// optimistically.** "Am I unlocked" is read straight off ctx.state at
// the moment it matters, and the gesture listeners stay armed until the
// context is genuinely 'running'. A failed attempt changes nothing; the
// next gesture retries. ctx.onstatechange re-arms them if the context is
// later suspended or interrupted, so the engine self-heals across tab
// backgrounding and audio-device changes.

import { decideShipSfxStatus, shouldPlayShipSfx } from './shipSfxGate';
import type { ShipSfxStatus } from './shipSfxGate';

// Playback volume. Halved — UI dings are easy to misjudge at full level.
const SHIP_SFX_VOLUME = 0.5;

// Capture-phase so we see the gesture even when a handler below stops
// propagation. touchend/pointerup are here as well as pointerdown
// because iOS Safari has historically been fussier about which of them
// counts as an activating gesture.
const GESTURE_EVENTS = ['pointerdown', 'pointerup', 'keydown', 'touchend'] as const;

let assetUrl = '';
let enabled = false;
let ctx: AudioContext | null = null;
let gain: GainNode | null = null;
let buffer: AudioBuffer | null = null;
// Memoised in-flight fetch+decode. Cleared on failure so a later gesture
// retries (decodeAudioData detaches its input, so a retry must re-fetch).
let bufferPromise: Promise<void> | null = null;
let activeSource: AudioBufferSourceNode | null = null;
let gestureSeen = false;
let loadError = '';
let armed = false;
// Cached snapshot. useSyncExternalStore compares by identity, so this
// object must only be replaced when something actually changed.
let status: ShipSfxStatus = { state: 'off', detail: '' };
const listeners = new Set<() => void>();
// One console.warn per distinct reason, so a run of silent ships leaves
// exactly one breadcrumb per cause rather than a wall of noise.
const warnedReasons = new Set<string>();

function ctorFor(): typeof AudioContext | undefined {
  // No webkitAudioContext fallback: unprefixed AudioContext has shipped
  // since Safari 14.1 (2021), and the prefixed path needs an untyped
  // cast for no real coverage.
  return typeof AudioContext !== 'undefined' ? AudioContext : undefined;
}

function computeStatus(): ShipSfxStatus {
  return decideShipSfxStatus({
    enabled,
    supported: !!ctorFor(),
    gestureSeen,
    contextState: ctx ? ctx.state : null,
    bufferReady: !!buffer,
    loadError,
  });
}

function publish(): void {
  const next = computeStatus();
  if (next.state === status.state && next.detail === status.detail) return;
  status = next;
  listeners.forEach(l => l());
}

function armGestureListeners(): void {
  if (armed || typeof window === 'undefined') return;
  armed = true;
  for (const evt of GESTURE_EVENTS) window.addEventListener(evt, onGesture, true);
}

function disarmGestureListeners(): void {
  if (!armed || typeof window === 'undefined') return;
  armed = false;
  for (const evt of GESTURE_EVENTS) window.removeEventListener(evt, onGesture, true);
}

// syncArming is the single place that decides whether the gesture
// listeners should be attached: attached unless the context is genuinely
// 'running'. Everything that can change ctx.state routes through here,
// so a failed unlock can never leave us disarmed.
function syncArming(): void {
  if (ctx && ctx.state === 'running') disarmGestureListeners();
  else armGestureListeners();
}

// ensureContext constructs the context + gain graph. MUST be called
// synchronously inside a gesture handler on WebKit — the activation is
// lost across an await.
function ensureContext(): AudioContext | null {
  if (ctx) return ctx;
  const Ctor = ctorFor();
  if (!Ctor) return null;
  try {
    ctx = new Ctor();
    gain = ctx.createGain();
    gain.gain.value = SHIP_SFX_VOLUME;
    gain.connect(ctx.destination);
    ctx.onstatechange = () => {
      // Self-healing: a context that drops out of 'running' (tab
      // backgrounded, audio device swapped, WebKit interruption) gets
      // its gesture listeners back so the next click revives it.
      syncArming();
      publish();
    };
  } catch {
    ctx = null;
    gain = null;
  }
  return ctx;
}

// ensureBuffer fetches + decodes the MP3. Needs no gesture, so it runs
// asynchronously after the synchronous unlock pair.
function ensureBuffer(): Promise<void> {
  if (buffer) return Promise.resolve();
  if (bufferPromise) return bufferPromise;
  const active = ensureContext();
  if (!active || !assetUrl) return Promise.resolve();
  bufferPromise = (async () => {
    const res = await fetch(assetUrl);
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    const bytes = await res.arrayBuffer();
    const decoded = await active.decodeAudioData(bytes);
    buffer = decoded;
    loadError = '';
  })()
    .catch((err: unknown) => {
      const e = err as { message?: string } | undefined;
      loadError = e?.message ? `Couldn't load the sound: ${e.message}` : "Couldn't load the sound.";
    })
    .finally(() => {
      // Clear the memo either way: on success `buffer` short-circuits,
      // and on failure a later gesture must be free to re-fetch.
      bufferPromise = null;
      publish();
    });
  return bufferPromise;
}

function onGesture(): void {
  // Don't record the gesture (and don't decode) for an opted-out
  // session: a click made while the toggle was off is not evidence that
  // the browser refused us, so it must not turn a later `locked` into
  // `blocked`.
  if (!enabled) return;
  gestureSeen = true;
  const active = ensureContext();
  if (!active) { publish(); return; }
  // Both the construction above and this resume() are synchronous inside
  // the handler — Safari loses the gesture across an await.
  if (active.state !== 'running') {
    active.resume().then(
      () => { syncArming(); publish(); },
      () => { syncArming(); publish(); /* refused; the next gesture retries */ },
    );
  }
  // Only stand down once the context is genuinely running. An optimistic
  // disarm here is exactly the BACI-375 bug.
  syncArming();
  void ensureBuffer();
  publish();
}

// armShipSfx records the asset URL and attaches the gesture listeners.
// Idempotent, and deliberately builds nothing: an opted-out session must
// pay zero decode cost, and audioEnabled seeds `true` for ~100ms before
// the server preference lands (PreferencesProvider.tsx), so eager
// construction would occasionally build for a user who opted out.
export function armShipSfx(url: string): void {
  assetUrl = url;
  armGestureListeners();
  publish();
}

export function setShipSfxEnabled(next: boolean): void {
  if (enabled === next) return;
  enabled = next;
  if (!enabled) {
    stopActiveSource();
  } else if (ctx && ctx.state !== 'running') {
    // Re-enabling shouldn't cost the user another gesture if the context
    // is merely suspended — try to revive it, and stay armed in case not.
    ctx.resume().then(
      () => { syncArming(); publish(); },
      () => { syncArming(); publish(); /* the next gesture retries */ },
    );
    syncArming();
  }
  // The context is deliberately NOT closed on disable: a re-enable
  // should not need a fresh gesture.
  publish();
}

function stopActiveSource(): void {
  if (!activeSource) return;
  try { activeSource.stop(); } catch { /* already ended — fine */ }
  activeSource = null;
}

function warnOnce(reason: string, message: string): void {
  if (warnedReasons.has(reason)) return;
  warnedReasons.add(reason);
  console.warn(`[ship-sfx] ${message}`);
}

// playShipSfx fires one ka-ching. Always returns synchronously, never
// throws, and no-ops on every failure mode.
export function playShipSfx(): void {
  if (!shouldPlayShipSfx(enabled, ctorFor())) return;

  if (!ctx || ctx.state !== 'running' || !buffer) {
    // Can't play *now* — record why, then do the cheap opportunistic
    // work that makes the *next* ship land.
    publish();
    if (!ctx) {
      warnOnce('no-context', 'no audio context yet — click anywhere on the page to unlock the ship sound');
    } else if (ctx.state !== 'running') {
      warnOnce('not-running', `the audio context is ${ctx.state} (browser autoplay policy) — click anywhere on the page to unlock the ship sound`);
      void ctx.resume().catch(() => { /* the next gesture retries */ });
    } else {
      warnOnce('no-buffer', loadError || 'the ka-ching is still loading');
    }
    if (ctx) void ensureBuffer();
    return;
  }

  // Back-to-back ships restart cleanly rather than overlapping — the
  // Web Audio equivalent of the old `currentTime = 0`.
  stopActiveSource();
  const src = ctx.createBufferSource();
  src.buffer = buffer;
  if (gain) src.connect(gain); else src.connect(ctx.destination);
  src.onended = () => { if (activeSource === src) activeSource = null; };
  activeSource = src;
  try { src.start(); } catch { activeSource = null; }
}

export function getShipSfxStatus(): ShipSfxStatus {
  return status;
}

export function subscribeShipSfxStatus(listener: () => void): () => void {
  listeners.add(listener);
  return () => { listeners.delete(listener); };
}

// resetShipSfxEngineForTests tears every module var down and detaches the
// window listeners. Vitest specs must call this in beforeEach — module
// singleton state leaks across specs otherwise, and a phantom gesture
// handler left attached corrupts every later spec.
export function resetShipSfxEngineForTests(): void {
  disarmGestureListeners();
  stopActiveSource();
  if (ctx) ctx.onstatechange = null;
  assetUrl = '';
  enabled = false;
  ctx = null;
  gain = null;
  buffer = null;
  bufferPromise = null;
  gestureSeen = false;
  loadError = '';
  status = { state: 'off', detail: '' };
  listeners.clear();
  warnedReasons.clear();
}
