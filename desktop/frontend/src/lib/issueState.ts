// Terminal-state predicates shared by the React tree (kanban,
// workspace) when it needs to mirror the Go board-cards rule that
// `done` / `cancelled` are the "completed" columns. The Go
// counterpart is `boardcards.IsCompletedColumn` in
// internal/boardcards/cards.go — keep this list in lockstep with it.
//
// BACI-146 introduced this so the optimistic drag-to-move in
// App.tsx can recompute downstream cards' `blockedBy` arrays
// client-side the moment a blocker lands in a terminal column,
// without waiting for the 10s poll to re-fetch the server-filtered
// view.

export const TERMINAL_STATES = ['done', 'cancelled'] as const;

export type TerminalState = (typeof TERMINAL_STATES)[number];

export function isTerminalState(state: string | undefined | null): boolean {
  if (!state) return false;
  return (TERMINAL_STATES as readonly string[]).includes(state);
}

// stripBlockerFromCards returns a fresh cards array with `movedKey`
// removed from every card's `blockedBy` entry. Used by the App
// kanban's optimistic drag-to-move when the moved card lands in a
// terminal column (BACI-146) — the server runs the same filter via
// `boardcards.isCardBlockerOpen` on the next poll, so this just makes
// the lock icon clear immediately instead of after up to 10s.
//
// Cards whose `blockedBy` already lacks `movedKey` are returned by
// identity so React.memo on KanbanCard still skips unchanged rows.
export function stripBlockerFromCards<T extends { key: string; blockedBy?: Array<{ key: string }> | null }>(
  cards: T[],
  movedKey: string,
): T[] {
  return cards.map((c) => {
    const bb = c.blockedBy;
    if (!bb || bb.length === 0) return c;
    if (!bb.some((b) => b.key === movedKey)) return c;
    return { ...c, blockedBy: bb.filter((b) => b.key !== movedKey) };
  });
}

// restoreBlockedByFromSnapshot rebuilds each card's `blockedBy` from a
// pre-mutation snapshot keyed by card.key. Used on rollback when the
// server rejects the optimistic move and we need to undo not just the
// moved card's column but also the `blockedBy` entries we stripped
// from sibling cards. Cards absent from the snapshot are passed
// through unchanged.
export function restoreBlockedByFromSnapshot<T extends { key: string; blockedBy?: Array<{ key: string; state: string }> | null }>(
  cards: T[],
  snapshot: Map<string, T['blockedBy']>,
): T[] {
  return cards.map((c) => {
    if (!snapshot.has(c.key)) return c;
    const prev = snapshot.get(c.key);
    return { ...c, blockedBy: prev };
  });
}
