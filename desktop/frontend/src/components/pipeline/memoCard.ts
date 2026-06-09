import type { BoardCard } from '../../api';

// cardPropsEqual is the React.memo comparator for the two hot pipeline leaf
// cards, PipelineCard and StageCard (BACI-366). The board re-fetches `cards`
// on every 10s poll, so the `card` prop is a *fresh object every tick* even
// when its value is unchanged — a default React.memo (referential compare)
// would therefore never skip a re-render. With every other prop now stable
// (the scalar flags are values; the callbacks were lifted to stable
// identities), the only churning prop is `card`, so we compare it by value and
// everything else by identity.
//
// Safety: a by-value comparison can only ever return `false` too often (an
// unnecessary re-render) — never `true` when the value actually changed — so a
// new BoardCard field can't make the card render stale. JSON.stringify is the
// pragmatic value check here: BoardCards are plain JSON DTOs (no functions /
// Dates / Maps), the snapshots being compared come from the same source so key
// order is stable, and serialising a handful of ~1 KB cards on the poll cadence
// is trivially cheaper than re-rendering each card's whole subtree.
export function cardPropsEqual<P extends { card: BoardCard }>(prev: P, next: P): boolean {
  const keys = Object.keys(prev) as (keyof P)[];
  if (keys.length !== Object.keys(next).length) return false;
  for (const key of keys) {
    if (key === 'card') continue;
    if (prev[key] !== next[key]) return false;
  }
  return sameCardValue(prev.card, next.card);
}

function sameCardValue(a: BoardCard, b: BoardCard): boolean {
  if (a === b) return true;
  return JSON.stringify(a) === JSON.stringify(b);
}
