import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import type { UIEvent } from 'react';
import { Plus } from 'lucide-react';
import KanbanLane from './KanbanLane';
import LaneDeleteDialog from './LaneDeleteDialog';
import LaneNameDialog from './LaneNameDialog';
import { useCollapsedColumns } from './boardCollapsePersistence';
import { useCompactColumns } from './boardCompactPersistence';
import { persistKanbanScroll, readKanbanScroll } from './kanbanPersistence';
import { laneShiftTarget, moveLaneTo } from './kanbanLanes';
import { boardHintText, offBoardCards, removeCardFromBoard } from './kanbanOffBoard';
import { laneOf, placeCardInLane, resolveLaneCards } from './kanbanPlacement';
import * as api from '../../api';
import type { BoardCard, KanbanColumn } from '../../api';
import { useAsyncResource } from '../../lib/hooks/useAsyncResource';
import { useOptimisticMutation } from '../../lib/hooks/useOptimisticMutation';
import { useActiveRepo } from '../../state/RepoProvider';
import { useCards } from '../../state/CardsProvider';

// KanbanBoard — the human work board, mounted at `/<prefix>/issues`.
//
// It is a SEPARATE AXIS from the Agentic Pipeline, not a second view of it.
// A card is on the Kanban if and only if some lane's `cards` lists it
// (`issues.kanban_column_id IS NOT NULL` server-side), which is orthogonal to
// the issue's `state`. Workspaces put every issue on a lane at creation; git
// repos start with an empty board and cards are opted in explicitly, so the
// two surfaces never double-render the same work by accident.
//
// Membership rides on the CONTAINER — BoardCard carries no lane field — so
// this component holds two resources and joins them: the lanes (with their
// ordered card references) from `api.listKanbanColumns`, and the card detail
// from the CardsProvider list the whole app already polls. That poll is live
// on this route for free: CardsProvider keys its 10s interval on
// `activeView === 'board' | 'pipeline'`, and `/<prefix>/issues` maps back to
// the `board` view id.
//
// The same two resources also give the board its OFF-board set for free —
// every card in the repo that no lane holds — which is what the lane
// header's "+" picker offers. See kanbanOffBoard: no third endpoint, no
// second fetch, just the difference between the two lists we already have.
//
// The drag-and-drop is the pre-pivot board's hand-rolled HTML5 implementation
// (dragstart on the card, dragover/drop on the lane) — deliberately no DnD
// library. The drop writes through `useOptimisticMutation` so the card lands
// under the cursor immediately and reverts if the server refuses; so does
// every one of the Phase 7B mutations below.
//
// Phase 7B closed the two holes 6A left: there was no in-app way to put a
// card ON the board (drag only moved cards BETWEEN existing lanes, which is
// a no-op on a git repo whose board starts empty), and no lane CRUD at all.
// Both used to be CLI-only, and the hint text used to say so.

// What the lane dialog is currently doing. Rename and delete carry the
// lane's name captured at open time, so the dialog keeps reading right
// even if a poll lands mid-edit.
type LaneDialog =
  | { kind: 'create' }
  | { kind: 'rename'; uuid: string; name: string }
  | { kind: 'delete'; uuid: string; name: string };

// A store refusal ("a lane called Doing already exists") belongs next to
// the input that caused it, so the dialogs render it inline instead of
// letting it through to the global error modal — which would close the
// dialog and lose what was typed.
function messageOf(err: unknown): string {
  if (err instanceof Error && err.message) return err.message;
  const s = String(err ?? '');
  return s || 'Something went wrong';
}

