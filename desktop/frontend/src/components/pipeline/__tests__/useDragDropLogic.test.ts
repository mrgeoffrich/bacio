import { describe, it, expect, vi } from 'vitest';
import { act, renderHook } from '@testing-library/react';
import { useDragDropLogic } from '../useDragDropLogic';
import type { DragDropHandlers } from '../useDragDropLogic';
import type { BoardCard, BoardCardBlocker } from '../../../api';

// mkCard builds a minimal BoardCard for the drop-routing tests — only key /
// column / blockedBy matter to useDragDropLogic.
function mkCard(key: string, column: string, blockedBy?: BoardCardBlocker[]): BoardCard {
  return { key, column, columnLabel: column, title: key, blockedBy } as BoardCard;
}

// A small board: two Backlog cards, one In-Pipeline, one Shipping.
const A = mkCard('BACI-1', 'todo');
const B = mkCard('BACI-2', 'todo');
const C = mkCard('BACI-3', 'in_pipeline');
const D = mkCard('BACI-4', 'to_be_shipped');
const cardByKey = new Map<string, BoardCard>([
  [A.key, A], [B.key, B], [C.key, C], [D.key, D],
]);

function setup(over: Partial<DragDropHandlers> = {}) {
  const handlers: DragDropHandlers = {
    onMoveCard: vi.fn(),
    onShip: vi.fn(),
    onReorder: vi.fn(),
    onCancelCard: vi.fn(),
    onDoneCard: vi.fn(),
    onBlockCard: vi.fn(),
    ...over,
  };
  const { result } = renderHook(() => useDragDropLogic(cardByKey, handlers));
  return { result, handlers };
}

// fakeEvent — a stand-in React.DragEvent that records preventDefault and
// lets us point currentTarget/target at the same/different nodes for the
// dragleave self-check.
function fakeEvent(same = true) {
  const node = {};
  return {
    preventDefault: vi.fn(),
    stopPropagation: vi.fn(),
    currentTarget: node,
    target: same ? node : {},
  } as unknown as React.DragEvent;
}

describe('useDragDropLogic — card move vs reorder', () => {
  it('reorders within the same column to the dropped card 1-based slot', () => {
    const { result, handlers } = setup();
    act(() => result.current.setDragKey('BACI-1'));
    act(() => result.current.dropOnCard(B, 1)); // drop A onto B at index 1
    expect(handlers.onReorder).toHaveBeenCalledWith('BACI-1', 2);
    expect(handlers.onMoveCard).not.toHaveBeenCalled();
  });

  it('does nothing when a card is dropped on itself', () => {
    const { result, handlers } = setup();
    act(() => result.current.setDragKey('BACI-1'));
    act(() => result.current.dropOnCard(A, 0));
    expect(handlers.onReorder).not.toHaveBeenCalled();
    expect(handlers.onMoveCard).not.toHaveBeenCalled();
  });

  it('cross-column drop onto a card moves it into the target column', () => {
    const { result, handlers } = setup();
    act(() => result.current.setDragKey('BACI-1')); // A is in todo
    act(() => result.current.dropOnCard(C, 0)); // onto an in_pipeline card
    expect(handlers.onMoveCard).toHaveBeenCalledWith('BACI-1', 'in_pipeline');
    expect(handlers.onReorder).not.toHaveBeenCalled();
  });

  it('column drop routes in_pipeline → Shipping through the Ship hand-off', () => {
    const { result, handlers } = setup();
    act(() => result.current.setDragKey('BACI-3')); // C is in_pipeline
    act(() => result.current.colDropProps('to_be_shipped').onDrop(fakeEvent()));
    expect(handlers.onShip).toHaveBeenCalledWith('BACI-3');
    expect(handlers.onMoveCard).not.toHaveBeenCalled();
  });

  it('column drop into a plain column is an ordinary move', () => {
    const { result, handlers } = setup();
    act(() => result.current.setDragKey('BACI-1')); // A is in todo
    act(() => result.current.colDropProps('in_pipeline').onDrop(fakeEvent()));
    expect(handlers.onMoveCard).toHaveBeenCalledWith('BACI-1', 'in_pipeline');
    expect(handlers.onShip).not.toHaveBeenCalled();
  });

  it('column drop onto the same column is a no-op', () => {
    const { result, handlers } = setup();
    act(() => result.current.setDragKey('BACI-1')); // A is in todo
    act(() => result.current.colDropProps('todo').onDrop(fakeEvent()));
    expect(handlers.onMoveCard).not.toHaveBeenCalled();
  });
});

