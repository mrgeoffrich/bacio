import { memo } from 'react';
import BlockedByBadge from '../pipeline/BlockedByBadge';
import { cardPropsEqual } from '../pipeline/memoCard';
import type { BoardCard } from '../../api';

// KanbanCard — the card on the human Kanban board.
//
// Deliberately slim. This is NOT a port of the pre-pivot kanban card: that
// one carried the agent-orchestration surface (dispatch menu, follow-on
// dropdown, quick-eval textarea, waiting spinner, transcript chip, question
// pill), and every REST route and CLI verb behind those was deleted with the
// agentic board. The Kanban is the *human work* axis — the Agentic Pipeline
// remains the place agents are driven from — so the card shows only what a
// person needs to recognise a piece of work at a glance:
//
//   feature emoji · blocked-by lock · issue key · title · tags · assignees
//
// Everything else is one click away in the issue workspace.
//
// The lane a card sits in rides on the CONTAINER (KanbanColumn.cards), not on
// the card, so there is no lane field to render here and no lane prop to
// thread — the board resolves membership before it ever reaches this
// component.
type KanbanCardProps = {
  card: BoardCard;
  // compact renders the dense variant: tags and the footer are dropped and
  // .mk-card.is-compact tightens the padding / title size.
  compact?: boolean;
  isDragging?: boolean;
  // These take the card / key rather than closing over it so the board can
  // pass ONE stable handler identity per callback — required for the
  // React.memo below to skip the 10s poll re-render. See ../pipeline/memoCard.
  onOpen?: (card: BoardCard) => void;
  onOpenIssue?: (key: string) => void;
  onDragStart?: (key: string) => void;
  onDragEnd?: () => void;
};

function KanbanCard({
  card,
  compact,
  isDragging,
  onOpen,
  onOpenIssue,
  onDragStart,
  onDragEnd,
}: KanbanCardProps) {
  // A card an agent currently holds, or one with a dispatch queued against
  // it, is not the user's to shuffle — the Pipeline owns that lifecycle. It
  // still opens and still renders; only the drag is withheld, matching the
  // pre-pivot board's `draggable={!taken && !waiting}` rule.
  const waiting = !!card.waitingState && !card.taken;
  const draggable = !card.taken && !waiting;

  return (
    <article
      className={[
        'mk-card',
        isDragging ? 'is-dragging' : '',
        compact ? 'is-compact' : '',
        card.archived ? 'is-archived' : '',
        card.taken ? 'is-taken' : '',
        waiting ? 'is-waiting' : '',
      ].filter(Boolean).join(' ')}
      draggable={draggable}
      onDragStart={() => onDragStart?.(card.key)}
      onDragEnd={onDragEnd}
      onClick={() => onOpen?.(card)}
    >
      <div className="mk-card-top">
        {card.featureEmoji && (
          <span className="mk-card-feature-emoji" aria-hidden="true">{card.featureEmoji}</span>
        )}
        {/* The blocked-by lock is the one relation the board surfaces
            inline — a blocked card is a card you shouldn't pick up. Reuses
            the Pipeline badge verbatim (and with it the deliberately-shared
            .mk-card-blocked-menu* popover CSS). canBlock is omitted: the
            drag-to-block gesture belongs to the Pipeline, so an unblocked
            card renders no chip at all here. */}
        <BlockedByBadge
          blockedBy={card.blockedBy}
          sourceKey={card.key}
          onOpenIssue={onOpenIssue}
        />
        <span className="mk-card-id">{card.key}</span>
      </div>

      <h4 className="mk-card-title">{card.title}</h4>

      {!compact && card.tags.length > 0 && (
        <div className="mk-tag-row">
          {card.tags.map(tag => <span key={tag} className="mk-tag">{tag}</span>)}
        </div>
      )}

      {!compact && card.assignees.length > 0 && (
        <div className="mk-card-foot">
          <div className="mk-card-meta">
            {card.assignees.map(name => (
              <span
                key={name}
                className={`mk-card-assignee${card.claude ? ' is-claude' : ''}`}
                title={name}
              >
                {name}
              </span>
            ))}
          </div>
        </div>
      )}
    </article>
  );
}

// React.memo'd on the same comparator as the Pipeline's hot cards: the board
// re-polls `cards` every 10s, so `card` is a fresh object every tick and a
// referential memo would never skip. cardPropsEqual compares `card` by value
// and every other prop by identity — which holds only because the board
// passes stable callbacks (see KanbanBoard).
export default memo(KanbanCard, cardPropsEqual);
