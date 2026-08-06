import { memo } from 'react';
import * as DropdownMenu from '@radix-ui/react-dropdown-menu';
import { MoreHorizontal } from 'lucide-react';
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
  // Take the card off the Kanban entirely (the seam's
  // `moveIssueToKanbanColumn(prefix, key, '', null)`). The inverse of the
  // lane header's "+", and the only way back off a git repo's board —
  // dragging can move a card between lanes but never out of all of them.
  onTakeOffBoard?: (key: string) => void;
};

function KanbanCard({
  card,
  compact,
  isDragging,
  onOpen,
  onOpenIssue,
  onDragStart,
  onDragEnd,
  onTakeOffBoard,
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
        {/* The card-level menu, in the slot the pre-pivot dispatch button
            used (and wearing its orphaned .mk-card-action-* CSS, which
            already carries the margin-left: auto that pins it right).
            draggable={false} so grabbing the button doesn't start the
            card's own move-drag, and stopPropagation so opening the menu
            doesn't trip the card's onClick → open-issue. */}
        {onTakeOffBoard && (
          <DropdownMenu.Root>
            <DropdownMenu.Trigger asChild>
              <button
                type="button"
                className="mk-card-action-btn"
                draggable={false}
                aria-label={`Actions for ${card.key}`}
                title={`Actions for ${card.key}`}
                onClick={(e) => e.stopPropagation()}
              >
                <MoreHorizontal size={13} strokeWidth={2} aria-hidden="true" />
              </button>
            </DropdownMenu.Trigger>
            <DropdownMenu.Portal>
              <DropdownMenu.Content
                className="mk-card-action-menu"
                align="end"
                side="bottom"
                sideOffset={4}
                collisionPadding={8}
                onClick={(e) => e.stopPropagation()}
              >
                <DropdownMenu.Item
                  className="mk-card-action-item"
                  onSelect={() => onTakeOffBoard(card.key)}
                >
                  Take off board
                </DropdownMenu.Item>
              </DropdownMenu.Content>
            </DropdownMenu.Portal>
          </DropdownMenu.Root>
        )}
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
