import { describe, it, expect } from 'vitest';
import {
  reshapeKanbanCardRef,
  reshapeKanbanColumn,
  reshapeKanbanBoard,
  reshapeKanbanColumnDeletePreview,
} from '../kanban';
import type { ApiKanbanColumn } from '../kanban';

// The pivot's lane reshapers. The load-bearing assertion is the one about
// `id`: HTTP nests `cards` inside the FULL model.KanbanColumn, so the
// payload also carries id / repo_id / timestamps, while the Wails
// KanbanColumnDTO ships only {uuid,name,position,cards}. The contract
// exposes `uuid` and never `id`, so a view can't accidentally start
// addressing lanes by a key that doesn't survive a sync round trip.

const DOING: ApiKanbanColumn = {
  id: 12,
  uuid: 'u-doing',
  repo_id: 7,
  name: 'Doing',
  position: 1,
  created_at: 'c',
  updated_at: 'u',
  cards: [
    { key: 'BACI-1', position: 0 },
    { key: 'BACI-9', position: 1 },
  ],
};

const EMPTY: ApiKanbanColumn = {
  id: 13,
  uuid: 'u-waiting',
  repo_id: 7,
  name: 'Waiting',
  position: 2,
  created_at: 'c',
  updated_at: 'u',
  cards: [],
};

describe('reshapeKanbanCardRef', () => {
  it('copies the placement rather than aliasing the wire object', () => {
    const wire = { key: 'BACI-1', position: 3 };
    const ref = reshapeKanbanCardRef(wire);
    expect(ref).toEqual({ key: 'BACI-1', position: 3 });
    expect(ref).not.toBe(wire);
  });
});

describe('reshapeKanbanColumn', () => {
  it('maps uuid / name / position and the ordered cards', () => {
    expect(reshapeKanbanColumn(DOING)).toEqual({
      uuid: 'u-doing',
      name: 'Doing',
      position: 1,
      cards: [
        { key: 'BACI-1', position: 0 },
        { key: 'BACI-9', position: 1 },
      ],
    });
  });

  it('drops the numeric id, repo_id and timestamps', () => {
    const col = reshapeKanbanColumn(DOING);
    expect(col).not.toHaveProperty('id');
    expect(col).not.toHaveProperty('repo_id');
    expect(col).not.toHaveProperty('created_at');
    expect(col).not.toHaveProperty('updated_at');
  });

  it('keeps an empty lane as an empty array, never undefined', () => {
    expect(reshapeKanbanColumn(EMPTY).cards).toEqual([]);
  });

  it('defends against an older server that omitted the cards slice', () => {
    // The contract promises `cards` is always an array so the React side
    // can map over it without a guard.
    const legacy = { ...EMPTY } as Partial<ApiKanbanColumn>;
    delete legacy.cards;
    expect(reshapeKanbanColumn(legacy as ApiKanbanColumn).cards).toEqual([]);
  });
});

describe('reshapeKanbanBoard', () => {
  it('preserves board order', () => {
    expect(reshapeKanbanBoard([DOING, EMPTY]).map(c => c.uuid)).toEqual(['u-doing', 'u-waiting']);
  });
});

describe('reshapeKanbanColumnDeletePreview', () => {
  it('flattens the nested cascade count onto the Wails-shaped DTO', () => {
    expect(reshapeKanbanColumnDeletePreview({
      column: {
        id: 12, uuid: 'u-doing', repo_id: 7, name: 'Doing',
        position: 1, created_at: 'c', updated_at: 'u',
      },
      cascade: { issues_removed_from_board: 4 },
      would_delete: true,
    })).toEqual({
      uuid: 'u-doing',
      name: 'Doing',
      issuesRemovedFromBoard: 4,
    });
  });
});
