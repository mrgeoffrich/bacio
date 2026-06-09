import React from 'react';
import { useDragState } from './useDragState';
import type { DragState } from './useDragState';
import type { BlockKind } from './blockDrag';
import type { BoardCard } from '../../api';

// DragDropHandlers — the card mutations the drop logic routes to. All
// optional so the hook is happy in a partially-wired test / loading state,
// matching the optional-prop shape the handlers had on the board.
export type DragDropHandlers = {
  onMoveCard?: (key: string, col: string) => void;
  onShip?: (key: string) => void;
  onReorder?: (key: string, position: number) => void;
  onCancelCard?: (key: string) => void;
  onDoneCard?: (key: string) => void;
  onBlockCard?: (sourceKey: string, targetKey: string) => void;
};

// ColDropProps / drop-zone handler bundles the board mounts on its columns
// and the two terminal-outcome drop zones.
type DropZoneProps = {
  onDragOver: (e: React.DragEvent) => void;
  onDragLeave: (e: React.DragEvent) => void;
  onDrop: (e: React.DragEvent) => void;
};

export type DragDropLogic = DragState & {
  // blockTargetKind classifies a card as a block-drag drop target while a
  // block-drag is in flight (null when none / out of scope).
  blockTargetKind: (card: BoardCard) => BlockKind;
  // dropOnCard — reorder within a column, or cross-column move.
  dropOnCard: (targetCard: BoardCard, index: number) => void;
  // dropBlockOnCard — create the blocks edge on a valid 'target' drop.
  dropBlockOnCard: (targetCard: BoardCard) => void;
  // Per-column / per-zone drag handler bundles.
  colDropProps: (col: string) => DropZoneProps;
  trashDropProps: () => DropZoneProps;
  doneDropProps: () => DropZoneProps;
};

