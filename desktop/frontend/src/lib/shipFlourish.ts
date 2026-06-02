// useShipFlourish detects card transitions into `done` from a
// non-terminal column and triggers the Pipeline "ship flourish" — a
// shared-element flight from the pipeline card position to the
// `Shipped · N` pill, followed by a short pulse on the pill. Wired
// into the App alongside the existing 10s `cards` poll / push pipeline;
// the hook is pure-client and reads no network.
//
// The diff is intentionally minimal — we only care about the
// `key → column` mapping. The very first render (when the prevRef is
// empty) is ignored so the board doesn't pretend every existing `done`
// card just shipped on mount. A re-render that lands the same card in
// `done` again (the common poll case) is also ignored because its
// previous column was already `done`.
//
// Cancelled is deliberately NOT a flourish trigger — the user comment
// on BACI-193 only asked for `done → Shipped pill`. See
// `bacio-193-plan.md` (Out of scope).
//
// The companion CSS (`.mk-shipped-pill.is-flash`) and Motion
// `layoutId` plumbing live in `PipelineView.tsx` (the PipelineCard
// flight source) and `ShippedPopover.tsx` (the destination slot);
// this hook is the controller.
//
// BACI-254: the hook also fires an optional `onShip(keys)` callback
// from the same detection effect. The callback receives every card
// that transitioned in the tick (not just the first), so a burst
// ship lands every audio cue even though only the first key drives
// the visual flight. The SFX caller lives in `App.tsx` so the audio
// fires regardless of which surface the user is on — previously the
// SFX was tied to the Motion flight-completion callback inside
// `ShippedPopover`, which never fires when the source `motion.article`
// isn't mounted (Features graph, Settings, off-board cards, …).

import { useCallback, useEffect, useRef, useState } from 'react';

// FLASH_DURATION_MS: how long the pill carries the `.is-flash` class
// after the flight lands. Matches the keyframe defined in app.css
// (`mk-ship-flash` runs ~480ms — 2 × --dur-slow); 520ms keeps one
// extra frame of buffer so the animation finishes before the class
// clears.
const FLASH_DURATION_MS = 520;

// MAX_FLIGHT_MS: hard cap on how long a single ship flight is allowed
// to occupy the destination slot. Motion's layout animation is
// interruptible and should finish in ~280ms, but if the user
// navigates away mid-flight and the destination never reports
// completion we still want the flying-card slot to clear. 600ms is
// the longest plausible flight; after that we drop the layoutId so
// the next ship can take the slot.
const MAX_FLIGHT_MS = 600;

// A minimal subset of the BoardCard shape we depend on — keeps this
// hook unit-testable without dragging in the full DTO.
type ShipCard = {
  key: string;
  column: string;
};

export type ShipFlourishResult = {
  // The key of the card currently flying to the Shipped pill, or
  // null when no flight is in progress. The PipelineCard wrapper
  // uses this to render its `motion.article` with a matching
  // `layoutId`; the ShippedPopover renders the destination slot
  // with the same `layoutId` so Motion bridges the two trees.
  flyingKey: string | null;
  // True for ~520ms after the flying card lands. PipelineView threads
  // this through to the pill so it can toggle `.is-flash`.
  flashing: boolean;
  // Called by the destination slot once Motion fires
  // `onLayoutAnimationComplete`. Triggers the pulse, clears the
  // flying-card slot.
  onFlightDone: () => void;
};