describe('useDragDropLogic — terminal drop zones', () => {
  it('trash zone cancels the dragged card', () => {
    const { result, handlers } = setup();
    act(() => result.current.setDragKey('BACI-3'));
    act(() => result.current.trashDropProps().onDrop(fakeEvent()));
    expect(handlers.onCancelCard).toHaveBeenCalledWith('BACI-3');
  });

  it('done zone closes the dragged card as done', () => {
    const { result, handlers } = setup();
    act(() => result.current.setDragKey('BACI-3'));
    act(() => result.current.doneDropProps().onDrop(fakeEvent()));
    expect(handlers.onDoneCard).toHaveBeenCalledWith('BACI-3');
  });
});

describe('useDragDropLogic — drag-to-block classification', () => {
  it('classifies cards relative to the block-drag source', () => {
    // A is already blocked by B (dup target); C is a valid target; D is
    // Shipping (out of scope); A itself is the source.
    const blockedA = mkCard('BACI-1', 'todo', [{ key: 'BACI-2', state: 'todo' }]);
    const map = new Map<string, BoardCard>([
      [blockedA.key, blockedA], [B.key, B], [C.key, C], [D.key, D],
    ]);
    const { result } = renderHook(() => useDragDropLogic(map, {}));

    // No block-drag in flight: every card classifies as null.
    expect(result.current.blockTargetKind(C)).toBeNull();

    act(() => result.current.onBlockDragStart('BACI-1'));
    expect(result.current.blockTargetKind(blockedA)).toBe('source');
    expect(result.current.blockTargetKind(B)).toBe('dup');
    expect(result.current.blockTargetKind(C)).toBe('target');
    expect(result.current.blockTargetKind(D)).toBeNull(); // Shipping out of scope
  });

  it('only a valid target drop creates the blocks edge, and the gesture clears', () => {
    const { result, handlers } = setup();
    act(() => result.current.onBlockDragStart('BACI-1'));
    act(() => result.current.dropBlockOnCard(C)); // valid target
    expect(handlers.onBlockCard).toHaveBeenCalledWith('BACI-1', 'BACI-3');
    expect(result.current.blockDragKey).toBeNull(); // gesture ended
  });

  it('a self-drop block gesture is a silent no-op but still ends', () => {
    const { result, handlers } = setup();
    act(() => result.current.onBlockDragStart('BACI-1'));
    act(() => result.current.dropBlockOnCard(A)); // source === target
    expect(handlers.onBlockCard).not.toHaveBeenCalled();
    expect(result.current.blockDragKey).toBeNull();
  });
});

describe('useDragDropLogic — stable card handlers (BACI-366)', () => {
  // dropOnCard / dropBlockOnCard are the two handlers the React.memo'd cards
  // consume, so they must keep a constant identity across re-renders (poll /
  // drag-state churn) or the memo never bites — while still reading the latest
  // card lookup and drag key via refs.
  it('dropOnCard / dropBlockOnCard keep a stable identity across re-renders', () => {
    const handlers: DragDropHandlers = { onReorder: vi.fn(), onBlockCard: vi.fn() };
    const { result, rerender } = renderHook(
      ({ map }) => useDragDropLogic(map, handlers),
      { initialProps: { map: cardByKey } },
    );
    const firstDrop = result.current.dropOnCard;
    const firstBlockDrop = result.current.dropBlockOnCard;
    // Re-render with a *new* Map identity (what the 10s poll does) + a drag
    // state change.
    act(() => result.current.setDragKey('BACI-1'));
    rerender({ map: new Map(cardByKey) });
    expect(result.current.dropOnCard).toBe(firstDrop);
    expect(result.current.dropBlockOnCard).toBe(firstBlockDrop);
  });

  it('the stable dropOnCard still reads the latest drag key after a re-render', () => {
    const handlers: DragDropHandlers = { onReorder: vi.fn() };
    const { result, rerender } = renderHook(
      ({ map }) => useDragDropLogic(map, handlers),
      { initialProps: { map: cardByKey } },
    );
    act(() => result.current.setDragKey('BACI-1'));
    rerender({ map: new Map(cardByKey) }); // identity churns; ref must stay fresh
    act(() => result.current.dropOnCard(B, 1));
    expect(handlers.onReorder).toHaveBeenCalledWith('BACI-1', 2);
  });
});
