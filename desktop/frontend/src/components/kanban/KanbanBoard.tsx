import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import type { UIEvent } from 'react';
import KanbanLane from './KanbanLane';
import { useCollapsedColumns } from './boardCollapsePersistence';
import { useCompactColumns } from './boardCompactPersistence';
import { persistKanbanScroll, readKanbanScroll } from './kanbanPersistence';
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
// The drag-and-drop is the pre-pivot board's hand-rolled HTML5 implementation
// (dragstart on the card, dragover/drop on the lane) — deliberately no DnD
// library. The drop writes through `useOptimisticMutation` so the card lands
// under the cursor immediately and reverts if the server refuses.

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

  // Per-lane UI preferences, persisted per repo and keyed on lane uuid.
  const collapsed = useCollapsedColumns(activeBoard);
  const compact = useCompactColumns(activeBoard);

  // Latest-value ref so the drop handler can read the current lanes without
  // listing `columns` as a dependency — the handler is threaded down to the
  // memo'd cards, so a fresh identity every poll would defeat the memo.
  const columnsRef = useRef(columns);
  useEffect(() => {
    columnsRef.current = columns;
  });

  const cardsByKey = useMemo(
    () => new Map(cards.map((card): [string, BoardCard] => [card.key, card])),
    [cards],
  );
  const lanes = useMemo(
    () => columns.map(column => ({ column, cards: resolveLaneCards(column, cardsByKey) })),
    [columns, cardsByKey],
  );
  const boardIsEmpty = lanes.every(lane => lane.cards.length === 0);

  // ─── Drag & drop ───────────────────────────────────────────────────

  const onCardDragStart = useCallback((key: string) => setDragKey(key), []);
  const onCardDragEnd = useCallback(() => {
    setDragKey(null);
    setOverLane(null);
  }, []);
  const onDragOverLane = useCallback((uuid: string) => setOverLane(uuid), []);
  const onDragLeaveLane = useCallback(() => setOverLane(null), []);

  // The drop: move the dragged card into this lane, appending at the bottom.
  // The no-op guard reads the live lanes off the ref BEFORE the optimistic
  // update so it can abort synchronously (a drop back onto the source lane
  // costs no round trip), and the pre-drop snapshot doubles as the rollback.
  const onDropOnLane = useCallback((uuid: string) => {
    const key = dragKey;
    setDragKey(null);
    setOverLane(null);
    if (!key) return;
    const before = columnsRef.current;
    if (laneOf(before, key)?.uuid === uuid) return;
    // A drop onto a collapsed lane expands it, so the user sees the card
    // land instead of it vanishing behind the rotated title.
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
  }, [activeBoard, dragKey, collapsed, mutate, setColumns]);

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
  if (columns.length === 0) {
    return (
      <div className="mk-app-state">
        No lanes on this Kanban yet — add one with <code>bacio kanban column add &lt;NAME&gt;</code>.
      </div>
    );
  }

  return (
    <div className="mk-kanban">
      <div className="mk-board" ref={boardRef} onScroll={onBoardScroll}>
        {lanes.map(({ column, cards: laneCards }) => (
          <KanbanLane
            key={column.uuid}
            column={column}
            cards={laneCards}
            isOver={overLane === column.uuid}
            isCollapsed={collapsed.set.has(column.uuid)}
            isCompact={compact.set.has(column.uuid)}
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
          />
        ))}
      </div>
      {boardIsEmpty && (
        <div className="mk-kanban-hint">
          Nothing on this Kanban yet. Put a card on a lane with{' '}
          <code>bacio kanban move &lt;KEY&gt; --column &lt;LANE&gt;</code>.
        </div>
      )}
    </div>
  );
}
