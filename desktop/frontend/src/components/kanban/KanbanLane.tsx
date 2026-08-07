import { AnimatePresence, m } from 'motion/react';
import Icon from '../Icon';
import AddCardsMenu from './AddCardsMenu';
import KanbanCard from './KanbanCard';
import LaneMenu from './LaneMenu';
import type { BoardCard, KanbanColumn } from '../../api';

// KanbanLane — one lane of the Kanban board: its header (name pill, count,
// compact + collapse toggles), its card list, and the drop target that wraps
// both. Ported from the pre-pivot Board.jsx's inline column, with the lane
// identity moved from `model.State` onto the lane's own uuid.
//
// The drag-and-drop is the same hand-rolled HTML5 implementation the board
// has always used — dragstart on the card, dragover/dragleave/drop on the
// lane. No DnD library: the gesture is one card into one lane, the browser
// gives us all of it for free, and a library would be more code than the
// thing it replaces.
//
// All the callbacks take the lane uuid (rather than being pre-bound per lane)
// so the board can hand every lane ONE stable identity each, which is what
// keeps the memo'd cards below from re-rendering on the 10s poll.
type KanbanLaneProps = {
  column: KanbanColumn;
  // cards is this lane's membership already resolved against the polled card
  // list, in lane order. Refs whose issue isn't in the card list (archived and
  // hidden, filtered) are dropped by the board before they get here.
  cards: BoardCard[];
  // Every card in the repo that no lane holds — the "+" picker's
  // candidates. Derived once by the board and shared by reference across
  // every lane, so it costs one pass over the board, not one per lane.
  offBoardCards: BoardCard[];
  isOver: boolean;
  isCollapsed: boolean;
  isCompact: boolean;
  // Whether a left / right nudge would land anywhere. False at the ends
  // of the board, where LaneMenu omits the item rather than greying it.
  canMoveLeft: boolean;
  canMoveRight: boolean;
  draggingKey: string | null;
  onDragOverLane: (uuid: string) => void;
  onDragLeaveLane: () => void;
  onDropOnLane: (uuid: string) => void;
  onCollapse: (uuid: string) => void;
  onExpand: (uuid: string) => void;
  onCompact: (uuid: string) => void;
  onUncompact: (uuid: string) => void;
  onOpenCard: (card: BoardCard) => void;
  onOpenIssue: (key: string) => void;
  onCardDragStart: (key: string) => void;
  onCardDragEnd: () => void;
  // Lane-level CRUD. Same shape as the drag callbacks — they take the
  // lane uuid rather than being pre-bound per lane, so the board hands
  // every lane ONE stable identity each.
  onAddCard: (uuid: string, key: string) => void;
  // Open the lane-scoped composer. Same shape as the callbacks above —
  // takes the lane uuid rather than being pre-bound, so the board hands
  // every lane ONE stable identity each and the memo'd cards below don't
  // re-render on the 10s poll.
  onCreateCard: (uuid: string) => void;
  onTakeCardOffBoard: (key: string) => void;
  onRenameLane: (uuid: string) => void;
  onMoveLane: (uuid: string, delta: number) => void;
  onDeleteLane: (uuid: string) => void;
};

