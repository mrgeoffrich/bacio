// Pure helpers for the BACI-240 OdometerNumber component. Lives in
// `lib/` (not `components/`) so the Node-based smoketest can import
// it without dragging React + JSX parsing into the runner.

// digitAt extracts the n-th decimal digit (0 = ones, 1 = tens, …).
// Negative / NaN values clamp to zero — defensive read style.
export function digitAt(value: number, place: number): number {
  if (!Number.isFinite(value) || value < 0) return 0;
  return Math.floor(value / Math.pow(10, place)) % 10;
}

// OdometerAction is the discriminated union the effect drives:
//
//   { kind: 'snap',   value }    — display value with no tween.
//   { kind: 'roll',   from, to } — tween from → to.
//   { kind: 'no-op' }            — value unchanged; no work.
export type OdometerAction =
  | { kind: 'snap'; value: number }
  | { kind: 'roll'; from: number; to: number }
  | { kind: 'no-op' };

// decideOdometerAction is the pure snap-vs-roll decision the effect
// drives. `prev` is the value displayed before this render (or null
// on first mount); `next` is the new target. `forceSnap` collapses
// the roll branch to a snap; the hook caller passes it when
// `prefers-reduced-motion` is on.
export function decideOdometerAction(
  prev: number | null | undefined,
  next: number,
  forceSnap: boolean = false,
): OdometerAction {
  if (!Number.isFinite(next) || next < 0) {
    // Clamp pathological inputs to a snap-to-zero rather than crash.
    return { kind: 'snap', value: 0 };
  }
  if (prev === null || prev === undefined) {
    return { kind: 'snap', value: next };
  }
  if (next === prev) {
    return { kind: 'no-op' };
  }
  if (next < prev) {
    return { kind: 'snap', value: next };
  }
  if (forceSnap) {
    return { kind: 'snap', value: next };
  }
  return { kind: 'roll', from: prev, to: next };
}