// useDragDropLogic owns the pipeline board's drag/drop behaviour: the
// card-move-vs-reorder routing, the BACI-342 drag-to-block gesture, and the
// BACI-268 / BACI-330 trash / done terminal drop zones. It holds the raw
// drag state (via useDragState) and derives the drop handlers from it plus
// the current card-by-key lookup and the card mutation handlers.
//
// The engine owns job progression — these handlers only move/reorder/close
// cards; Start / Stop / Auto live on the StageCard.
export function useDragDropLogic(
  cardByKey: Map<string, BoardCard>,
  handlers: DragDropHandlers,
): DragDropLogic {
  const drag = useDragState();
  const { dragKey, setDragKey, setDragOverCol, blockDragKey, setBlockDragKey } = drag;
  const { onMoveCard, onShip, onReorder, onCancelCard, onDoneCard, onBlockCard } = handlers;

  // Move a card into `col` (= its new state). in_pipeline → Shipping goes
  // through the Ship hand-off; everything else is a plain state move. Shared
  // by the column drop zone and the cross-column case of dropOnCard.
  const moveCardToColumn = (card: BoardCard, col: string) => {
    if (col === 'to_be_shipped' && card.column === 'in_pipeline') {
      onShip?.(card.key);
    } else {
      onMoveCard?.(card.key, col);
    }
  };

  // Cross-column drop onto a column's empty area.
  const dropToColumn = (col: string) => {
    setDragOverCol(null);
    const key = dragKey;
    setDragKey(null);
    if (!key) return;
    const card = cardByKey.get(key);
    if (!card || card.column === col) return;
    moveCardToColumn(card, col);
  };

  // Dropping onto a card. Within the same Backlog / Shipping list it's a
  // reorder to that card's 1-based slot; dropping a card from another column
  // onto one of these cards is a cross-column move into the target's column.
  // The card's own onDrop stops propagation, so the column drop zone never
  // sees the event — without the cross-column branch here the move silently
  // no-ops, which is BACI-269 (drag In Pipeline card onto a Backlog card).
  const dropOnCard = (targetCard: BoardCard, index: number) => {
    const key = dragKey;
    setDragKey(null);
    setDragOverCol(null);
    if (!key || key === targetCard.key) return;
    const dragged = cardByKey.get(key);
    if (!dragged) return;
    if (dragged.column !== targetCard.column) {
      moveCardToColumn(dragged, targetCard.column);
      return;
    }
    onReorder?.(key, index + 1);
  };

  // BACI-342 drag-to-block: classify a card as a drop target while a
  // block-drag is in flight. Returns null when no block-drag is active or
  // the card is out of scope (Shipping — blocking semantics aren't
  // actionable on a card already shipping), so the gesture is gated to
  // Backlog / In-Pipeline cards. Otherwise:
  //   'source' — the dragged card itself (self-drop is a no-op);
  //   'dup'    — the source is already blocked by this card (no-op);
  //   'target' — a valid drop that would create the blocks edge.
  // The source/dup cards render a muted not-allowed variant so the user
  // sees the no-op before releasing.
  const blockTargetKind = (card: BoardCard): BlockKind => {
    if (!blockDragKey || card.column === 'to_be_shipped') return null;
    if (card.key === blockDragKey) return 'source';
    const source = cardByKey.get(blockDragKey);
    if (source && (source.blockedBy || []).some(b => b.key === card.key)) return 'dup';
    return 'target';
  };

  // Drop a block-drag onto a card. Only a 'target' classification creates
  // the edge (source/dup are silent no-ops); clearing blockDragKey happens
  // unconditionally so a no-op drop still ends the gesture.
  const dropBlockOnCard = (targetCard: BoardCard) => {
    const sourceKey = blockDragKey;
    setBlockDragKey(null);
    if (!sourceKey) return;
    if (blockTargetKind(targetCard) !== 'target') return;
    onBlockCard?.(sourceKey, targetCard.key);
  };

  const colDropProps = (col: string): DropZoneProps => ({
    onDragOver: (e) => { e.preventDefault(); setDragOverCol(col); },
    onDragLeave: (e) => { if (e.currentTarget === e.target) setDragOverCol(null); },
    onDrop: (e) => { e.preventDefault(); dropToColumn(col); },
  });

  // BACI-268: drop onto the trash bin cancels the dragged issue. Mirrors
  // dropToColumn but routes to onCancelCard instead of a column move; the
  // bin accepts a card from any column (no column guard) since `cancelled`
  // is a plain terminal transition.
  const cancelToTrash = () => {
    const key = dragKey;
    setDragKey(null);
    setDragOverCol(null);
    if (!key) return;
    const card = cardByKey.get(key);
    if (!card) return;
    onCancelCard?.(card.key);
  };

  const trashDropProps = (): DropZoneProps => ({
    onDragOver: (e) => { e.preventDefault(); setDragOverCol('trash'); },
    onDragLeave: (e) => { if (e.currentTarget === e.target) setDragOverCol(null); },
    onDrop: (e) => { e.preventDefault(); cancelToTrash(); },
  });

  // BACI-330: drop onto the Done zone closes the dragged issue as `done`.
  // Twin of cancelToTrash — routes to onDoneCard instead of onCancelCard;
  // accepts a card from any column since `done` is a plain terminal
  // transition (the same shape as cancel).
  const doneToTray = () => {
    const key = dragKey;
    setDragKey(null);
    setDragOverCol(null);
    if (!key) return;
    const card = cardByKey.get(key);
    if (!card) return;
    onDoneCard?.(card.key);
  };

  const doneDropProps = (): DropZoneProps => ({
    onDragOver: (e) => { e.preventDefault(); setDragOverCol('done'); },
    onDragLeave: (e) => { if (e.currentTarget === e.target) setDragOverCol(null); },
    onDrop: (e) => { e.preventDefault(); doneToTray(); },
  });

  return {
    ...drag,
    blockTargetKind,
    dropOnCard,
    dropBlockOnCard,
    colDropProps,
    trashDropProps,
    doneDropProps,
  };
}
