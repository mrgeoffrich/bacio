import React, { memo } from 'react';
import * as DropdownMenu from '@radix-ui/react-dropdown-menu';
import Icon from './Icon.jsx';
import Tooltip from './Tooltip.jsx';

function KanbanCard({ card, promptConfig, isDragging, onDragStart, onDragEnd, onOpen, onDispatch, onCancelWaiting }) {
  // The prompts valid to dispatch from this card's current state — the
  // state-gate config is global (App-owned), filtered per-card here.
  const validPrompts = (promptConfig || []).filter(
    p => (p.allowedStates || []).includes(card.column),
  );

  // A taken card is held by an agent — block the human from dragging it
  // or dispatching from it until the claim is released. Opening the
  // read-only drawer stays allowed (viewing isn't a mutation).
  const taken = !!card.taken;
  // A waiting card has a dispatch queued but no agent claim yet — the
  // gap this feature closes. Show a spinner, block drag/dispatch.
  // `taken` wins: once an agent claims, waiting_for_claim is cleared, so
  // they shouldn't overlap, but render defensively if they do.
  const waiting = !!card.waitingForClaim && !taken;
  const dispatchDisabled = taken || waiting;

  const hasFooter = validPrompts.length > 0 || card.assignees.length > 0 || waiting;

  return (
    <article
      className={`mk-card ${isDragging ? 'is-dragging' : ''} ${card.claude ? 'is-claude' : ''} ${taken ? 'is-taken' : ''} ${waiting ? 'is-waiting' : ''}`}
      draggable={!taken && !waiting}
      onDragStart={onDragStart}
      onDragEnd={onDragEnd}
      onClick={onOpen}
    >
      <div className="mk-card-top">
        <span className="mk-card-id">{card.key}</span>
      </div>
      <h3 className="mk-card-title">{card.title}</h3>
      {card.tags && card.tags.length > 0 && (
        <div className="mk-tag-row">
          {card.tags.map(t => <span key={t} className="mk-tag">{t}</span>)}
        </div>
      )}
      {hasFooter && (
        <footer className="mk-card-foot">
          {card.assignees.length > 0 && (
            <Tooltip label={card.assignees.join(', ')}>
              <span className={`mk-card-assignee ${card.claude ? 'is-claude' : ''}`}>
                {card.assignees.join(', ')}
              </span>
            </Tooltip>
          )}
          {waiting ? (
            <Tooltip label="Cancel queued dispatch">
              <button
                type="button"
                className="mk-card-spinner mk-card-spinner-btn"
                aria-label="Cancel queued dispatch"
                onClick={(e) => {
                  e.stopPropagation();
                  if (onCancelWaiting) onCancelWaiting(card.key);
                }}
              />
            </Tooltip>
          ) : validPrompts.length > 0 && (
            <DropdownMenu.Root>
              <DropdownMenu.Trigger asChild>
                <button
                  className="mk-card-action-btn"
                  aria-label={taken ? 'An agent is working on this issue' : 'Dispatch a prompt'}
                  disabled={dispatchDisabled}
                  title={taken ? 'An agent is working on this issue' : undefined}
                  onClick={(e) => e.stopPropagation()}
                >
                  <Icon name="zap" />
                </button>
              </DropdownMenu.Trigger>
              <DropdownMenu.Portal>
                <DropdownMenu.Content
                  className="mk-card-action-menu"
                  align="end"
                  side="top"
                  sideOffset={4}
                  collisionPadding={8}
                >
                  {validPrompts.map(p => (
                    <DropdownMenu.Item
                      key={p.mode}
                      className="mk-card-action-item"
                      onSelect={() => onDispatch(card.key, p.mode)}
                    >
                      {p.label}
                    </DropdownMenu.Item>
                  ))}
                </DropdownMenu.Content>
              </DropdownMenu.Portal>
            </DropdownMenu.Root>
          )}
        </footer>
      )}
    </article>
  );
}

// Memo skips the re-render on the common case where one card mutates
// (drag, dispatch): App's setCards updater returns the same object ref
// for every unchanged card, callback props are useCallback'd, so shallow
// compare passes for the others. (Doesn't help the poll path — that
// rebuilds the array from the server response, fresh refs all round.)
export default memo(KanbanCard);
