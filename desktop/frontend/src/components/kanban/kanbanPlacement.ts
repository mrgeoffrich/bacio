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
// whichever lane holds it and append it to `toUuid`. Appending (rather than
// inserting at a drop index) mirrors the write the board issues —
// `moveIssueToKanbanColumn(..., position: null)`.
//
// A card that isn't on the board yet has no source lane to leave, so the
// removal pass simply no-ops and the append opts it in — which is exactly
// what dropping a card onto a lane means on a git repo.
export function placeCardInLane(
  columns: KanbanColumn[],
  key: string,
  toUuid: string,
): KanbanColumn[] {
  return columns.map(col => {
    if (col.uuid === toUuid) {
      const without = col.cards.filter(ref => ref.key !== key);
      return { ...col, cards: densify([...without, { key, position: without.length }]) };
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
