import React, { memo, useState } from 'react';
import * as DropdownMenu from '@radix-ui/react-dropdown-menu';
import Icon from './Icon.jsx';
import Tooltip from './Tooltip.jsx';
import { todoGlyph } from '../lib/todoGlyph.js';

function KanbanCard({ card, promptConfig, isDragging, onDragStart, onDragEnd, onOpen, onDispatch, onCancelWaiting, onOpenQuestion }) {
  // BACI-75: local-only expansion state for the Tasks pill. Resets on
  // unmount (board switch, repo switch, hard refresh) — that's
  // intentional, we don't want to persist a row-level UI toggle.
  const [tasksOpen, setTasksOpen] = useState(false);
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

  // BACI-60 meta line — only on taken cards, only when at least one of
  // verb or tasks is populated. Hidden entirely otherwise so cards that
  // aren't being worked on stay visually quiet.
  const activeVerb = taken ? (card.activeVerb || '') : '';
  const todosTotal = taken ? (card.todosTotal || 0) : 0;
  const todosDone = taken ? (card.todosDone || 0) : 0;
  const hasMeta = !!activeVerb || todosTotal > 0;

  // BACI-53 open ask_user_question rows for this issue. The first
  // one drives the pill copy (header is the agent's ≤12-char tag);
  // clicking the pill auto-pops the modal for that row id.
  const openQuestions = card.openQuestions || [];
  const firstQuestion = openQuestions[0];

  return (
    <article
      className={`mk-card ${isDragging ? 'is-dragging' : ''} ${card.claude ? 'is-claude' : ''} ${taken ? 'is-taken' : ''} ${waiting ? 'is-waiting' : ''} ${card.archived ? 'is-archived' : ''}`}
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
      {firstQuestion && (
        <Tooltip label={firstQuestion.firstQuestion || 'User input needed'}>
          <button
            type="button"
            className="mk-card-question-pill"
            aria-label="Answer agent question"
            onClick={(e) => {
              e.stopPropagation();
              if (onOpenQuestion) onOpenQuestion(firstQuestion.id);
            }}
          >
            <span className="mk-card-question-pill-tag">
              ? {openQuestions.length > 1 ? `${openQuestions.length}` : ''}
              {firstQuestion.header ? ` ${firstQuestion.header}` : ''}
            </span>
            <span className="mk-card-question-pill-text">
              {firstQuestion.firstQuestion || 'Answer'}
            </span>
          </button>
        </Tooltip>
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
                      onClick={(e) => e.stopPropagation()}
                    >
                      {/*
                        BACI-67: render the imperative actionLabel
                        ("Plan", "Design") so the dispatch button
                        reads as a call to action. label (gerund —
                        "Planning") is the fallback for templates
                        that haven't set the override and aren't
                        built-in (no derivation yet client-side; the
                        store seed handles built-ins).
                      */}
                      {p.actionLabel || p.label}
                    </DropdownMenu.Item>
                  ))}
                </DropdownMenu.Content>
              </DropdownMenu.Portal>
            </DropdownMenu.Root>
          )}
        </footer>
      )}
      {hasMeta && (
        <>
          <div className="mk-card-meta-line">
            {activeVerb && <span className="mk-card-verb">{activeVerb}</span>}
            {activeVerb && todosTotal > 0 && <span className="mk-card-meta-sep">·</span>}
            {todosTotal > 0 && (
              <button
                type="button"
                className="mk-card-tasks mk-card-tasks-btn"
                aria-expanded={tasksOpen}
                aria-controls={`card-todos-${card.key}`}
                onClick={(e) => {
                  e.stopPropagation();
                  if ((card.todos || []).length) setTasksOpen(o => !o);
                }}
              >
                Tasks {todosDone}/{todosTotal}
              </button>
            )}
          </div>
          {tasksOpen && (card.todos || []).length > 0 && (
            <ul
              id={`card-todos-${card.key}`}
              className="mk-card-todos-list"
              onClick={(e) => e.stopPropagation()}
            >
              {card.todos.map((t, i) => (
                <li
                  key={i}
                  className={`mk-card-todo mk-card-todo--${t.status}`}
                >
                  <span className="mk-card-todo-glyph" aria-hidden>
                    {todoGlyph(t.status)}
                  </span>
                  <span className="mk-card-todo-text">{t.content}</span>
                </li>
              ))}
            </ul>
          )}
        </>
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
