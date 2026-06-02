// blockedByBadge (BACI-310) — the pure classification behind the
// Pipeline card's blocked-by indicator. Lives here, not inline in
// PipelineView.tsx, so it can be unit-tested headlessly (plain Node, no
// React/JSX) the same way featureGraphLayout.ts backs the
// featureDependencyLayout smoke test.
//
// card.blockedBy carries the OPEN `blocks` edges pointing AT a card
// (the server filters out done/cancelled blockers), so the badge clears
// automatically once a blocker finishes.

export type BlockedByMode = 'none' | 'single' | 'multi';

// blockedByMode classifies a card's open-blocker list into the three
// render modes BlockedByBadge switches on: 'none' (no badge), 'single'
// (the blocker key chip), 'multi' (a count chip + popover).
export function blockedByMode(
  blockedBy: ReadonlyArray<unknown> | null | undefined,
): BlockedByMode {
  const n = blockedBy?.length ?? 0;
  if (n === 0) return 'none';
  if (n === 1) return 'single';
  return 'multi';
}