export default function KanbanLane({
  column,
  cards,
  offBoardCards,
  isOver,
  isCollapsed,
  isCompact,
  canMoveLeft,
  canMoveRight,
  draggingKey,
  onDragOverLane,
  onDragLeaveLane,
  onDropOnLane,
  onCollapse,
  onExpand,
  onCompact,
  onUncompact,
  onOpenCard,
  onOpenIssue,
  onCardDragStart,
  onCardDragEnd,
  onAddCard,
  onCreateCard,
  onTakeCardOffBoard,
  onRenameLane,
  onMoveLane,
  onDeleteLane,
}: KanbanLaneProps) {
  return (
    <div
      className={`mk-col${isCollapsed ? ' is-collapsed' : ''}${isOver ? ' is-over' : ''}`}
      onDragOver={(e) => { e.preventDefault(); onDragOverLane(column.uuid); }}
      onDragLeave={onDragLeaveLane}
      onDrop={(e) => { e.preventDefault(); onDropOnLane(column.uuid); }}
    >
      {isCollapsed ? (
        <>
          {/* The expand affordance sits above the rotated title so clicking
              the chevron doesn't fight the title text for the cursor. */}
          <button
            type="button"
            className="mk-col-expand-btn"
            aria-label={`Expand ${column.name} lane`}
            onClick={() => onExpand(column.uuid)}
          >
            <Icon name="maximize-2" />
          </button>
          <div className="mk-col-collapsed-title">{column.name}</div>
        </>
      ) : (
        <>
          <header className="mk-col-head">
            <span className="mk-col-pill is-lane">{column.name}</span>
            <span className="mk-col-count">{cards.length}</span>
            {/* The two Phase 7B CRUD affordances, deliberately always
                visible rather than hover-revealed: the gap this closes is
                "there is no in-app way to do this", so hiding the way
                until the pointer happens to be over the header would
                leave most of it unclosed. */}
            <AddCardsMenu
              laneName={column.name}
              candidates={offBoardCards}
              onAdd={(key) => onAddCard(column.uuid, key)}
              onCreate={() => onCreateCard(column.uuid)}
            />
            <LaneMenu
              laneName={column.name}
              canMoveLeft={canMoveLeft}
              canMoveRight={canMoveRight}
              onRename={() => onRenameLane(column.uuid)}
              onMoveLeft={() => onMoveLane(column.uuid, -1)}
              onMoveRight={() => onMoveLane(column.uuid, 1)}
              onDelete={() => onDeleteLane(column.uuid)}
            />
            {/* Compact-cards toggle, between the count and the collapse
                chevron. The glyph swaps with state: converging chevrons
                when expanded read as "squeeze together", diverging ones
                when already compact read as "let breathe". */}
            <button
              type="button"
              className={`mk-col-compact-btn${isCompact ? ' is-active' : ''}`}
              aria-label={isCompact ? `Expand cards in ${column.name} lane` : `Compact cards in ${column.name} lane`}
              aria-pressed={isCompact}
              onClick={() => (isCompact ? onUncompact(column.uuid) : onCompact(column.uuid))}
            >
              <Icon name={isCompact ? 'chevrons-up-down' : 'chevrons-down-up'} />
            </button>
            <button
              type="button"
              className="mk-col-collapse-btn"
              aria-label={`Collapse ${column.name} lane`}
              onClick={() => onCollapse(column.uuid)}
            >
              <Icon name="minimize-2" />
            </button>
          </header>
          <div className="mk-col-body">
            {/* The old copy — "Drag a card here, or add one with +" — was
                false in both halves on a fresh workspace: there is nothing
                to drag, and the `+` only PLACED cards that already
                existed. Now that it creates too, the empty lane gets the
                folder-page treatment: a statement of fact, a one-line
                explanation, and a primary button. */}
            {cards.length === 0 && (
              <div className="mk-col-empty mk-empty-cta">
                <span className="mk-empty-cta-title">Nothing in this lane yet.</span>
                <span className="mk-empty-cta-sub">Drag a card in, or start a new one.</span>
                <button
                  type="button"
                  className="mk-btn-primary"
                  onClick={() => onCreateCard(column.uuid)}
                >
                  New issue
                </button>
              </div>
            )}
            {/* AnimatePresence so a card leaving the lane (dragged away, or
                taken off the board by another window's poll) plays its exit
                before unmount. `mode="popLayout"` keeps the siblings FLIPing
                while the exit runs — "wait" would freeze the lane on removal,
                which fights the cross-lane layout move. `initial={false}`
                skips the entry animation on first mount so a freshly-loaded
                board doesn't pop in. */}
            <AnimatePresence mode="popLayout" initial={false}>
              {cards.map(card => (
                <m.div
                  key={card.key}
                  layout
                  initial={{ opacity: 0 }}
                  animate={{ opacity: 1 }}
                  exit={{ opacity: 0 }}
                  transition={{ duration: 0.2 }}
                >
                  <KanbanCard
                    card={card}
                    compact={isCompact}
                    isDragging={draggingKey === card.key}
                    onOpen={onOpenCard}
                    onOpenIssue={onOpenIssue}
                    onDragStart={onCardDragStart}
                    onDragEnd={onCardDragEnd}
                    onTakeOffBoard={onTakeCardOffBoard}
                  />
                </m.div>
              ))}
            </AnimatePresence>
          </div>
        </>
      )}
    </div>
  );
}
