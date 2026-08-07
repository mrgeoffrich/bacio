import { describe, it, expect } from 'vitest';
import { laneOf, placeCardInLane, resolveLaneCards } from '../kanbanPlacement';
import type { BoardCard, KanbanColumn } from '../../../api';

// The two fiddly halves of the Kanban board live in kanbanPlacement so they
// can be exercised without a DOM: the optimistic drag-drop reshuffle (which
// has to re-densify positions the way the store does) and the container-side
// membership join (which is the only reason a lane can render at all —
// BoardCard carries no lane field).

function lane(uuid: string, name: string, keys: string[], position = 0): KanbanColumn {
  return {
    uuid,
    name,
    position,
    cards: keys.map((key, index) => ({ key, position: index })),
  };
}

function card(key: string): BoardCard {
  return {
    key,
    column: 'todo',
    columnLabel: 'Todo',
    title: `title ${key}`,
    tags: [],
    assignees: [],
    claude: false,
    taken: false,
  };
}

describe('placeCardInLane', () => {
  const board = [lane('u1', 'Backlog', ['A-1', 'A-2'], 0), lane('u2', 'Doing', ['A-3'], 1)];

  it('moves a card between lanes and appends it at the bottom', () => {
    const next = placeCardInLane(board, 'A-1', 'u2');
    expect(next[0].cards.map(r => r.key)).toEqual(['A-2']);
    expect(next[1].cards.map(r => r.key)).toEqual(['A-3', 'A-1']);
  });

  it('re-densifies positions on both sides of the move', () => {
    const next = placeCardInLane(board, 'A-1', 'u2');
    expect(next[0].cards.map(r => r.position)).toEqual([0]);
    expect(next[1].cards.map(r => r.position)).toEqual([0, 1]);
  });

  it('inserts at an explicit index so the paint matches the persisted position', () => {
    // "New issue in this lane" persists position 0; appending optimistically
    // would paint the card at the bottom and then animate it to the top when
    // the server board lands.
    const next = placeCardInLane(board, 'A-9', 'u2', 0);
    expect(next[1].cards.map(r => r.key)).toEqual(['A-9', 'A-3']);
    expect(next[1].cards.map(r => r.position)).toEqual([0, 1]);
  });

  it('clamps an out-of-range index rather than leaving a hole', () => {
    const next = placeCardInLane(board, 'A-9', 'u1', 99);
    expect(next[0].cards.map(r => r.key)).toEqual(['A-1', 'A-2', 'A-9']);
    expect(placeCardInLane(board, 'A-9', 'u1', -3)[0].cards.map(r => r.key))
      .toEqual(['A-9', 'A-1', 'A-2']);
  });

  it('opts a card that is on no lane onto the board', () => {
    const next = placeCardInLane(board, 'A-9', 'u1');
    expect(next[0].cards.map(r => r.key)).toEqual(['A-1', 'A-2', 'A-9']);
    expect(next[1].cards.map(r => r.key)).toEqual(['A-3']);
  });

  it('leaves untouched lanes referentially identical so React can skip them', () => {
    const next = placeCardInLane(board, 'A-1', 'u2');
    expect(next[0]).not.toBe(board[0]);
    // Lane u2 is the target so it changes; a third, uninvolved lane would not.
    const wider = [...board, lane('u3', 'Done', ['A-7'], 2)];
    const moved = placeCardInLane(wider, 'A-1', 'u2');
    expect(moved[2]).toBe(wider[2]);
  });

  it('never mutates the input board', () => {
    const before = JSON.stringify(board);
    placeCardInLane(board, 'A-1', 'u2');
    expect(JSON.stringify(board)).toBe(before);
  });
});

describe('laneOf', () => {
  const board = [lane('u1', 'Backlog', ['A-1']), lane('u2', 'Doing', ['A-3'])];

  it('finds the lane holding a card', () => {
    expect(laneOf(board, 'A-3')?.uuid).toBe('u2');
  });

  it('returns undefined for a card that is off the board', () => {
    expect(laneOf(board, 'A-9')).toBeUndefined();
  });
});

describe('resolveLaneCards', () => {
  const cardsByKey = new Map([['A-1', card('A-1')], ['A-2', card('A-2')]]);

  it('joins refs against the polled cards in lane order, not ref order', () => {
    const column: KanbanColumn = {
      uuid: 'u1',
      name: 'Backlog',
      position: 0,
      cards: [{ key: 'A-2', position: 1 }, { key: 'A-1', position: 0 }],
    };
    expect(resolveLaneCards(column, cardsByKey).map(c => c.key)).toEqual(['A-1', 'A-2']);
  });

  it('drops a ref whose issue is not in the card list rather than rendering a hole', () => {
    const column = lane('u1', 'Backlog', ['A-1', 'A-404', 'A-2']);
    expect(resolveLaneCards(column, cardsByKey).map(c => c.key)).toEqual(['A-1', 'A-2']);
  });

  it('does not mutate the lane it reads', () => {
    const column = lane('u1', 'Backlog', ['A-2', 'A-1']);
    column.cards = [{ key: 'A-2', position: 1 }, { key: 'A-1', position: 0 }];
    resolveLaneCards(column, cardsByKey);
    expect(column.cards.map(r => r.key)).toEqual(['A-2', 'A-1']);
  });
});
