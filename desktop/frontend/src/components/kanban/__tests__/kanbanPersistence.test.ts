import { describe, it, expect, beforeEach } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import {
  columnSetFor,
  columnSetMapCodec,
  persistKanbanScroll,
  readKanbanScroll,
  useColumnSetPref,
  withColumnSet,
  SCROLL_STORAGE_KEY,
} from '../kanbanPersistence';
import { STORAGE_KEY as COLLAPSE_KEY, useCollapsedColumns } from '../boardCollapsePersistence';
import { STORAGE_KEY as COMPACT_KEY } from '../boardCompactPersistence';

describe('columnSetMapCodec', () => {
  it('round-trips a per-repo map', () => {
    const map = { BACI: ['u1', 'u2'], MINI: ['u3'] };
    expect(columnSetMapCodec.deserialize(columnSetMapCodec.serialize(map))).toEqual(map);
  });

  it('falls back to an empty map on anything malformed', () => {
    expect(columnSetMapCodec.deserialize('not json')).toEqual({});
    expect(columnSetMapCodec.deserialize('null')).toEqual({});
    expect(columnSetMapCodec.deserialize('["a"]')).toEqual({});
  });

  it('drops non-array repo entries and non-string members', () => {
    expect(columnSetMapCodec.deserialize('{"BACI":"nope","MINI":["u1",7,null,"u2"]}'))
      .toEqual({ MINI: ['u1', 'u2'] });
  });
});

describe('columnSetFor / withColumnSet', () => {
  it('reads a repo entry as a Set and treats an absent repo as empty', () => {
    expect(columnSetFor({ BACI: ['u1'] }, 'BACI')).toEqual(new Set(['u1']));
    expect(columnSetFor({ BACI: ['u1'] }, 'MINI')).toEqual(new Set());
    expect(columnSetFor({ BACI: ['u1'] }, '')).toEqual(new Set());
  });

  it('trims the repo entry entirely when its set empties', () => {
    expect(withColumnSet({ BACI: ['u1'], MINI: ['u2'] }, 'BACI', new Set())).toEqual({ MINI: ['u2'] });
  });
});

describe('useColumnSetPref', () => {
  beforeEach(() => localStorage.clear());

  it('persists per repo and re-derives the set on a repo switch', () => {
    const { result, rerender } = renderHook(
      ({ repo }: { repo: string }) => useColumnSetPref('test-key', repo),
      { initialProps: { repo: 'BACI' } },
    );

    act(() => result.current.add('u1'));
    expect(result.current.set).toEqual(new Set(['u1']));
    expect(JSON.parse(localStorage.getItem('test-key') ?? '{}')).toEqual({ BACI: ['u1'] });

    // Switching repos re-derives from the same map — no carry-over, and no
    // effect needed to re-seed (the map itself is the persisted value).
    rerender({ repo: 'MINI' });
    expect(result.current.set).toEqual(new Set());

    rerender({ repo: 'BACI' });
    expect(result.current.set).toEqual(new Set(['u1']));

    act(() => result.current.remove('u1'));
    expect(result.current.set).toEqual(new Set());
    expect(JSON.parse(localStorage.getItem('test-key') ?? '{}')).toEqual({});
  });

  it('seeds from an already-persisted map', () => {
    localStorage.setItem('test-key', JSON.stringify({ BACI: ['u7'] }));
    const { result } = renderHook(() => useColumnSetPref('test-key', 'BACI'));
    expect(result.current.set).toEqual(new Set(['u7']));
  });

  it('ignores writes when no repo is active', () => {
    const { result } = renderHook(() => useColumnSetPref('test-key', ''));
    act(() => result.current.add('u1'));
    expect(result.current.set).toEqual(new Set());
  });
});

describe('lane preference keys', () => {
  beforeEach(() => localStorage.clear());

  // Collapsed and compact are independent preferences, so they must not share
  // a key — a lane can be compact without being collapsed.
  it('keeps collapsed and compact on separate keys', () => {
    expect(COLLAPSE_KEY).not.toBe(COMPACT_KEY);
  });

  // The pre-pivot keys held `model.State` strings; a lane is addressed by
  // uuid, so the old payloads are meaningless rather than merely stale.
  it('does not reuse the pre-pivot state-keyed storage keys', () => {
    expect(COLLAPSE_KEY).not.toBe('bacio-board-collapsed-columns');
    expect(COMPACT_KEY).not.toBe('bacio-board-compact-columns');
  });

  it('wires useCollapsedColumns to its own key', () => {
    const { result } = renderHook(() => useCollapsedColumns('BACI'));
    act(() => result.current.add('u1'));
    expect(JSON.parse(localStorage.getItem(COLLAPSE_KEY) ?? '{}')).toEqual({ BACI: ['u1'] });
    // The compact hook isn't mounted here, so its key stays untouched — the
    // point of the assertion is that the two never share storage.
    expect(localStorage.getItem(COMPACT_KEY)).toBeNull();
  });
});

describe('kanban scroll offset', () => {
  beforeEach(() => localStorage.clear());

  it('round-trips a per-repo offset, rounded and clamped at zero', () => {
    persistKanbanScroll('BACI', 120.6);
    persistKanbanScroll('MINI', -5);
    expect(readKanbanScroll('BACI')).toBe(121);
    expect(readKanbanScroll('MINI')).toBe(0);
  });

  it('reads 0 for an unknown repo, a blank prefix and a malformed payload', () => {
    expect(readKanbanScroll('NOPE')).toBe(0);
    expect(readKanbanScroll('')).toBe(0);
    localStorage.setItem(SCROLL_STORAGE_KEY, 'not json');
    expect(readKanbanScroll('BACI')).toBe(0);
  });
});