export default function KanbanBoard() {
  const { activeBoard, openCard, openIssue } = useActiveRepo();
  const { cards } = useCards();
  const mutate = useOptimisticMutation();

  // The lanes. `useAsyncResource` owns the load / stale-guard / error policy;
  // `setColumns` is the optimistic hook the drop handler writes through, and
  // the seam's move call answers with the whole re-densified board so success
  // is a straight replace.
  const {
    data: columns,
    loading,
    setData: setColumns,
  } = useAsyncResource<KanbanColumn[]>(
    () => api.listKanbanColumns(activeBoard),
    [],
    [activeBoard],
    { enabled: !!activeBoard, errorHeadline: "Couldn't load the Kanban board" },
  );

  const [dragKey, setDragKey] = useState<string | null>(null);
  const [overLane, setOverLane] = useState<string | null>(null);

  // Lane CRUD dialog state. `busy` gates the in-flight submit and `error`
  // is the inline store refusal; both reset on every open.
  const [dialog, setDialog] = useState<LaneDialog | null>(null);
  const [dialogBusy, setDialogBusy] = useState(false);
  const [dialogError, setDialogError] = useState('');

  // Per-lane UI preferences, persisted per repo and keyed on lane uuid.
  const collapsed = useCollapsedColumns(activeBoard);
  const compact = useCompactColumns(activeBoard);

  // Latest-value refs so the mutation handlers can read the current lanes /
  // pending dialog without listing them as dependencies — the handlers are
  // threaded down to the memo'd cards, so a fresh identity every poll would
  // defeat the memo.
  const columnsRef = useRef(columns);
  const dialogRef = useRef(dialog);
  useEffect(() => {
    columnsRef.current = columns;
    dialogRef.current = dialog;
  });

  const cardsByKey = useMemo(
    () => new Map(cards.map((card): [string, BoardCard] => [card.key, card])),
    [cards],
  );
  const lanes = useMemo(
    () => columns.map(column => ({ column, cards: resolveLaneCards(column, cardsByKey) })),
    [columns, cardsByKey],
  );
  // The "+" picker's candidates: every card no lane holds. One pass over
  // the board, shared by reference across every lane.
  const offBoard = useMemo(() => offBoardCards(columns, cards), [columns, cards]);
  const onBoardCount = cards.length - offBoard.length;

  // ─── Drag & drop ───────────────────────────────────────────────────

  const onCardDragStart = useCallback((key: string) => setDragKey(key), []);
  const onCardDragEnd = useCallback(() => {
    setDragKey(null);
    setOverLane(null);
  }, []);
  const onDragOverLane = useCallback((uuid: string) => setOverLane(uuid), []);
  const onDragLeaveLane = useCallback(() => setOverLane(null), []);

  // placeCard is the shared body of "put this card in this lane" — the
  // drop handler and the "+" picker differ only in how they choose the
  // card, so they write through one path. The pre-write snapshot doubles
  // as the rollback.
  const placeCard = useCallback((key: string, uuid: string) => {
    const before = columnsRef.current;
    if (laneOf(before, key)?.uuid === uuid) return;
    // Landing a card in a collapsed lane expands it, so the user sees the
    // card arrive instead of it vanishing behind the rotated title.
    collapsed.remove(uuid);
    mutate({
      optimisticUpdate: () => {
        setColumns(placeCardInLane(before, key, uuid));
      },
      persist: () => api.moveIssueToKanbanColumn(activeBoard, key, uuid, null),
      onSuccess: (board) => setColumns(board),
      rollback: () => setColumns(before),
      errorHeadline: "Couldn't move card",
    });
  }, [activeBoard, collapsed, mutate, setColumns]);

  // The drop: move the dragged card into this lane, appending at the bottom.
  // The no-op guard inside placeCard reads the live lanes off the ref BEFORE
  // the optimistic update so a drop back onto the source lane costs no round
  // trip.
  const onDropOnLane = useCallback((uuid: string) => {
    const key = dragKey;
    setDragKey(null);
    setOverLane(null);
    if (key) placeCard(key, uuid);
  }, [dragKey, placeCard]);

  // ─── Card membership ───────────────────────────────────────────────

  const onAddCard = useCallback((uuid: string, key: string) => {
    placeCard(key, uuid);
  }, [placeCard]);

  // The inverse of "+": an empty column uuid is how the seam un-opts a
  // card, putting `kanban_column_id` back to NULL. The issue is untouched
  // — it stays in the repo and on the Agentic Pipeline.
  const onTakeCardOffBoard = useCallback((key: string) => {
    const before = columnsRef.current;
    if (!laneOf(before, key)) return;
    mutate({
      optimisticUpdate: () => {
        setColumns(removeCardFromBoard(before, key));
      },
      persist: () => api.moveIssueToKanbanColumn(activeBoard, key, '', null),
      onSuccess: (board) => setColumns(board),
      rollback: () => setColumns(before),
      errorHeadline: "Couldn't take the card off the board",
    });
  }, [activeBoard, mutate, setColumns]);

  // ─── Lane CRUD ─────────────────────────────────────────────────────

  const closeDialog = useCallback(() => {
    setDialog(null);
    setDialogBusy(false);
    setDialogError('');
  }, []);

  const openDialog = useCallback((next: LaneDialog) => {
    setDialogError('');
    setDialogBusy(false);
    setDialog(next);
  }, []);

  const onCreateLane = useCallback(() => openDialog({ kind: 'create' }), [openDialog]);
  const onRenameLane = useCallback((uuid: string) => {
    const column = columnsRef.current.find(c => c.uuid === uuid);
    if (!column) return;
    openDialog({ kind: 'rename', uuid, name: column.name });
  }, [openDialog]);
  const onDeleteLane = useCallback((uuid: string) => {
    const column = columnsRef.current.find(c => c.uuid === uuid);
    if (!column) return;
    openDialog({ kind: 'delete', uuid, name: column.name });
  }, [openDialog]);

  // Create and rename both answer with the single lane they wrote (not the
  // whole board — only reorder / delete / issue-move re-densify siblings),
  // so each splices its answer into place rather than re-listing.
  const submitLaneName = useCallback((value: string) => {
    const pending = dialogRef.current;
    if (!pending || pending.kind === 'delete') return;
    setDialogBusy(true);
    setDialogError('');
    mutate({
      persist: () => (pending.kind === 'create'
        ? api.createKanbanColumn(activeBoard, value)
        : api.renameKanbanColumn(activeBoard, pending.uuid, value)),
      onSuccess: (column) => {
        setColumns(prev => (pending.kind === 'create'
          ? [...prev, column]
          : prev.map(c => (c.uuid === column.uuid ? column : c))));
        closeDialog();
      },
      onError: (err) => {
        setDialogBusy(false);
        setDialogError(messageOf(err));
      },
    });
  }, [activeBoard, closeDialog, mutate, setColumns]);

  const confirmDeleteLane = useCallback(() => {
    const pending = dialogRef.current;
    if (!pending || pending.kind !== 'delete') return;
    setDialogBusy(true);
    setDialogError('');
    mutate({
      persist: () => api.deleteKanbanColumn(activeBoard, pending.uuid),
      onSuccess: (board) => {
        setColumns(board);
        closeDialog();
      },
      onError: (err) => {
        setDialogBusy(false);
        setDialogError(messageOf(err));
      },
    });
  }, [activeBoard, closeDialog, mutate, setColumns]);

  // Reorder is a left / right nudge: the lane's array index IS the 0-based
  // position the store wants, and the answer is the whole re-densified
  // board.
  const onMoveLane = useCallback((uuid: string, delta: number) => {
    const before = columnsRef.current;
    const to = laneShiftTarget(before, uuid, delta);
    if (to === null) return;
    mutate({
      optimisticUpdate: () => {
        setColumns(moveLaneTo(before, uuid, to));
      },
      persist: () => api.reorderKanbanColumn(activeBoard, uuid, to),
      onSuccess: (board) => setColumns(board),
      rollback: () => setColumns(before),
      errorHeadline: "Couldn't reorder the lane",
    });
  }, [activeBoard, mutate, setColumns]);

  // ─── Horizontal scroll persistence ─────────────────────────────────

  const boardRef = useRef<HTMLDivElement>(null);
  const scrollWriteTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  // Restore the saved offset before paint (useLayoutEffect, so there's no
  // flash at 0 first). The card / lane counts are dependencies because both
  // load async: the first mount may render a 0-width board, which clamps any
  // restore to 0; re-running once the content lands lets the assignment
  // stick. Setting scrollLeft past the current scrollWidth is a harmless
  // no-op — the browser clamps.
  useLayoutEffect(() => {
    const el = boardRef.current;
    if (!el || !activeBoard) return;
    const saved = readKanbanScroll(activeBoard);
    if (saved > 0 && el.scrollLeft !== saved) el.scrollLeft = saved;
  }, [activeBoard, cards.length, columns.length]);

  // Persist on scroll with a small debounce. A cleanup-on-unmount write would
  // also work, but capturing live also covers tab-close / window-quit paths
  // where React never gets a clean unmount.
  const onBoardScroll = useCallback((e: UIEvent<HTMLDivElement>) => {
    const left = e.currentTarget.scrollLeft;
    if (scrollWriteTimer.current) clearTimeout(scrollWriteTimer.current);
    scrollWriteTimer.current = setTimeout(() => persistKanbanScroll(activeBoard, left), 150);
  }, [activeBoard]);

  useEffect(() => () => {
    if (scrollWriteTimer.current) clearTimeout(scrollWriteTimer.current);
  }, []);

  // ─── Render ────────────────────────────────────────────────────────

  if (!activeBoard) {
    return <div className="mk-app-state">Select a repository to view its Kanban board.</div>;
  }
  if (loading && columns.length === 0) {
    return <div className="mk-app-state">Loading…</div>;
  }

  // No early return for a lane-less board any more: the trailing "Add lane"
  // slot IS the empty state, so a repo with no lanes renders the grid with
  // just that slot in it and there is never a dead end.
  const hint = boardHintText(columns.length, onBoardCount, offBoard.length);

  return (
    <div className="mk-kanban">
      <div className="mk-board" ref={boardRef} onScroll={onBoardScroll}>
        {lanes.map(({ column, cards: laneCards }, index) => (
          <KanbanLane
            key={column.uuid}
            column={column}
            cards={laneCards}
            offBoardCards={offBoard}
            isOver={overLane === column.uuid}
            isCollapsed={collapsed.set.has(column.uuid)}
            isCompact={compact.set.has(column.uuid)}
            canMoveLeft={index > 0}
            canMoveRight={index < lanes.length - 1}
            draggingKey={dragKey}
            onDragOverLane={onDragOverLane}
            onDragLeaveLane={onDragLeaveLane}
            onDropOnLane={onDropOnLane}
            onCollapse={collapsed.add}
            onExpand={collapsed.remove}
            onCompact={compact.add}
            onUncompact={compact.remove}
            onOpenCard={openCard}
            onOpenIssue={openIssue}
            onCardDragStart={onCardDragStart}
            onCardDragEnd={onCardDragEnd}
            onAddCard={onAddCard}
            onTakeCardOffBoard={onTakeCardOffBoard}
            onRenameLane={onRenameLane}
            onMoveLane={onMoveLane}
            onDeleteLane={onDeleteLane}
          />
        ))}
        <button type="button" className="mk-col-add-lane" onClick={onCreateLane}>
          <Plus size={16} strokeWidth={2} aria-hidden="true" />
          <span>Add lane</span>
        </button>
      </div>
      {hint && <div className="mk-kanban-hint">{hint}</div>}

      {dialog?.kind === 'create' && (
        <LaneNameDialog
          title="Add a lane"
          initialValue=""
          submitLabel="Add lane"
          busyLabel="Adding…"
          busy={dialogBusy}
          error={dialogError}
          onSubmit={submitLaneName}
          onClose={closeDialog}
        />
      )}
      {dialog?.kind === 'rename' && (
        <LaneNameDialog
          title="Rename lane"
          initialValue={dialog.name}
          submitLabel="Rename"
          busyLabel="Renaming…"
          busy={dialogBusy}
          error={dialogError}
          onSubmit={submitLaneName}
          onClose={closeDialog}
        />
      )}
      {dialog?.kind === 'delete' && (
        <LaneDeleteDialog
          repoPrefix={activeBoard}
          uuid={dialog.uuid}
          name={dialog.name}
          busy={dialogBusy}
          error={dialogError}
          onConfirm={confirmDeleteLane}
          onClose={closeDialog}
        />
      )}
    </div>
  );
}
