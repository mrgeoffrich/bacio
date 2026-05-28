import React, { memo, forwardRef, useEffect, useRef, useState } from 'react';
import { Link } from 'react-router';
import * as DropdownMenu from '@radix-ui/react-dropdown-menu';
import { m } from 'motion/react';
import Icon from './Icon.jsx';
import Tooltip from './Tooltip.jsx';
import DispatchMenuContent from './DispatchMenuContent.jsx';
import { renderZapMenuRows } from './dispatchMenuRows.jsx';
import { todoGlyph } from '../lib/todoGlyph.jsx';
import { waitingStateLabel } from '../lib/waitingLabels.ts';
import { documentPath } from '../lib/routes';
import prLabel from '../lib/prLabel';
import { shortBranchLabel } from '../lib/branchName';

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

function KanbanCard({ card, cardsByKey, promptConfig, isDragging, compact, onDragStart, onDragEnd, onOpen, onDispatch, onDispatchChain, onCancelWaiting, onOpenQuestion, onOpenIssue, onQuickEval, onSetFollowOn, onCancelFollowOn, isTrayHover, isJumping, layoutEase = [0.2, 0, 0, 1] }, ref) {
  // BACI-75: local-only expansion state for the Tasks pill. Resets on
  // unmount (board switch, repo switch, hard refresh) — that's
  // intentional, we don't want to persist a row-level UI toggle.
  const [tasksOpen, setTasksOpen] = useState(false);
  // BACI-193: while Motion is mid-FLIP, `var(--radius-lg)` and
  // `var(--shadow-1)` distort because the layout animation interpolates
  // a snapshot of the resolved CSS — research called this out. Inline
  // concrete values for the duration of the animation, drop back to
  // the CSS variables (which keep theme-correct values) when at rest.
  const [animating, setAnimating] = useState(false);
  // BACI-131: local-only eval composer state. Reset on unmount /
  // board switch — same justification as tasksOpen.
  const [evalOpen, setEvalOpen] = useState(false);
  const [evalBody, setEvalBody] = useState('');
  const [evalSending, setEvalSending] = useState(false);
  const evalRef = useRef(null);
  useEffect(() => {
    if (evalOpen) evalRef.current?.focus();
  }, [evalOpen]);
  // BACI-191: when compact mode turns on, force-close an open eval
  // composer so it doesn't re-appear (with half-typed text) when the
  // user turns compact mode off. The composer is part of the eval
  // surface that compact mode hides, so letting it survive is
  // confusing — the trade-off (losing the typed text) is acceptable
  // because the user toggled the whole column, not just this card.
  useEffect(() => {
    if (compact && evalOpen) {
      setEvalOpen(false);
      setEvalBody('');
    }
  }, [compact, evalOpen]);
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
  // BACI-252: the zap and follow-on popups no longer filter on issue
  // state — every configured non-reserved template is offered in a
  // single flat list. Reserved/internal slugs (the `_dispatch_preamble`
  // body-only wrapper) are filtered by leading-underscore prefix; the
  // convention is documented in model.BuiltinTemplatePreamble.
  const nonReservedPrompts = (promptConfig || []).filter(
    p => !(p.mode || '').startsWith('_'),
  );

  // A taken card is held by an agent — block the human from dragging it
  // or dispatching from it until the claim is released. Opening the
  // read-only drawer stays allowed (viewing isn't a mutation).
  const taken = !!card.taken;
  // BACI-145: card.waitingState is the structured "why is this card
  // waiting?" projection. Non-null when a dispatch is in flight (any
  // of queued / pending / delivered); the label and the cancel-button
  // gate flow off its `kind`. `taken` wins — once an agent claims,
  // the claim row pre-empts the waiting render. BACI-255: the
  // server's waitingState IS the signal now (no denormalised boolean
  // riding alongside), but render defensively if `taken` and
  // `waiting` somehow overlap.
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
  // BACI-192: follow-on dispatch is only meaningful while a parent
  // dispatch is in flight on the same issue (the BACI-180 backend
  // resolves the parent via WaitingDispatchForIssue and rejects when
  // there is none). Gate the button visually on either a taken or
  // waiting card; the dropdown stays closed on idle cards. The
  // follow-on shape comes from the server-side denorm onto BoardCard
  // (BACI-192) — undefined on cards without a dormant follow-on row.
  const followOn = card.followOn || null;
  // BACI-209: a pre-queued follow-on (attached by the compound
  // dispatch-chain affordance below) lights up the chip on a still-todo
  // card. Without this relaxation the chip would stay hidden until the
  // primary's matcher binds the parent — exactly the visibility we
  // wanted the compound action to enable.
  // BACI-217: a blocked-and-idle card (at least one open `blocks` edge
  // pointing at it) is also eligible — the user can queue a follow-on
  // that waits for every blocker to clear before firing. card.blockedBy
  // is the open-state list (server-filtered) so length > 0 is the
  // server's "this card is currently blocked" signal.
  const blockedForFollowOn = (card.blockedBy?.length ?? 0) > 0;
  const followOnEligible = taken || waiting || !!followOn || blockedForFollowOn;
  // showFollowOn also needs the footer to exist on taken / waiting
  // cards — those normally already render the footer (assignee +
  // spinner), but if the assignee slot is empty the showFollowOn
  // condition keeps the footer alive so the button has a home.
  const showFollowOn = followOnEligible && !!onSetFollowOn;
  const hasFooter = nonReservedPrompts.length > 0 || card.assignees.length > 0 || waiting || showEvalAffordance || showFollowOn;

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

  // BACI-216: per-card "open the latest plan" affordance. card.latestPlan
  // is populated by the boardcards assembler from the bulk store
  // helper; null / undefined when no plan-typed doc is linked to the
  // issue. The icon-button trigger sits in the .mk-card-top row next to
  // the issue key (between the issue id and any blocked-icon popover);
  // clicking it navigates to the doc viewer route (covered by BACI-215)
  // and stops propagation so the card-level onOpen doesn't also fire.
  const latestPlan = card.latestPlan || null;
  // BACI-239: per-card "open the latest PR" affordance. Sibling of
  // latestPlan above — populated by the boardcards assembler from the
  // bulk LatestPRByIssue store helper, null when no PR is attached.
  // The chip is an external `<a>` (not a `<Link>`) because PR URLs are
  // remote http(s) — opens a new tab. Tooltip + aria-label use the
  // shared prLabel helper to shorten the GitHub URL to "owner/repo#N";
  // when more than one PR is attached the count gets surfaced in the
  // tooltip so the user knows the chip links to the newest.
  const latestPR = card.latestPR || null;
  const prTooltip = latestPR
    ? (latestPR.count > 1
      ? `Open latest PR: ${prLabel(latestPR.url)} (${latestPR.count} attached)`
      : `Open PR: ${prLabel(latestPR.url)}`)
    : '';

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
    // BACI-193: motion.article (via the `m.` short form required by
    // LazyMotion `strict`) gives us FLIP-based layout reorders inside
    // a column, exit animations when a card leaves, and shared-element
    // flight to the topbar Shipped pill via `layoutId`. The layoutId
    // matches the card.key so when this card transitions into `done`
    // and the App re-renders the destination slot inside ShippedPopover
    // with the same layoutId, Motion animates the position between
    // the two subtrees.
    //
    // `transition.layout` is tuned to --dur-slow (240ms) so layout
    // animation matches the rest of the kanban's transition family.
    // exit is snappier (--dur-medium-ish at 180ms) so removed cards
    // don't linger.
    //
    // The mid-flight inline style overrides borderRadius / boxShadow
    // because Motion's CSS-variable snapshotting distorts them during
    // the animation (see the research note in the plan).
    <m.article
      ref={ref}
      layout
      layoutId={card.key}
      initial={{ opacity: 0, scale: 0.96 }}
      animate={{ opacity: 1, scale: 1 }}
      exit={{ opacity: 0, scale: 0.96 }}
      transition={{
        layout: { duration: 0.28, ease: layoutEase },
        opacity: { duration: 0.18 },
        scale: { duration: 0.18 },
      }}
      onLayoutAnimationStart={() => setAnimating(true)}
      onLayoutAnimationComplete={() => setAnimating(false)}
      style={animating ? {
        borderRadius: 12,
        boxShadow: '0 1px 0 rgba(27,35,54,0.04), 0 1px 2px rgba(27,35,54,0.05)',
      } : undefined}
      className={`mk-card ${isDragging ? 'is-dragging' : ''} ${card.claude ? 'is-claude' : ''} ${taken ? 'is-taken' : ''} ${waiting ? 'is-waiting' : ''} ${card.archived ? 'is-archived' : ''} ${compact ? 'is-compact' : ''} ${isTrayHover ? 'is-tray-hover' : ''} ${isJumping ? 'is-jumping' : ''}`}
      draggable={!taken && !waiting}
      onDragStart={onDragStart}
      onDragEnd={onDragEnd}
      onClick={onOpen}
    >
      <div className="mk-card-top">
        {/*
          BACI-172: per-feature glyph rendered top-left of the card,
          before the issue key. aria-hidden because the glyph is a
          visual marker only — the feature association is also
          discoverable via the issue brief's `feature` field.
        */}
        {card.featureEmoji && (
          <span className="mk-card-feature-emoji" aria-hidden="true">
            {card.featureEmoji}
          </span>
        )}
        <span className="mk-card-id">{card.key}</span>
        {/*
          BACI-231: per-feature integration branch chip — visible on
          every card under a feature whose branch_name is set. Empty
          (and absent on the wire) for cards on `main` so the chip
          stays out of the way for the legacy ship-to-main flow.
          Truncated via shortBranchLabel; full ref reads in the
          tooltip on hover.
        */}
        {card.featureBranchName && (
          <Tooltip label={`Ships to ${card.featureBranchName}`}>
            <span className="mk-card-branch-chip" aria-label={`Ships to ${card.featureBranchName}`}>
              <Icon name="branch" />
              {shortBranchLabel(card.featureBranchName)}
            </span>
          </Tooltip>
        )}
        {latestPlan && (
          <Tooltip label={`Open plan: ${latestPlan.filename}`}>
            <Link
              to={documentPath(latestPlan.filename)}
              className="mk-card-plan-btn"
              aria-label={`Open plan: ${latestPlan.filename}`}
              onClick={(e) => e.stopPropagation()}
            >
              <Icon name="plan" />
            </Link>
          </Tooltip>
        )}
        {latestPR && (
          <Tooltip label={prTooltip}>
            <a
              href={latestPR.url}
              target="_blank"
              rel="noreferrer noopener"
              className="mk-card-pr-btn"
              aria-label={prTooltip}
              onClick={(e) => e.stopPropagation()}
            >
              <Icon name="pull-request" />
            </a>
          </Tooltip>
        )}
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
      {/* BACI-191: tag row hidden in compact mode to reduce card height. */}
      {!compact && card.tags && card.tags.length > 0 && (
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
          {/*
            BACI-192: follow-on dispatch button — gated on a taken /
            waiting card (BACI-180 needs an active parent dispatch to
            attach to). Outline glyph when no follow-on is queued; the
            mode label sits inside the button when one is attached.
            BACI-252: the dropdown lists every non-reserved prompt
            template in a flat list — no per-state filtering. Cancel
            item appears at the bottom only when a follow-on is already
            attached.
          */}
          {showFollowOn && (() => {
            // BACI-217: the chip's secondary label and the dropdown's
            // section header swap based on the dormant row's variant.
            // followOn.waitingReason is set server-side ("blocked by N")
            // for the blockers-clear variant; absent for the parent-acks
            // variant (today's default — the chip reads the mode label
            // without a qualifier). The dropdown header reads "When
            // unblocked →" when the card is blocked-and-idle (variant
            // we'd write on next queue) or already carries a
            // blockers-clear follow-on; otherwise "After current →".
            const waitingReason = followOn?.waitingReason || '';
            const isBlockersVariant = waitingReason !== '';
            const willQueueBlockersVariant = !followOn && blockedForFollowOn && !taken && !waiting;
            const menuHeader = (isBlockersVariant || willQueueBlockersVariant)
              ? 'When unblocked →'
              : 'After current →';
            const chipModeLabel = followOn ? (followOn.actionLabel || followOn.mode) : '';
            const chipAria = followOn
              ? (waitingReason
                  ? `Follow-on: ${chipModeLabel} (${waitingReason}) — click to change or cancel`
                  : `Follow-on: ${chipModeLabel} — click to change or cancel`)
              : 'Queue a follow-on dispatch';
            const chipTitle = followOn
              ? (waitingReason
                  ? `Follow-on queued: ${chipModeLabel} — ${waitingReason}`
                  : `Follow-on queued: ${chipModeLabel}`)
              : 'Queue follow-on';
            return (
            <DropdownMenu.Root>
              <DropdownMenu.Trigger asChild>
                <button
                  type="button"
                  className={`mk-card-followon-btn ${followOn ? 'is-attached' : ''}`}
                  aria-label={chipAria}
                  title={chipTitle}
                  onClick={(e) => e.stopPropagation()}
                >
                  <Icon name="forward" />
                  {followOn && (
                    <span className="mk-card-followon-label">
                      {chipModeLabel}
                      {waitingReason && (
                        <span className="mk-card-followon-reason"> · {waitingReason}</span>
                      )}
                    </span>
                  )}
                </button>
              </DropdownMenu.Trigger>
              <DropdownMenu.Portal>
                <DropdownMenu.Content
                  className="mk-card-action-menu mk-card-followon-menu"
                  align="end"
                  side="top"
                  sideOffset={4}
                  collisionPadding={8}
                  onClick={(e) => e.stopPropagation()}
                >
                  {/* BACI-222: the scrollable+filterable shell lives in
                      DispatchMenuContent; this call site provides the
                      row markup. The Radix DropdownMenu.Root / Trigger
                      / Portal / Content wrappers stay so we keep
                      click-outside-to-close + collision-aware placement
                      out of the box.

                      BACI-252: every non-reserved template renders in
                      one flat list — no primary / secondary / unusual
                      bucketing. Reserved slugs (`_dispatch_preamble`)
                      are filtered out by the DispatchMenuContent shell
                      via the leading-underscore convention. The
                      filter-narrowed `visible` slice is what we map
                      over so typing in the filter input narrows the
                      list as expected. */}
                  <DispatchMenuContent
                    prompts={nonReservedPrompts}
                    currentMode={followOn?.mode}
                    menuLabel={menuHeader}
                    footer={followOn ? (
                      <DropdownMenu.Item
                        className="mk-card-action-item is-danger"
                        onSelect={() => onCancelFollowOn && onCancelFollowOn(card.key)}
                        onClick={(e) => e.stopPropagation()}
                      >
                        Cancel follow-on
                      </DropdownMenu.Item>
                    ) : null}
                    renderRows={({ visible, currentMode }) => (
                      <>
                        {visible.map(p => (
                          <DropdownMenu.Item
                            key={p.mode}
                            data-dispatch-row=""
                            className={`mk-card-action-item ${currentMode === p.mode ? 'is-current' : ''}`}
                            onSelect={() => onSetFollowOn && onSetFollowOn(card.key, p.mode)}
                            onClick={(e) => e.stopPropagation()}
                          >
                            {p.actionLabel || p.label}
                          </DropdownMenu.Item>
                        ))}
                      </>
                    )}
                  />
                </DropdownMenu.Content>
              </DropdownMenu.Portal>
            </DropdownMenu.Root>
            );
          })()}
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
              {/* BACI-191: eval button hidden in compact mode — the
                  quick-eval affordance is part of the eval surface
                  that compact suppresses. The composer itself is
                  force-closed by the useEffect above. */}
              {!compact && showEvalAffordance && (
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
              {!taken && nonReservedPrompts.length > 0 && (
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
                      {/*
                        BACI-252: zap menu renders every non-reserved
                        template in one flat list — no primary /
                        secondary / unusual bucketing. BACI-209
                        compound semantics are preserved as a
                        per-row `<details>` expander: clicking the
                        primary label fires onDispatch (single mode);
                        opening the caret reveals one "Primary, then
                        Follow-on" row per other visible mode, each
                        firing onDispatchChain (unchanged server-side
                        behaviour). Default state is collapsed so the
                        menu reads at single-mode density.

                        BACI-67: render the imperative actionLabel
                        ("Plan", "Design") via the helper. label
                        (gerund — "Planning") is the fallback for
                        templates that haven't set the override and
                        aren't built-in.
                      */}
                      <DispatchMenuContent
                        prompts={nonReservedPrompts}
                        renderRows={({ visible }) => renderZapMenuRows({
                          visible,
                          cardKey: card.key,
                          onDispatch,
                          onDispatchChain,
                        })}
                      />
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
      {/* BACI-191: meta line (active verb, tasks pill, eval chip) hidden
          in compact mode. This is the whole meta surface — the eval chip
          stays hidden even if the card has transcripts, because compact
          mode is an intentional density preference. The eval content is
          still accessible by toggling compact off or opening the card. */}
      {!compact && (hasMeta || hasEvalChip) && (
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
    </m.article>
  );
}

// Memo skips the re-render on the common case where one card mutates
// (drag, dispatch): App's setCards updater returns the same object ref
// for every unchanged card, callback props are useCallback'd, so shallow
// compare passes for the others. (Doesn't help the poll path — that
// rebuilds the array from the server response, fresh refs all round.)
export default memo(forwardRef(KanbanCard));
