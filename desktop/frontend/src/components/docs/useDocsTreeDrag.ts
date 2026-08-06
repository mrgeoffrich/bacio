import { useCallback, useMemo, useState } from 'react';
import type React from 'react';
import { folderAncestry } from '../../lib/docsFilter';
import type { Folder } from '../../lib/docsFilter';

// useDocsTreeDrag — drag-to-move for the Documents page tree.
//
// Hand-rolled HTML5 drag and drop, deliberately: the same pattern the
// pipeline board has proven since BACI-268 (components/pipeline/
// useDragDropLogic.ts + useDragState.ts). No DnD library is added for this —
// the gesture set here is smaller than the board's (no cross-column
// semantics, no drag-to-block), and a library would be a new dependency to
// keep in lockstep with React for one screen.
//
// Two gestures, matching the two things the store can express:
//   • drop ONTO a folder row (or the rail's root background) — file the
//     dragged page/folder into that folder, appended (`position: null`).
//   • drop ON a page row — insert the dragged page before it, inside that
//     page's folder, at its index. This is the drop-line gesture in the
//     mockup. Folders have no ordering mutator on the seam (moveDocFolder
//     re-parents only), so a folder drag only ever means "re-parent".

// The thing being dragged. `parentUuid` is where it currently lives, so the
// drop handler can skip a move that would be a no-op.
export type DocsDragItem =
  | { kind: 'doc'; filename: string; parentUuid: string }
  | { kind: 'folder'; uuid: string; parentUuid: string };

// Where it would land. `folderUuid: ''` is the tree ROOT — a value, not an
// absence.
export type DocsDropTarget =
  | { kind: 'into'; folderUuid: string }
  | { kind: 'before'; folderUuid: string; index: number; filename: string };

type DropZoneProps = {
  onDragOver: (e: React.DragEvent) => void;
  onDragLeave: (e: React.DragEvent) => void;
  onDrop: (e: React.DragEvent) => void;
};

export type DocsTreeDrag = {
  dragItem: DocsDragItem | null;
  dropTarget: DocsDropTarget | null;
  // Drag-source props for a row (spread onto the row element).
  dragProps: (item: DocsDragItem) => {
    draggable: true;
    onDragStart: (e: React.DragEvent) => void;
    onDragEnd: () => void;
  };
  // Drop-zone props: a folder row, a page row, and the rail's empty
  // background (which means "the root").
  folderDropProps: (folderUuid: string) => DropZoneProps;
  docDropProps: (filename: string, folderUuid: string, index: number) => DropZoneProps;
  rootDropProps: () => DropZoneProps;
  // True while THIS folder row is the live "drop into me" target.
  isDropInto: (folderUuid: string) => boolean;
  // True while the drop line should render above THIS page row.
  isDropBefore: (filename: string) => boolean;
};

type UseDocsTreeDragArgs = {
  folders: Folder[];
  onMove: (item: DocsDragItem, target: DocsDropTarget) => void;
};