// useShipFlourish watches the App's `cards` array. When it detects a
// transition (prev_column != 'done' && next_column === 'done') for
// any card, it stamps that card.key into `flyingKey`. The
// ShippedPopover's destination slot then receives a Motion
// `layoutId` match with the leaving card and the flight animates.
// On completion the pill flashes for FLASH_DURATION_MS.
//
// The hook also installs a MAX_FLIGHT_MS safety timer so a flight
// that never reports completion (caller unmounted, animation
// suppressed by `prefers-reduced-motion`, etc.) eventually clears.
//
// BACI-254: the optional `onShip(keys)` callback is fired from the
// same detection effect with every card that transitioned in the
// tick. The SFX caller in `App.tsx` uses it to play a sound per ship
// independently of Motion / the Pipeline being mounted. Only the first
// key still drives the visual flight (one Motion flight at a time);
// the array carries the rest so a burst ship lands every audio cue.
export function useShipFlourish(
  cards: ShipCard[],
  options?: { onShip?: (keys: string[]) => void },
): ShipFlourishResult {
  const [flyingKey, setFlyingKey] = useState<string | null>(null);
  const [flashing, setFlashing] = useState(false);

  // prevColumns maps key → last-seen column. Starts empty so the
  // first render (mount) populates without triggering a flight.
  const prevColumns = useRef<Map<string, string> | null>(null);

  // Safety timer for runaway flights — see MAX_FLIGHT_MS docstring.
  const flightTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  // Hold the latest onShip in a ref so the detection effect can read
  // it without re-running when the callback identity flips on every
  // App render. The `cards` array is the only dep that should
  // re-fire detection.
  const onShipRef = useRef<((keys: string[]) => void) | undefined>(options?.onShip);
  useEffect(() => { onShipRef.current = options?.onShip; }, [options?.onShip]);

  useEffect(() => {
    const prev = prevColumns.current;
    // First render: seed the map, don't fire any flights.
    if (prev === null) {
      const seeded = new Map<string, string>();
      for (const c of cards) seeded.set(c.key, c.column);
      prevColumns.current = seeded;
      return;
    }

    // Collect every card whose column flipped INTO `done` from a
    // non-terminal column. The visual flight still uses the first
    // key (Motion only supports one flight at a time inside the
    // shared LayoutGroup); the full list is fanned to onShip so a
    // burst ship lands every audio cue.
    const shipped: string[] = [];
    for (const c of cards) {
      const wasIn = prev.get(c.key);
      if (c.column === 'done' && wasIn && wasIn !== 'done' && wasIn !== 'cancelled') {
        shipped.push(c.key);
      }
    }

    // Update the snapshot regardless of whether we flew anything,
    // so the next render's diff uses the latest columns.
    const next = new Map<string, string>();
    for (const c of cards) next.set(c.key, c.column);
    prevColumns.current = next;

    if (shipped.length === 0) return;

    // Fire the audio fan-out first so SFX never depends on the
    // visual flight starting (a card off the active board has no
    // source `motion.article`; the flight never runs but the audio
    // should still fire). Guard the callback so a thrown handler
    // doesn't stop us setting the flying key.
    const handler = onShipRef.current;
    if (handler) {
      try { handler(shipped); }
      catch { /* SFX is best-effort; never let it stall the flourish */ }
    }

    setFlyingKey(shipped[0]);
    // Arm the safety timer. If onFlightDone fires first it clears
    // the timer; otherwise the timer drops the slot after the cap.
    if (flightTimer.current) clearTimeout(flightTimer.current);
    flightTimer.current = setTimeout(() => {
      setFlyingKey(null);
      flightTimer.current = null;
    }, MAX_FLIGHT_MS);
  }, [cards]);

  // Cleanup the safety timer on unmount so the setTimeout doesn't
  // outlive the App.
  useEffect(() => () => {
    if (flightTimer.current) clearTimeout(flightTimer.current);
  }, []);

  const onFlightDone = useCallback(() => {
    if (flightTimer.current) {
      clearTimeout(flightTimer.current);
      flightTimer.current = null;
    }
    setFlyingKey(null);
    setFlashing(true);
    setTimeout(() => setFlashing(false), FLASH_DURATION_MS);
  }, []);

  return { flyingKey, flashing, onFlightDone };
}

// computeShippedKeys is the pure diff function the hook leans on,
// exported for unit testing. Returns the ordered list of card keys
// that just transitioned into `done` from a non-terminal column in
// this tick — possibly multiple when a poll bundles two ships, or
// empty when nothing shipped. `prev` is null on the very first
// render (no diff is meaningful — the board's existing `done`
// cards shouldn't pretend to have just shipped).
export function computeShippedKeys(
  prev: Map<string, string> | null,
  next: ShipCard[],
): string[] {
  if (prev === null) return [];
  const out: string[] = [];
  for (const c of next) {
    const wasIn = prev.get(c.key);
    if (c.column === 'done' && wasIn && wasIn !== 'done' && wasIn !== 'cancelled') {
      out.push(c.key);
    }
  }
  return out;
}

// computeShipFlight (kept for backwards compatibility with the
// BACI-193 visual-flight contract) returns just the first shipped
// key — what `flyingKey` gets stamped with. The audio fan-out reads
// `computeShippedKeys` directly so every ship in the tick is heard.
export function computeShipFlight(
  prev: Map<string, string> | null,
  next: ShipCard[],
): string | null {
  const keys = computeShippedKeys(prev, next);
  return keys.length > 0 ? keys[0] : null;
}
