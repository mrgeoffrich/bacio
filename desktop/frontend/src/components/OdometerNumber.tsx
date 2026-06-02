import { useEffect, useRef, useState } from 'react';
import { decideOdometerAction, digitAt } from '../lib/odometer';

// OdometerNumber (BACI-240) is the Pipeline Shipping-column Shipped
// pill's 3-digit odometer. Renders three zero-padded digit columns each clipped
// via `overflow:hidden`; the visible digit is positioned by
// `transform: translateY(-d * 1em)` interpolated by CSS transition.
//
// The critical UX rule is **roll only on genuine increments, snap on
// everything else**:
//
//   - First mount: snap (no prior value to roll from).
//   - Decrement (scope / repo switch, archive flip): snap. We never
//     roll backwards.
//   - Equal value: no-op.
//   - Increment: tween a single internal counter from prev → next
//     over ROLL_DURATION_MS (~600ms), easing applied. All three
//     digit columns derive from the same counter so they roll
//     coherently (8 → 9 → 10) rather than spinning independently.
//
// The three-column / 1em row strip is purely presentational. The
// matching CSS lives at `.mk-odometer*` in styles/app.css.
//
// `prefers-reduced-motion` short-circuits the RAF tween to a snap.
//
// Props:
//   value: number   — the live count to display.
//   maxDigits?: 3   — column count (defaults to 3; widen here when
//                      we ship 1000+ in scope).
//
// Pure presentational — owns no API state, no error surface.

const ROLL_DURATION_MS = 600;
const DEFAULT_MAX_DIGITS = 3;

// easeOutCubic — the brief calls for a "tasteful default". Cubic-out
// is the standard pick for short UI tweens (fast start, gentle stop).
function easeOutCubic(t: number): number {
  return 1 - Math.pow(1 - t, 3);
}

type OdometerNumberProps = {
  value: number;
  maxDigits?: number;
};

export default function OdometerNumber({ value, maxDigits = DEFAULT_MAX_DIGITS }: OdometerNumberProps) {
  // displayValue is the integer the digit columns derive from. Starts
  // at the prop on first mount (snap), then either snaps or tweens
  // on each prop change.
  const safeValue = Number.isFinite(value) && value >= 0 ? Math.floor(value) : 0;
  const [displayValue, setDisplayValue] = useState(safeValue);

  // Track the most recently-seen prop value across renders so the
  // effect can diff old vs new without re-firing on every parent
  // re-render that didn't actually change `value`.
  const prevValueRef = useRef<number | null>(null);
  // Active RAF handle so we can cancel a mid-flight tween if a new
  // value arrives before it lands (the brief's "rapid re-renders
  // stomp on displayValue" failure mode).
  const rafRef = useRef<number | null>(null);

  useEffect(() => {
    const prev = prevValueRef.current;
    prevValueRef.current = safeValue;

    // Cancel any in-flight tween before deciding the next action.
    if (rafRef.current !== null) {
      cancelAnimationFrame(rafRef.current);
      rafRef.current = null;
    }

    const reducedMotion = typeof window !== 'undefined' && window.matchMedia
      ? window.matchMedia('(prefers-reduced-motion: reduce)').matches
      : false;

    const action = decideOdometerAction(prev, safeValue, reducedMotion);
    if (action.kind === 'snap') {
      setDisplayValue(action.value);
      return;
    }
    if (action.kind === 'no-op') {
      return;
    }
    // Roll: tween from action.from → action.to over ROLL_DURATION_MS.
    const startedAt = (typeof performance !== 'undefined' && performance.now)
      ? performance.now()
      : Date.now();
    const from = action.from;
    const to = action.to;
    const tick = (now: number) => {
      const elapsed = now - startedAt;
      const t = Math.min(1, elapsed / ROLL_DURATION_MS);
      const eased = easeOutCubic(t);
      const current = Math.round(from + (to - from) * eased);
      setDisplayValue(current);
      if (t < 1) {
        rafRef.current = requestAnimationFrame(tick);
      } else {
        rafRef.current = null;
      }
    };
    rafRef.current = requestAnimationFrame(tick);
  }, [safeValue]);

  // Cleanup on unmount — the brief's open question called this out.
  useEffect(() => () => {
    if (rafRef.current !== null) cancelAnimationFrame(rafRef.current);
  }, []);

  // Render `maxDigits` columns, most-significant first. Each column
  // is a `.mk-odometer-col` containing a `.mk-odometer-strip` 0-9
  // vertical strip; CSS positions the strip via
  // `transform: translateY(-digit * 1em)`.
  const cols = [];
  for (let col = maxDigits - 1; col >= 0; col--) {
    const d = digitAt(displayValue, col);
    cols.push(
      <span key={col} className="mk-odometer-col" aria-hidden="true">
        <span
          className="mk-odometer-strip"
          style={{ transform: `translateY(-${d}em)` }}
        >
          {Array.from({ length: 10 }, (_, n) => (
            <span key={n} className="mk-odometer-digit">{n}</span>
          ))}
        </span>
      </span>
    );
  }

  return (
    // sr-only label carries the integer value so screen readers hear
    // "145" not "1 4 5 columns". The visual columns are aria-hidden.
    <span className="mk-odometer" role="text">
      <span className="mk-odometer-sr">{safeValue}</span>
      {cols}
    </span>
  );
}
