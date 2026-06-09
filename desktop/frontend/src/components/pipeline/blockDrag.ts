import type React from 'react';

// blockKind classifies a card while a drag-to-block gesture is in flight:
// 'target' (a valid drop), 'source' (self-drop no-op), 'dup' (already
// blocked no-op), or null when no block-drag is active / the card is out
// of scope. Mirrors useDragDropLogic.blockTargetKind's return type.
export type BlockKind = 'target' | 'source' | 'dup' | null;

// BACI-342 drag-to-block helpers, shared by PipelineCard and StageCard so
// both card types render the block drop-target identically. blockKind is
// useDragDropLogic.blockTargetKind's classification ('target' | 'source' |
// 'dup' | null).

// blockDragClass maps a blockKind to the extra card class while a block-
// drag is in flight: a coral dashed ring on a valid 'target', a muted
// not-allowed variant on the 'source' (self-drop) and 'dup' (already
// blocked) no-ops, and nothing otherwise.
export function blockDragClass(blockKind: BlockKind | undefined): string {
  if (blockKind === 'target') return ' is-block-target';
  if (blockKind === 'source' || blockKind === 'dup') return ' is-block-noop';
  return '';
}

// blockDropProps returns the drag-over / drop handlers a card mounts while
// a block-drag is in flight. preventDefault on dragover marks the card a
// valid drop surface; the drop fires onBlockDrop (which the shell gates
// to the 'target' kind, so a drop on a source/dup card is a silent no-op).
// Returns empty handlers when no block-drag is active so the move/reorder
// drag keeps its own onDragOver/onDrop.
export type BlockDropProps = {
  onDragOver?: (e: React.DragEvent) => void;
  onDrop?: (e: React.DragEvent) => void;
};

export function blockDropProps(blockKind: BlockKind | undefined, onBlockDrop?: () => void): BlockDropProps {
  if (!blockKind) return {};
  return {
    onDragOver: (e) => { e.preventDefault(); e.stopPropagation(); },
    onDrop: (e) => { e.preventDefault(); e.stopPropagation(); onBlockDrop?.(); },
  };
}
