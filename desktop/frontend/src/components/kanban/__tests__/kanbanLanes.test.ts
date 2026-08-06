import { describe, it, expect } from 'vitest';
import { deleteLaneLines, laneIndex, laneShiftTarget, moveLaneTo } from '../kanbanLanes';
import type { KanbanColumn } from '../../../api';

// Lane CRUD's two testable halves: the reorder arithmetic (a left/right
// nudge has to resolve to the 0-based position the store wants, and the
// optimistic snapshot has to re-densify the way the store will), and the
// wording of the delete confirmation — which has to make clear that a
// lane delete takes cards OFF the board rather than deleting the issues.

function lane(uuid: string, name: string, position: number): KanbanColumn {
  return { uuid, name, position, cards: [] };
}

const board = [lane('u1', 'Backlog', 0), lane('u2', 'Doing', 1), lane('u3', 'Done', 2)];

describe('laneIndex', () => {
  it('reports the lanes slot in board order', () => {
    expect(laneIndex(board, 'u2')).toBe(1);
  });

  it('returns -1 for a lane that is not on the board', () => {
    expect(laneIndex(board, 'nope')).toBe(-1);
  });
});

describe('laneShiftTarget', () => {
  it('resolves a left nudge to the slot before', () => {
    expect(laneShiftTarget(board, 'u3', -1)).toBe(1);
  });

  it('resolves a right nudge to the slot after', () => {
    expect(laneShiftTarget(board, 'u1', 1)).toBe(1);
  });

  it('refuses to walk off the left end', () => {
    expect(laneShiftTarget(board, 'u1', -1)).toBeNull();
  });

  it('refuses to walk off the right end', () => {
    expect(laneShiftTarget(board, 'u3', 1)).toBeNull();
  });

  it('returns null for an unknown lane', () => {
    expect(laneShiftTarget(board, 'nope', 1)).toBeNull();
  });
});

describe('moveLaneTo', () => {
  it('moves a lane left and renumbers every position', () => {
    const next = moveLaneTo(board, 'u3', 0);
    expect(next.map(c => c.uuid)).toEqual(['u3', 'u1', 'u2']);
    expect(next.map(c => c.position)).toEqual([0, 1, 2]);
  });

  it('moves a lane right and renumbers every position', () => {
    const next = moveLaneTo(board, 'u1', 2);
    expect(next.map(c => c.uuid)).toEqual(['u2', 'u3', 'u1']);
    expect(next.map(c => c.position)).toEqual([0, 1, 2]);
  });

  it('clamps a target past the right-hand end', () => {
    expect(moveLaneTo(board, 'u1', 99).map(c => c.uuid)).toEqual(['u2', 'u3', 'u1']);
  });

  it('clamps a negative target', () => {
    expect(moveLaneTo(board, 'u3', -5).map(c => c.uuid)).toEqual(['u3', 'u1', 'u2']);
  });

  it('is identity when the lane is already there', () => {
    expect(moveLaneTo(board, 'u2', 1)).toBe(board);
  });

  it('is identity for an unknown lane', () => {
    expect(moveLaneTo(board, 'nope', 0)).toBe(board);
  });

  it('leaves a lane whose position did not change referentially identical', () => {
    // Swapping the last two leaves the first alone, so React can skip it.
    const next = moveLaneTo(board, 'u2', 2);
    expect(next[0]).toBe(board[0]);
  });

  it('never mutates the input board', () => {
    const before = JSON.stringify(board);
    moveLaneTo(board, 'u1', 2);
    expect(JSON.stringify(board)).toBe(before);
  });
});

describe('deleteLaneLines', () => {
  it('says the lane is empty when nothing comes off', () => {
    expect(deleteLaneLines(0)).toEqual([
      'This lane is empty, so no cards are affected.',
      'The lane itself is removed for good.',
    ]);
  });

  it('treats a negative count as empty', () => {
    expect(deleteLaneLines(-1)[0]).toBe('This lane is empty, so no cards are affected.');
  });

  it('singularises one card and says the issue is not deleted', () => {
    expect(deleteLaneLines(1)).toEqual([
      '1 card comes off the Kanban board. The issue is not deleted — it stays in the repository and can be put back on a lane at any time.',
      'The lane itself is removed for good.',
    ]);
  });

  it('pluralises and still says the issues are not deleted', () => {
    expect(deleteLaneLines(3)).toEqual([
      '3 cards come off the Kanban board. Those issues are not deleted — they stay in the repository and can be put back on a lane at any time.',
      'The lane itself is removed for good.',
    ]);
  });

  it('never describes the cards as deleted', () => {
    for (const count of [0, 1, 5]) {
      const body = deleteLaneLines(count).join(' ');
      expect(body).not.toMatch(/cards? (will be |are )?deleted/);
    }
  });
});
