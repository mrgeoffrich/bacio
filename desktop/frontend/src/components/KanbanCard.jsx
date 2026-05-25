import React, { memo, useEffect, useRef, useState } from 'react';
import * as DropdownMenu from '@radix-ui/react-dropdown-menu';
import Icon from './Icon.jsx';
import Tooltip from './Tooltip.jsx';
import { todoGlyph } from '../lib/todoGlyph.js';
import { waitingStateLabel } from '../lib/waitingLabels.ts';

// stateLabel mirrors api.http.ts's STATE_LABELS — duplicated here so the
// blocked popover can render a blocker's state pill ("In Progress")
// without an extra prop drill. Kept in sync with api.http.ts:STATE_LABELS.
const STATE_LABELS = {
  todo: 'Todo',
  in_progress: 'In Progress',
  needs_action: 'Needs Action',
  in_review: 'In Review',
  done: 'Done',
  cancelled: 'Cancelled',
};
function stateLabel(s) {
  return STATE_LABELS[s] ?? s;
}

function KanbanCard({ card, cardsByKey, promptConfig, isDragging, onDragStart, onDragEnd, onOpen, onDispatch, onCancelWaiting, onOpenQuestion, onOpenIssue, onQuickEval }) {
  // BACI-75: local-only expansion state for the Tasks pill. Resets on
  // unmount (board switch, repo switch, hard refresh) — that's
  // intentional, we don't want to persist a row-level UI toggle.
  const [tasksOpen, setTasksOpen] = useState(false);
  // BACI-131: local-only eval composer state. Reset on unmount /
  // board switch — same justification as tasksOpen.
  const [evalOpen, setEvalOpen] = useState(false);
  const [evalBody, setEvalBody] = useState('');
  const [evalSending, setEvalSending] = useState(false);
  const evalRef = useRef(null);
  useEffect(() => {
    if (evalOpen) evalRef.current?.focus();
  }, [evalOpen]);
  const submitEval = async () => {
    const body = evalBody.trim();
    if (!body || evalSending) return;
    setEvalSending(true);
    try {
      await onQuickEval?.(card.key, body);
      setEvalOpen(false);
      setEvalBody('');
    } catch {
      // App-side error handler surfaces the toast; keep the
      // composer open so the user doesn't lose their typed note.
    } finally {
      setEvalSending(false);
    }
  };
  // The prompts valid to dispatch from this card's current state — the
  // state-gate config is global (App-owned), filtered per-card here.
  const validPrompts = (promptConfig || []).filter(
    p => (p.allowedStates || []).includes(card.column),
  );

  // A taken card is held by an agent — block the human from dragging it
  // or dispatching from it until the claim is released. Opening the
  // read-only drawer stays allowed (viewing isn't a mutation).
  const taken = !!card.taken;
  // BACI-145: card.waitingState is the structured "why is this card
  // waiting?" projection. Non-null when a dispatch is in flight (any
  // of queued / pending / delivered); the label and the cancel-button
  // gate flow off its `kind`. `taken` wins — once an agent claims,
  // waiting_for_claim is cleared in the same txn so the two shouldn't
  // overlap, but render defensively if they do.
  const waitingState = card.waitingState || null;
  const waiting = !!waitingState && !taken;
  // delivered: the worker has taken the Task and cancel-after-delivery
  // is rejected at the store boundary (BACI-130). Keep the spinner
  // glyph (the card is still in flight) but drop the click affordance
  // — the cancel button would either no-op (next tick re-renders
  // without it) or surface a confusing error toast.
  const waitingDelivered = waiting && waitingState.kind === 'delivered';
  const waitingLabel = waiting ? waitingStateLabel(waitingState) : '';

  // BACI-131: the quick-eval composer is the right-edge affordance on
  // a taken card. A taken card disables the zap dropdown anyway, so
  // taking the slot replaces a dead affordance with a live one.
  // BACI-174: also surface the eval composer on cards with at least
  // one attached .jsonl transcript, even after the agent releases its
  // claim — without this gate, eval notes on a since-released card are
  // invisible from the board (the BACI-141 chip is read-only).
  const hasTranscript = (card.transcriptDocCount || 0) > 0;
  const showEvalAffordance = taken || hasTranscript;
  const hasFooter = validPrompts.length > 0 || card.assignees.length > 0 || waiting || showEvalAffordance;

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

  // BACI-114: the per-card blocked indicator. card.blockedBy is the
  // server-filtered open-state list, so a non-empty array means this
  // card has at least one open blocker. The icon-button trigger lives
  // in the .mk-card-top row, click expands a popover listing each
  // blocker (key + state pill + title joined via cardsByKey). The
  // popover is read-only — clicking a row navigates to that issue.
  const blockedBy = card.blockedBy || [];
  const isBlocked = blockedBy.length > 0;

  // BACI-141: combined transcript + eval chip. Visible on every card
  // regardless of `taken` state — the whole point of the ticket is
  // making eval notes / transcripts discoverable AFTER an agent
  // releases its claim (the BACI-131 quick-eval composer only renders
  // while taken, so eval notes on a since-released card would
  // otherwise be invisible from the board). Click delegates to the
  // existing card-open path (the wrapping <article>'s onClick), which
  // lands the user on the issue workspace where the timeline +
  // transcript panels surface the full notes.
  const transcriptDocCount = card.transcriptDocCount || 0;
  const evalCommentCount = card.evalCommentCount || 0;
  const hasEvalChip = transcriptDocCount + evalCommentCount > 0;
  const evalChipLabel = (() => {
    const tParts = transcriptDocCount > 0
      ? `${transcriptDocCount} transcript${transcriptDocCount === 1 ? '' : 's'}`
      : '';
    const eParts = evalCommentCount > 0
      ? `${evalCommentCount} eval note${evalCommentCount === 1 ? '' : 's'}`
      : '';
    const joined = [tParts, eParts].filter(Boolean).join(', ');
    return `${joined} — click to open`;
  })();

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
        {isBlocked && (
          <DropdownMenu.Root>
            <DropdownMenu.Trigger asChild>
              <button
                type="button"
                className="mk-card-blocked-btn"
                aria-label={`Blocked by ${blockedBy.length} issue${blockedBy.length === 1 ? '' : 's'}`}
                title={`Blocked by ${blockedBy.length} issue${blockedBy.length === 1 ? '' : 's'}`}
                onClick={(e) => e.stopPropagation()}
              >
                <Icon name="lock" />
              </button>
            </DropdownMenu.Trigger>
            <DropdownMenu.Portal>
              <DropdownMenu.Content
                className="mk-card-blocked-menu"
                align="end"
                side="bottom"
                sideOffset={4}
                collisionPadding={8}
                onClick={(e) => e.stopPropagation()}
              >
                <div className="mk-card-blocked-menu-label">Blocked by</div>
                {blockedBy.map(b => {
                  const other = cardsByKey?.get(b.key);
                  return (
                    <DropdownMenu.Item
                      key={b.key}
                      className="mk-card-blocked-item"
                      onSelect={() => onOpenIssue?.(b.key)}
                    >
                      <span className="mk-card-id">{b.key}</span>
                      <span className={`mk-pill mk-status-${b.state}`}>{stateLabel(b.state)}</span>
                      {other && <span className="mk-card-blocked-title">{other.title}</span>}
                    </DropdownMenu.Item>
                  );
                })}
              </DropdownMenu.Content>
            </DropdownMenu.Portal>
          </DropdownMenu.Root>
        )}
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
            // BACI-145: the spinner + the inline label render together
            // so the user can read "why is this card waiting?" at a
            // glance. waitingDelivered drops the cancel affordance —
            // BACI-130 store rejects cancel-after-delivery. The label
            // is the affordance now, so the tooltip wrapping is gone
            // (the cancel button itself still carries aria-label).
            // BACI-173: label leads, spinner trails — the spinner is
            // the live thing and reads more naturally at the right
            // edge of the card; the label hangs off its left so the
            // eye lands on the spinner then reads back to the reason.
            <span className="mk-card-waiting">
              {waitingLabel && (
                <span className="mk-card-spinner-label">{waitingLabel}</span>
              )}
              {waitingDelivered ? (
                <span
                  className="mk-card-spinner"
                  role="status"
                  aria-label="Dispatch delivered to worker"
                />
              ) : (
                <button
                  type="button"
                  className="mk-card-spinner mk-card-spinner-btn"
                  aria-label="Cancel queued dispatch"
                  title="Cancel queued dispatch"
                  onClick={(e) => {
                    e.stopPropagation();
                    if (onCancelWaiting) onCancelWaiting(card.key);
                  }}
                />
              )}
            </span>
          ) : (
            // BACI-131 introduced the eval button as a taken-only
            // replacement for the disabled zap dropdown.
            // BACI-174 extends it: any card with attached .jsonl
            // transcripts also gets the eval composer, even after the
            // agent releases — otherwise eval notes on a since-released
            // card are invisible from the board.
            //
            // Precedence: on a taken card the zap is disabled, so we
            // render the eval button alone (unchanged from BACI-131).
            // On a non-taken card with transcripts, render eval AND
            // zap side-by-side (option b in the brief) — having an
            // historical transcript doesn't preclude dispatching a new
            // prompt (e.g. fix-review). When neither condition holds,
            // fall back to the zap-only menu.
            <>
              {showEvalAffordance && (
                <Tooltip label="Add a quick eval note">
                  <button
                    type="button"
                    className="mk-card-eval-btn"
                    aria-label="Add a quick eval comment"
                    onClick={(e) => {
                      e.stopPropagation();
                      setEvalOpen(true);
                    }}
                  >
                    <Icon name="comment" />
                  </button>
                </Tooltip>
              )}
              {!taken && validPrompts.length > 0 && (
                <DropdownMenu.Root>
                  <DropdownMenu.Trigger asChild>
                    <button
                      className="mk-card-action-btn"
                      aria-label="Dispatch a prompt"
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
            </>
          )}
        </footer>
      )}
      {evalOpen && showEvalAffordance && (
        // BACI-131 quick-eval inline composer. Cmd/Ctrl+Enter submits,
        // Escape cancels. Wrapper stops propagation so clicks inside
        // the composer don't open the drawer.
        // BACI-174: the same composer is now reachable on cards with
        // attached transcripts (see showEvalAffordance derivation).
        <div className="mk-card-eval-composer" onClick={(e) => e.stopPropagation()}>
          <textarea
            ref={evalRef}
            className="mk-card-eval-textarea"
            rows={3}
            placeholder="Quick eval note…"
            value={evalBody}
            disabled={evalSending}
            onChange={(e) => setEvalBody(e.target.value)}
            onKeyDown={(e) => {
              if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
                e.preventDefault();
                submitEval();
              }
              if (e.key === 'Escape') {
                e.preventDefault();
                setEvalOpen(false);
                setEvalBody('');
              }
            }}
          />
          <div className="mk-card-eval-actions">
            <button
              type="button"
              className="mk-btn-ghost"
              disabled={evalSending}
              onClick={(e) => {
                e.stopPropagation();
                setEvalOpen(false);
                setEvalBody('');
              }}
            >Cancel</button>
            <button
              type="button"
              className="mk-btn-primary"
              disabled={!evalBody.trim() || evalSending}
              onClick={(e) => {
                e.stopPropagation();
                submitEval();
              }}
            >{evalSending ? 'Saving…' : 'Save'}</button>
          </div>
        </div>
      )}
      {(hasMeta || hasEvalChip) && (
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
            {hasEvalChip && (activeVerb || todosTotal > 0) && <span className="mk-card-meta-sep">·</span>}
            {hasEvalChip && (
              // BACI-141: combined transcript + eval chip. Clicking
              // bubbles to the card's onClick (open the workspace) —
              // no per-chip handler needed.
              <Tooltip label={evalChipLabel}>
                <span
                  className="mk-card-eval-chip"
                  aria-label={evalChipLabel}
                >
                  <Icon name="comment" />
                  {transcriptDocCount > 0 && evalCommentCount > 0
                    ? `${transcriptDocCount}/${evalCommentCount}`
                    : `${transcriptDocCount + evalCommentCount}`}
                </span>
              </Tooltip>
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