export function useDocsTreeDrag({ folders, onMove }: UseDocsTreeDragArgs): DocsTreeDrag {
  const [dragItem, setDragItem] = useState<DocsDragItem | null>(null);
  const [dropTarget, setDropTarget] = useState<DocsDropTarget | null>(null);

  // canDropInto rejects the two moves the store would refuse anyway —
  // a folder into itself, and a folder into its own descendant (the cycle
  // the store's ancestor walk guards). Refusing them here means the drop
  // indicator never lights on a target that would error.
  const canDropInto = useCallback(
    (item: DocsDragItem | null, folderUuid: string): boolean => {
      if (!item) return false;
      if (item.kind === 'doc') return true;
      if (item.uuid === folderUuid) return false;
      return !folderAncestry(folders, folderUuid).some(f => f.uuid === item.uuid);
    },
    [folders],
  );

  const dragProps = useCallback(
    (item: DocsDragItem) => ({
      draggable: true as const,
      onDragStart: (e: React.DragEvent) => {
        e.stopPropagation();
        setDragItem(item);
        // Firefox refuses to start a drag without payload; the payload
        // itself is unused (the item lives in React state).
        e.dataTransfer.effectAllowed = 'move';
        try {
          e.dataTransfer.setData('text/plain', item.kind === 'doc' ? item.filename : item.uuid);
        } catch {
          /* some webviews lock dataTransfer down — the drag still works */
        }
      },
      onDragEnd: () => {
        setDragItem(null);
        setDropTarget(null);
      },
    }),
    [],
  );

  const folderDropProps = useCallback(
    (folderUuid: string): DropZoneProps => ({
      onDragOver: (e) => {
        if (!canDropInto(dragItem, folderUuid)) return;
        e.preventDefault();
        e.stopPropagation();
        e.dataTransfer.dropEffect = 'move';
        setDropTarget({ kind: 'into', folderUuid });
      },
      onDragLeave: (e) => {
        e.stopPropagation();
        setDropTarget(t => (t && t.kind === 'into' && t.folderUuid === folderUuid ? null : t));
      },
      onDrop: (e) => {
        e.preventDefault();
        e.stopPropagation();
        const item = dragItem;
        setDragItem(null);
        setDropTarget(null);
        if (!canDropInto(item, folderUuid) || !item) return;
        if (item.parentUuid === folderUuid) return; // already there
        onMove(item, { kind: 'into', folderUuid });
      },
    }),
    [canDropInto, dragItem, onMove],
  );

  const docDropProps = useCallback(
    (filename: string, folderUuid: string, index: number): DropZoneProps => ({
      onDragOver: (e) => {
        // Only pages take a between-rows drop: folders sort before pages at
        // every level, so "between two pages" is not a position a folder can
        // occupy.
        if (!dragItem || dragItem.kind !== 'doc' || dragItem.filename === filename) return;
        e.preventDefault();
        e.stopPropagation();
        e.dataTransfer.dropEffect = 'move';
        setDropTarget({ kind: 'before', folderUuid, index, filename });
      },
      onDragLeave: (e) => {
        e.stopPropagation();
        setDropTarget(t => (t && t.kind === 'before' && t.filename === filename ? null : t));
      },
      onDrop: (e) => {
        e.preventDefault();
        e.stopPropagation();
        const item = dragItem;
        setDragItem(null);
        setDropTarget(null);
        if (!item || item.kind !== 'doc' || item.filename === filename) return;
        onMove(item, { kind: 'before', folderUuid, index, filename });
      },
    }),
    [dragItem, onMove],
  );

  const rootDropProps = useCallback((): DropZoneProps => ({
    onDragOver: (e) => {
      if (!canDropInto(dragItem, '')) return;
      e.preventDefault();
      e.dataTransfer.dropEffect = 'move';
      setDropTarget({ kind: 'into', folderUuid: '' });
    },
    onDragLeave: () => {
      setDropTarget(t => (t && t.kind === 'into' && t.folderUuid === '' ? null : t));
    },
    onDrop: (e) => {
      e.preventDefault();
      const item = dragItem;
      setDragItem(null);
      setDropTarget(null);
      if (!item || !canDropInto(item, '')) return;
      if (item.parentUuid === '') return;
      onMove(item, { kind: 'into', folderUuid: '' });
    },
  }), [canDropInto, dragItem, onMove]);

  const isDropInto = useCallback(
    (folderUuid: string) => !!dropTarget && dropTarget.kind === 'into' && dropTarget.folderUuid === folderUuid,
    [dropTarget],
  );
  const isDropBefore = useCallback(
    (filename: string) => !!dropTarget && dropTarget.kind === 'before' && dropTarget.filename === filename,
    [dropTarget],
  );

  return useMemo(
    () => ({
      dragItem,
      dropTarget,
      dragProps,
      folderDropProps,
      docDropProps,
      rootDropProps,
      isDropInto,
      isDropBefore,
    }),
    [dragItem, dropTarget, dragProps, folderDropProps, docDropProps, rootDropProps, isDropInto, isDropBefore],
  );
}
