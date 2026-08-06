import { describe, it, expect } from 'vitest';
import {
  boardHintText,
  filterCardsByQuery,
  offBoardCards,
  onBoardKeys,
  removeCardFromBoard,
} from '../kanbanOffBoard';
import type { BoardCard, KanbanColumn } from '../../../api';

// The off-board set is the whole reason the "Add cards" picker needs no
// endpoint of its own: it is the difference between the lanes' card
// references and the repo's polled card list, both of which the board
// already holds. These exercise that difference — and the copy that
// depends on it — without a DOM.

function lane(uuid: string, name: string, keys: string[], position = 0): KanbanColumn {
  return {
    uuid,
    name,
    position,
    cards: keys.map((key, index) => ({ key, position: index })),
  };
}

function card(key: string, title = `title ${key}`): BoardCard {
  return {
    key,
    column: 'todo',
    columnLabel: 'Todo',
    title,
    tags: [],
    assignees: [],
    claude: false,
    taken: false,
  };
}

describe('onBoardKeys', () => {
  it('unions every lanes card references', () => {
    const board = [lane('u1', 'Backlog', ['A-1', 'A-2']), lane('u2', 'Doing', ['A-3'], 1)];
    expect([...onBoardKeys(board)].sort()).toEqual(['A-1', 'A-2', 'A-3']);
  });

  it('is empty for a board with no lanes', () => {
    expect(onBoardKeys([]).size).toBe(0);
  });

  it('is empty for lanes that hold nothing', () => {
    expect(onBoardKeys([lane('u1', 'Backlog', [])]).size).toBe(0);
  });
});

describe('offBoardCards', () => {
  const board = [lane('u1', 'Backlog', ['A-1']), lane('u2', 'Doing', ['A-3'], 1)];
  const cards = [card('A-1'), card('A-2'), card('A-3'), card('A-4')];

  it('returns exactly the cards no lane holds', () => {
    expect(offBoardCards(board, cards).map(c => c.key)).toEqual(['A-2', 'A-4']);
  });

  it('preserves the card list order rather than lane order', () => {
    const reversed = [...cards].reverse();
    expect(offBoardCards(board, reversed).map(c => c.key)).toEqual(['A-4', 'A-2']);
  });

  it('offers every card on a git repos empty board', () => {
    // The case the whole affordance exists for: kanban_column_id starts
    // NULL on a git repo, so a seeded-but-empty board offers everything.
    const empty = [lane('u1', 'Backlog', []), lane('u2', 'Doing', [], 1)];
    expect(offBoardCards(empty, cards).map(c => c.key)).toEqual(['A-1', 'A-2', 'A-3', 'A-4']);
  });

  it('offers nothing once every card is placed', () => {
    const full = [lane('u1', 'Backlog', ['A-1', 'A-2', 'A-3', 'A-4'])];
    expect(offBoardCards(full, cards)).toEqual([]);
  });

  it('ignores lane refs whose issue is not in the card list', () => {
    // An archived-and-hidden card is absent from `cards`, so its lane ref
    // must not conjure a candidate out of nowhere.
    const withGhost = [lane('u1', 'Backlog', ['A-1', 'A-404'])];
    expect(offBoardCards(withGhost, cards).map(c => c.key)).toEqual(['A-2', 'A-3', 'A-4']);
  });
});

describe('filterCardsByQuery', () => {
  const cards = [card('A-1', 'Sync badge reports mirroring'), card('A-2', 'Rebuild the ship sound')];

  it('returns everything for an empty or whitespace query', () => {
    expect(filterCardsByQuery(cards, '')).toBe(cards);
    expect(filterCardsByQuery(cards, '   ')).toBe(cards);
  });

  it('matches on the issue key', () => {
    expect(filterCardsByQuery(cards, 'a-2').map(c => c.key)).toEqual(['A-2']);
  });

  it('matches on the title, case-insensitively', () => {
    expect(filterCardsByQuery(cards, 'SHIP SOUND').map(c => c.key)).toEqual(['A-2']);
  });

  it('returns nothing when nothing matches', () => {
    expect(filterCardsByQuery(cards, 'zzz')).toEqual([]);
  });
});

describe('removeCardFromBoard', () => {
  const board = [lane('u1', 'Backlog', ['A-1', 'A-2', 'A-3']), lane('u2', 'Doing', ['A-4'], 1)];

  it('takes the card out of whichever lane holds it', () => {
    const next = removeCardFromBoard(board, 'A-2');
    expect(next[0].cards.map(r => r.key)).toEqual(['A-1', 'A-3']);
    expect(next[1].cards.map(r => r.key)).toEqual(['A-4']);
  });

  it('re-densifies the positions it leaves behind', () => {
    const next = removeCardFromBoard(board, 'A-1');
    expect(next[0].cards.map(r => r.position)).toEqual([0, 1]);
  });

  it('leaves untouched lanes referentially identical so React can skip them', () => {
    const next = removeCardFromBoard(board, 'A-2');
    expect(next[1]).toBe(board[1]);
  });

  it('is a no-op for a card already off the board', () => {
    const next = removeCardFromBoard(board, 'A-9');
    expect(next[0]).toBe(board[0]);
    expect(next[1]).toBe(board[1]);
  });

  it('never mutates the input board', () => {
    const before = JSON.stringify(board);
    removeCardFromBoard(board, 'A-2');
    expect(JSON.stringify(board)).toBe(before);
  });
});

describe('boardHintText', () => {
  it('asks for a lane when the board has none', () => {
    expect(boardHintText(0, 0, 12)).toBe(
      'No lanes on this Kanban yet — add one to start putting work on the board.',
    );
  });

  it('says nothing once anything is on the board', () => {
    expect(boardHintText(3, 1, 40)).toBe('');
  });

  it('points at the + affordance when lanes exist but nothing is placed', () => {
    expect(boardHintText(4, 0, 12)).toBe(
      'Nothing is on this Kanban yet — use + on a lane to put any of its 12 issues on the board.',
    );
  });

  it('singularises a lone off-board issue', () => {
    expect(boardHintText(4, 0, 1)).toContain('its 1 issue on the board');
  });

  it('does not blame the user when the repo simply has no issues', () => {
    expect(boardHintText(4, 0, 0)).toBe('This repository has no issues to put on the board yet.');
  });
});
