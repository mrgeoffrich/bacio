import { AnimatePresence, m } from 'motion/react';
import Icon from '../Icon';
import KanbanCard from './KanbanCard';
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
  isOver: boolean;
  isCollapsed: boolean;
  isCompact: boolean;
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
};

export default function KanbanLane({
  column,
  cards,
  isOver,
  isCollapsed,
  isCompact,
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
            {cards.length === 0 && (
              <div className="mk-col-empty">Drag a card here</div>
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
