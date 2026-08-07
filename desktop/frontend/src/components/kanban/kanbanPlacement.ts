import type { BoardCard, KanbanColumn } from '../../api';

// Pure placement helpers for the Kanban board. Kept out of the component so
// the two fiddly bits — the optimistic drag-drop reshuffle and the
// container-side membership join — are unit-testable without a DOM.

// densify renumbers a lane's refs 0..n-1 after an insert / removal. The store
// re-densifies server-side around every write, so doing the same locally
// keeps the optimistic snapshot shaped like the answer that will replace it.
function densify(cards: KanbanColumn['cards']): KanbanColumn['cards'] {
  return cards.map((ref, index) => ({ ...ref, position: index }));
}

// placeCardInLane is the optimistic half of a drag-drop: take `key` out of
// whichever lane holds it and insert it into `toUuid`. `at` is the 0-based
// index to insert at and must match the `position` the caller persists —
// omit it (the drop handler's case) to append, mirroring
// `moveIssueToKanbanColumn(..., position: null)`; pass 0 for a write that
// pins the card to the top. A mismatch paints the card in one place and
// then animates it to another when the server board lands.
//
// A card that isn't on the board yet has no source lane to leave, so the
// removal pass simply no-ops and the insert opts it in — which is exactly
// what dropping a card onto a lane means on a git repo.
export function placeCardInLane(
  columns: KanbanColumn[],
  key: string,
  toUuid: string,
  at?: number,
): KanbanColumn[] {
  return columns.map(col => {
    if (col.uuid === toUuid) {
      const without = col.cards.filter(ref => ref.key !== key);
      const index = Math.max(0, Math.min(at ?? without.length, without.length));
      const next = [...without];
      next.splice(index, 0, { key, position: index });
      return { ...col, cards: densify(next) };
    }
    const without = col.cards.filter(ref => ref.key !== key);
    if (without.length === col.cards.length) return col;
    return { ...col, cards: densify(without) };
  });
}

// laneOf reports which lane currently holds a card, or undefined when the
// card isn't on the board. Drives the drop no-op guard.
export function laneOf(columns: KanbanColumn[], key: string): KanbanColumn | undefined {
  return columns.find(col => col.cards.some(ref => ref.key === key));
}

// resolveLaneCards joins a lane's card REFERENCES against the polled card
// list — membership rides on the container, so this is where a lane becomes
// renderable. Refs are taken in `position` order; a ref whose issue isn't in
// the card list (filtered out by the archived-visibility preference, or a
// lane that a concurrent write emptied between the two fetches) is dropped
// rather than rendered as a hole.
export function resolveLaneCards(
  column: KanbanColumn,
  cardsByKey: Map<string, BoardCard>,
): BoardCard[] {
  return [...column.cards]
    .sort((a, b) => a.position - b.position)
    .map(ref => cardsByKey.get(ref.key))
    .filter((card): card is BoardCard => card !== undefined);
}
