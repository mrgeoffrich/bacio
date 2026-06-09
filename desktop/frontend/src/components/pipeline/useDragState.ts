import { useState, useCallback } from 'react';
import type { Dispatch, SetStateAction } from 'react';

// DragState is the raw drag-gesture state the pipeline board owns: the
// card-move drag (dragKey + the column/sentinel it's over), the BACI-342
// drag-to-block gesture (blockDragKey, kept distinct so the two never
// alias), and the BACI-310 blocked-by hover highlight. useDragDropLogic
// builds the drop handlers on top of this; the shell reads the keys to
// drive the per-card highlight classes.
export type DragState = {
  // dragKey — the card being moved/reordered, or null.
  dragKey: string | null;
  setDragKey: Dispatch<SetStateAction<string | null>>;
  // dragOverCol — the column the dragged card is over, driving the
  // `is-drop` highlight. BACI-268 widens it with a non-column 'trash'
  // sentinel (drag-to-cancel bin); BACI-330 adds a 'done' sentinel
  // (drag-to-done zone) — both only feed an `=== <sentinel>` check.
  dragOverCol: string | null;
  setDragOverCol: Dispatch<SetStateAction<string | null>>;
  // blockDragKey (BACI-342) — the source card key while a drag-to-block
  // gesture is in flight, distinct from dragKey so the two gestures never
  // alias. While non-null, in-scope cards paint a coral "drop to block"
  // highlight and a drop fires onBlockCard(blockDragKey, target).
  blockDragKey: string | null;
  setBlockDragKey: Dispatch<SetStateAction<string | null>>;
  // highlightKey (BACI-310) — the blocker key currently hovered in some
  // card's blocked-by badge; the card whose key matches paints red.
  highlightKey: string | null;
  onHighlight: (k: string | null) => void;
  onBlockDragStart: (k: string) => void;
  onBlockDragEnd: () => void;
};

export function useDragState(): DragState {
  const [dragKey, setDragKey] = useState<string | null>(null);
  const [dragOverCol, setDragOverCol] = useState<string | null>(null);
  const [highlightKey, setHighlightKey] = useState<string | null>(null);
  const [blockDragKey, setBlockDragKey] = useState<string | null>(null);

  const onHighlight = useCallback((k: string | null) => setHighlightKey(k), []);
  const onBlockDragStart = useCallback((k: string) => setBlockDragKey(k), []);
  const onBlockDragEnd = useCallback(() => setBlockDragKey(null), []);

  return {
    dragKey, setDragKey,
    dragOverCol, setDragOverCol,
    blockDragKey, setBlockDragKey,
    highlightKey, onHighlight,
    onBlockDragStart, onBlockDragEnd,
  };
}
