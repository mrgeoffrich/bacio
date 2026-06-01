import React, { useState, useMemo, useEffect, useCallback } from 'react';
import { AnimatePresence, m } from 'motion/react';
import { Link } from 'react-router';
import * as DropdownMenu from '@radix-ui/react-dropdown-menu';
import Icon from './Icon.jsx';
import Tooltip from './Tooltip.jsx';
import QuestionModal from './QuestionModal.jsx';
import ShippedPopover from './ShippedPopover.jsx';
import SessionMessageButton from './SessionMessageButton.jsx';
import { documentPath } from '../lib/routes';
import prLabel from '../lib/prLabel';
import { stageLabel, stageGlyph, isShipStage } from '../lib/pipelineProcesses';
import { blockedByMode } from '../lib/blockedByBadge';
import * as api from '../api';

// PipelineView (Phase 4) — the real three-column pipeline board, keyed on
// the server-side issue states: Backlog (todo) → In Pipeline
// (in_pipeline) → Shipping (to_be_shipped). Replaces the BACI mock
// (local placement Map) with persisted state: a card's column IS its
// issue state, ordering is server-side, and the per-card job chain +
// engine drive-mode come straight off the BoardCard.
//
// Card design intent: a compact issue card in Backlog / Shipping, and a
// stage card that grows to fill the column when in pipeline — issue
// header on top, the processing detail (job chain + active-job todos /
// question) in the body, all operation controls along the bottom.

// BLOCKER_STATE_LABEL maps the open-state set carried by a blocker
// (BoardCardBlocker.state, always one of todo | in_progress |
// needs_action | in_review) to the human label shown on the multi-blocker
// popover's state pill. Mirrors STATE_LABELS in api.http.ts (module-
// private there); kept tiny and local since only the popover needs it.
const BLOCKER_STATE_LABEL = {
  todo: 'Todo',
  in_progress: 'In Progress',
  needs_action: 'Needs Action',
  in_review: 'In Review',
};

// BlockedByBadge — the per-card blocked-by indicator (BACI-310). Purely
// informational: it never disables Start. card.blockedBy is the open
// `blocks` edges pointing AT this card (the server filters out
// done/cancelled blockers, so the badge clears automatically when a
// blocker finishes).
//
//   single blocker → a 🔒 chip showing the blocker's key directly;
//   multiple        → a 🔒 "blocked by N" chip opening a Radix popover
//                      (re-uses the .mk-card-blocked-menu* CSS) listing
//                      each blocker key + state pill.
//
// Hovering a blocker reference calls onHighlight(key) so PipelineView can
// red-highlight that blocker's card if it is currently on screen, and
// onHighlight(null) on leave. Clicking navigates to the blocker via
// onOpenIssue. All click/select handlers stopPropagation so opening the
// popover or following a link never starts a drag or trips the card's
// onOpen.
function BlockedByBadge({ blockedBy, onOpenIssue, onHighlight }) {
  const mode = blockedByMode(blockedBy);
  if (mode === 'none') return null;

  if (mode === 'single') {
    const { key } = blockedBy[0];
    return (
      <button
        type="button"
        className="mk-pl-blocked-btn"
        aria-label={`Blocked by ${key}`}
        title={`Blocked by ${key}`}
        onClick={(e) => { e.stopPropagation(); onOpenIssue?.(key); }}
        onMouseEnter={() => onHighlight?.(key)}
        onMouseLeave={() => onHighlight?.(null)}
      >
        <Icon name="lock" />
        <span className="mk-pl-blocked-lbl">{key}</span>
      </button>
    );
  }

  // multi
  return (
    <DropdownMenu.Root>
      <DropdownMenu.Trigger asChild>
        <button
          type="button"
          className="mk-pl-blocked-btn"
          aria-label={`Blocked by ${blockedBy.length} issues`}
          title={`Blocked by ${blockedBy.length} issues`}
          onClick={(e) => e.stopPropagation()}
        >
          <Icon name="lock" />
          <span className="mk-pl-blocked-lbl">blocked by {blockedBy.length}</span>
        </button>
      </DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenu.Content
          className="mk-card-blocked-menu"
          align="start"
          side="bottom"
          sideOffset={4}
          collisionPadding={8}
          onClick={(e) => e.stopPropagation()}
        >
          <div className="mk-card-blocked-menu-label">Blocked by</div>
          {blockedBy.map(b => (
            <DropdownMenu.Item
              key={b.key}
              className="mk-card-blocked-item"
              onSelect={() => onOpenIssue?.(b.key)}
              onMouseEnter={() => onHighlight?.(b.key)}
              onMouseLeave={() => onHighlight?.(null)}
            >
              <span className="mk-card-id">{b.key}</span>
              <span className={`mk-pill mk-status-${b.state}`}>
                {BLOCKER_STATE_LABEL[b.state] ?? b.state}
              </span>
            </DropdownMenu.Item>
          ))}
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  );
}
//
// Drag model: dragging a card to another column changes its state
// (onMoveCard, or the Ship hand-off for in_pipeline → Shipping); dropping
// onto a card inside the same Backlog / Shipping column reorders it
// (onReorder, 1-based position). The engine owns job progression — the
// Start / Stop / Auto controls only nudge it.

export default function PipelineView({
  cards,
  activeBoard,
  onOpenComposer,
  onOpenCard,
  onOpenIssue,
  onMoveCard,
  onFastTrack,
  onCancelCard,
  onReorder,
  onSetProcess,
  onResetProcess,
  onEditProcess,
  onStartJob,
  onStopJob,
  onRerunJob,
  onSetEngineMode,
  onShip,
  onSetAutoShip,
  onSetBacklogCollapsed,
  onShipDispatch,
  onCancelWaiting,
  shippedCount,
  shippedScope,
  onShippedScopeChange,
  timezone,
  flyingShipKey,
  shipFlashing,
  onShipFlightDone,
  onTestIncrementShipped,
}) {
  // Dev/test affordance (BACI-295 follow-up): show a button that bumps
  // the Shipped count by one so the count-rise → ka-ching path can be
  // exercised with a real user gesture. Off for normal users — opt in
  // with `?sfxtest` in the URL or `localStorage.bacioShipSfxTest = '1'`.
  const showSfxTest = useMemo(() => {
    try {
      if (typeof window === 'undefined') return false;
      if (new URLSearchParams(window.location.search).has('sfxtest')) return true;
      return window.localStorage?.getItem('bacioShipSfxTest') === '1';
    } catch { return false; }
  }, []);
  const [activeQuestionId, setActiveQuestionId] = useState(null);
  const [expanded, setExpanded] = useState(false);
  // collapsed (BACI-288) shrinks the Backlog column to a thin rail so the
  // In Pipeline + Shipping columns get the full width. Persisted per-repo
  // in the tui_settings KV — seeded from the backend GET, same pattern as
  // autoShip below. It overrides the transient `expanded` grid toggle.
  const [collapsed, setCollapsed] = useState(false);
  const [dragKey, setDragKey] = useState(null);
  // Holds the column the dragged card is currently over, driving the
  // `is-drop` highlight. BACI-268 widens the value space with a non-column
  // 'trash' sentinel for the drag-to-cancel bin — it only feeds the
  // `=== 'trash'` highlight check, never a column lookup.
  const [dragOverCol, setDragOverCol] = useState(null);
  // autoShip seeds from the backend (GET /auto-ship) — the DB value the
  // controller's auto-ship ticker actually acts on — so the toggle
  // reflects the source of truth across machines, not a local cache.
  const [autoShip, setAutoShip] = useState(false);
  // highlightKey (BACI-310): the issue key of the blocker currently being
  // hovered in some card's blocked-by badge. The card whose key matches
  // paints itself red (.is-blocker-hl). A no-op when the hovered blocker
  // isn't rendered on the pipeline — no card matches, nothing highlights.
  const [highlightKey, setHighlightKey] = useState(null);
  const onHighlight = useCallback((k) => setHighlightKey(k), []);

  useEffect(() => {
    if (!activeBoard) { setAutoShip(false); return; }
    let cancelled = false;
    api.getAutoShip(activeBoard)
      .then((v) => { if (!cancelled) setAutoShip(!!v); })
      .catch(() => { if (!cancelled) setAutoShip(false); });
    return () => { cancelled = true; };
  }, [activeBoard]);

  useEffect(() => {
    if (!activeBoard) { setCollapsed(false); return; }
    let cancelled = false;
    api.getBacklogCollapsed(activeBoard)
      .then((v) => { if (!cancelled) setCollapsed(!!v); })
      .catch(() => { if (!cancelled) setCollapsed(false); });
    return () => { cancelled = true; };
  }, [activeBoard]);

  const list = cards || [];
  const backlog = useMemo(() => list.filter(c => c.column === 'todo'), [list]);
  const inPipeline = useMemo(() => list.filter(c => c.column === 'in_pipeline'), [list]);
  const shipping = useMemo(() => list.filter(c => c.column === 'to_be_shipped'), [list]);
  const cardByKey = useMemo(() => new Map(list.map(c => [c.key, c])), [list]);

  const toggleAutoShip = useCallback(() => {
    const next = !autoShip;
    setAutoShip(next); // optimistic
    Promise.resolve(onSetAutoShip?.(next)).catch(() => {
      // Revert the optimistic flip if the PUT failed.
      setAutoShip(!next);
    });
  }, [autoShip, onSetAutoShip]);

  const toggleCollapsed = useCallback(() => {
    const next = !collapsed;
    setCollapsed(next); // optimistic
    Promise.resolve(onSetBacklogCollapsed?.(next)).catch(() => {
      // Revert the optimistic flip if the PUT failed.
      setCollapsed(!next);
    });
  }, [collapsed, onSetBacklogCollapsed]);

  // Move a card into `col` (= its new state). in_pipeline → Shipping goes
  // through the Ship hand-off; everything else is a plain state move. Shared
  // by the column drop zone and the cross-column case of dropOnCard.
  const moveCardToColumn = (card, col) => {
    if (col === 'to_be_shipped' && card.column === 'in_pipeline') {
      onShip?.(card.key);
    } else {
      onMoveCard?.(card.key, col);
    }
  };

  // Cross-column drop onto a column's empty area.
  const dropToColumn = (col) => {
    setDragOverCol(null);
    const key = dragKey;
    setDragKey(null);
    if (!key) return;
    const card = cardByKey.get(key);
    if (!card || card.column === col) return;
    moveCardToColumn(card, col);
  };

  // Dropping onto a card. Within the same Backlog / Shipping list it's a
  // reorder to that card's 1-based slot; dropping a card from another column
  // onto one of these cards is a cross-column move into the target's column.
  // The card's own onDrop stops propagation, so the column drop zone never
  // sees the event — without the cross-column branch here the move silently
  // no-ops, which is BACI-269 (drag In Pipeline card onto a Backlog card).
  const dropOnCard = (targetCard, index) => {
    const key = dragKey;
    setDragKey(null);
    setDragOverCol(null);
    if (!key || key === targetCard.key) return;
    const dragged = cardByKey.get(key);
    if (!dragged) return;
    if (dragged.column !== targetCard.column) {
      moveCardToColumn(dragged, targetCard.column);
      return;
    }
    onReorder?.(key, index + 1);
  };

  const colDropProps = (col) => ({
    onDragOver: (e) => { e.preventDefault(); setDragOverCol(col); },
    onDragLeave: (e) => { if (e.currentTarget === e.target) setDragOverCol(null); },
    onDrop: (e) => { e.preventDefault(); dropToColumn(col); },
  });

  // BACI-268: drop onto the trash bin cancels the dragged issue. Mirrors
  // dropToColumn but routes to onCancelCard instead of a column move; the
  // bin accepts a card from any column (no column guard) since `cancelled`
  // is a plain terminal transition.
  const cancelToTrash = () => {
    const key = dragKey;
    setDragKey(null);
    setDragOverCol(null);
    if (!key) return;
    const card = cardByKey.get(key);
    if (!card) return;
    onCancelCard?.(card.key);
  };

  const trashDropProps = () => ({
    onDragOver: (e) => { e.preventDefault(); setDragOverCol('trash'); },
    onDragLeave: (e) => { if (e.currentTarget === e.target) setDragOverCol(null); },
    onDrop: (e) => { e.preventDefault(); cancelToTrash(); },
  });

  if (!activeBoard) {
    return (
      <div className="mk-pl">
        <div className="mk-pl-empty">Select a repository to view its pipeline.</div>
      </div>
    );
  }

  // Collapse wins over the transient expand-to-grid toggle: a collapsed
  // column never renders in expanded-grid mode.
  const gridExpanded = expanded && !collapsed;

  return (
    <div className={`mk-pl${gridExpanded ? ' is-backlog-expanded' : ''}${collapsed ? ' is-backlog-collapsed' : ''}`}>
      {/* ── Backlog ── */}
      <section
        className={`mk-pl-col mk-pl-backlog${collapsed ? ' is-collapsed' : ''}${dragOverCol === 'todo' ? ' is-drop' : ''}`}
        {...colDropProps('todo')}
      >
        {collapsed ? (
          // Thin rail: an expand button, the vertical "Backlog" label, and
          // the backlog count — enough to know what's hidden and reopen it.
          <button
            type="button"
            className="mk-pl-rail"
            onClick={toggleCollapsed}
            aria-label="Expand backlog"
            aria-expanded={false}
            title="Expand backlog"
          >
            <span className="mk-pl-rail-icon">»</span>
            <span className="mk-pl-rail-lbl">Backlog</span>
            <span className="mk-pl-rail-count">{backlog.length}</span>
          </button>
        ) : (
          <>
            <header className="mk-pl-col-head">
              <span className="mk-pl-pill is-todo">Backlog</span>
              <span className="mk-pl-count">{backlog.length}</span>
              <span className="mk-pl-spacer" />
              {/* BACI-287: the + (new issue) button moved here from the
                  topbar (which now holds the notification bell). Gated on a
                  real prefix — the composer needs one to create against —
                  same gate the topbar used. */}
              {onOpenComposer && activeBoard && activeBoard !== 'all' && (
                <Tooltip label="New issue (⌘N)">
                  <button
                    type="button"
                    className="mk-pl-new-issue"
                    aria-label="New issue"
                    onClick={onOpenComposer}
                  >
                    <Icon name="plus" />
                  </button>
                </Tooltip>
              )}
              <button
                type="button"
                className="mk-pl-drawer-toggle"
                onClick={() => setExpanded(e => !e)}
                aria-label={expanded ? 'Shrink backlog' : 'Widen backlog'}
                aria-expanded={expanded}
                title={expanded ? 'Shrink backlog' : 'Widen backlog'}
              >
                {expanded ? '«' : '»'}
              </button>
              <button
                type="button"
                className="mk-pl-drawer-toggle"
                onClick={toggleCollapsed}
                aria-label="Collapse backlog"
                aria-expanded
                title="Collapse backlog to a rail"
              >
                ⟨
              </button>
            </header>
            <div className={`mk-pl-backlog-body${expanded ? ' is-grid' : ''}`}>
              {backlog.length === 0 ? (
                <div className="mk-pl-col-empty">No backlog items</div>
              ) : (
                backlog.map((card, i) => (
                  <PipelineCard
                    key={card.key}
                    card={card}
                    index={i}
                    showBadge={expanded}
                    backlog
                    isDragging={dragKey === card.key}
                    isHighlighted={card.key === highlightKey}
                    onOpen={() => onOpenCard?.(card)}
                    onOpenIssue={onOpenIssue}
                    onHighlight={onHighlight}
                    onDragStart={() => setDragKey(card.key)}
                    onDragEnd={() => { setDragKey(null); setDragOverCol(null); }}
                    onDropCard={() => dropOnCard(card, i)}
                    onMoveCard={onMoveCard}
                    onFastTrack={onFastTrack}
                  />
                ))
              )}
            </div>
          </>
        )}
      </section>

      {/* ── In Pipeline ── */}
      <section
        className={`mk-pl-col mk-pl-stage-col${dragOverCol === 'in_pipeline' ? ' is-drop' : ''}`}
        {...colDropProps('in_pipeline')}
      >
        <header className="mk-pl-col-head">
          <span className="mk-pl-pill is-pipe">In Pipeline</span>
          <span className="mk-pl-count">{inPipeline.length}</span>
        </header>
        <div className="mk-pl-stage-body">
          {inPipeline.length === 0 && (
            <div className="mk-pl-col-empty mk-pl-stage-empty">
              Drag a card here to run it through the pipeline
            </div>
          )}
          <AnimatePresence mode="sync" initial={false}>
            {inPipeline.map(card => (
              <m.div
                key={card.key}
                layout
                initial={{ opacity: 0 }}
                animate={{ opacity: 1 }}
                exit={{ opacity: 0 }}
                transition={{ duration: 0.2 }}
              >
                <StageCard
                  card={card}
                  activeBoard={activeBoard}
                  isDragging={dragKey === card.key}
                  isHighlighted={card.key === highlightKey}
                  onOpen={() => onOpenCard?.(card)}
                  onOpenIssue={onOpenIssue}
                  onHighlight={onHighlight}
                  onDragStart={() => setDragKey(card.key)}
                  onDragEnd={() => { setDragKey(null); setDragOverCol(null); }}
                  onSetProcess={onSetProcess}
                  onResetProcess={onResetProcess}
                  onEditProcess={onEditProcess}
                  onStartJob={onStartJob}
                  onStopJob={onStopJob}
                  onRerunJob={onRerunJob}
                  onSetEngineMode={onSetEngineMode}
                  onShip={onShip}
                  onOpenQuestion={(id) => setActiveQuestionId(id)}
                />
              </m.div>
            ))}
          </AnimatePresence>
        </div>
      </section>

      {/* ── Shipping ── */}
      <section
        className={`mk-pl-col mk-pl-shipping${dragOverCol === 'to_be_shipped' ? ' is-drop' : ''}`}
        {...colDropProps('to_be_shipped')}
      >
        <header className="mk-pl-col-head">
          <span className="mk-pl-pill is-ship">Shipping</span>
          <span className="mk-pl-count">{shipping.length}</span>
          <span className="mk-pl-spacer" />
          <label className="mk-pl-toggle" title="Auto-ship the next card">
            Auto-ship
            <button
              type="button"
              role="switch"
              aria-checked={autoShip}
              className={`mk-pl-switch${autoShip ? ' is-on' : ''}`}
              onClick={toggleAutoShip}
            />
          </label>
        </header>
        <div className="mk-pl-shipping-tools">
          <ShippedPopover
            activeBoard={activeBoard}
            shippedCount={shippedCount}
            scope={shippedScope}
            onScopeChange={onShippedScopeChange}
            timezone={timezone}
            onOpenIssue={onOpenIssue}
            flyingShipKey={flyingShipKey}
            shipFlashing={shipFlashing}
            onShipFlightDone={onShipFlightDone}
          />
          {showSfxTest && (
            <button
              type="button"
              className="mk-pill"
              onClick={onTestIncrementShipped}
              title="Test: bump the Shipped count by 1 to fire the ka-ching (dev only; the next poll re-syncs the real count)"
            >
              🔔 Test +1
            </button>
          )}
        </div>
        <div className="mk-pl-col-body">
          {shipping.length === 0 ? (
            <div className="mk-pl-col-empty">Nothing to ship</div>
          ) : (
            shipping.map((card, i) => (
              <PipelineCard
                key={card.key}
                card={card}
                activeBoard={activeBoard}
                index={i}
                shipping
                isNextToShip={i === 0}
                autoShip={autoShip}
                isDragging={dragKey === card.key}
                isHighlighted={card.key === highlightKey}
                onOpen={() => onOpenCard?.(card)}
                onOpenIssue={onOpenIssue}
                onHighlight={onHighlight}
                onDragStart={() => setDragKey(card.key)}
                onDragEnd={() => { setDragKey(null); setDragOverCol(null); }}
                onDropCard={() => dropOnCard(card, i)}
                onShipDispatch={onShipDispatch}
                onCancelWaiting={onCancelWaiting}
              />
            ))
          )}
        </div>
      </section>

      {/* ── Trash bin (BACI-268) — drop a card here to cancel its issue.
          Pinned bottom-right of the pipeline area; lights up with the
          shared `is-drop` highlight on drag-over. ── */}
      <div
        className={`mk-pl-trash${dragOverCol === 'trash' ? ' is-drop' : ''}`}
        aria-label="Cancel issue (drop here)"
        {...trashDropProps()}
      >
        <Icon name="trash" />
        <span className="mk-pl-trash-lbl">Cancel</span>
      </div>

      <QuestionModal
        questionId={activeQuestionId}
        onClose={() => setActiveQuestionId(null)}
      />
    </div>
  );
}

// PipelineCard — the compact issue card for Backlog and Shipping. Issue
// info only (feature glyph, key, plan/PR icon buttons, blocked-by badge,
// title, labels) — the issue card only ever shows the issue itself.
// Shipping adds a position badge + the Next-to-ship SHIP row / waiting
// status.
function PipelineCard({
  card,
  activeBoard,
  index,
  showBadge,
  shipping,
  backlog,
  isNextToShip,
  autoShip,
  isDragging,
  isHighlighted,
  onOpen,
  onOpenIssue,
  onHighlight,
  onDragStart,
  onDragEnd,
  onDropCard,
  onMoveCard,
  onFastTrack,
  onShipDispatch,
  onCancelWaiting,
}) {
  const [over, setOver] = useState(false);
  const waiting = !!card.waitingState && !card.taken;
  const shippingInFlight = shipping && (card.taken || waiting);

  return (
    <article
      className={`mk-pl-card${isDragging ? ' is-dragging' : ''}${over ? ' is-drop-before' : ''}${shippingInFlight ? ' is-shipping' : ''}${isHighlighted ? ' is-blocker-hl' : ''}`}
      draggable
      onDragStart={onDragStart}
      onDragEnd={onDragEnd}
      onDragOver={(e) => { e.preventDefault(); e.stopPropagation(); setOver(true); }}
      onDragLeave={() => setOver(false)}
      onDrop={(e) => { e.preventDefault(); e.stopPropagation(); setOver(false); onDropCard?.(); }}
      onClick={onOpen}
    >
      {showBadge && (
        <span
          className={`mk-pl-badge${shipping && isNextToShip ? ' is-next' : ''}`}
          aria-hidden="true"
        >
          {index + 1}
        </span>
      )}
      <CardHead card={card} activeBoard={activeBoard} onOpenIssue={onOpenIssue} onHighlight={onHighlight} />
      <h3 className="mk-pl-card-title">{card.title}</h3>
      <CardLabels tags={card.tags} />
      {backlog && (
        <div className="mk-pl-card-foot">
          <span className="mk-pl-spacer" />
          {/* Fast-track (BACI-311): one click moves the card into the
              pipeline, assigns Plan → Implement → Ship, and turns Auto on. */}
          <button
            type="button"
            className="mk-pl-btn is-primary is-sm"
            title="Fast-track: into pipeline, Plan → Implement → Ship, Auto on"
            aria-label="Fast-track: into pipeline, Plan → Implement → Ship, Auto on"
            onClick={(e) => { e.stopPropagation(); onFastTrack?.(card.key); }}
          >
            <Icon name="zap" />
          </button>
          <button
            type="button"
            className="mk-pl-btn is-ghost is-sm"
            title="Move into the pipeline"
            aria-label="Move into the pipeline"
            onClick={(e) => { e.stopPropagation(); onMoveCard?.(card.key, 'in_pipeline'); }}
          >
            <Icon name="forward" />
          </button>
        </div>
      )}
      {shipping && (
        <div className="mk-pl-ship-row">
          {shippingInFlight ? (
            <>
              <span className="mk-pl-ship-status">
                <span className="mk-pl-spin" /> Shipping…
              </span>
              <span className="mk-pl-spacer" />
              {waiting && (
                <button
                  type="button"
                  className="mk-pl-btn is-ghost is-danger is-sm"
                  onClick={(e) => { e.stopPropagation(); onCancelWaiting?.(card.key); }}
                >
                  Cancel
                </button>
              )}
            </>
          ) : isNextToShip ? (
            <>
              <span className="mk-pl-next">Next to ship</span>
              <span className="mk-pl-spacer" />
              {!autoShip && (
                <button
                  type="button"
                  className="mk-pl-btn is-primary is-sm"
                  onClick={(e) => { e.stopPropagation(); onShipDispatch?.(card.key, 'ship'); }}
                >
                  ⏏ SHIP
                </button>
              )}
            </>
          ) : (
            <span className="mk-pl-queued">⏳ Waiting in queue</span>
          )}
        </div>
      )}
    </article>
  );
}

// CardHead — feature glyph · issue key · plan / PR icon buttons · blocked-by
// badge (each only when it applies). Shared by the compact card and the
// stage card's header so the anatomy stays identical, which is how the
// blocked-by badge lands on all three card types at once.
function CardHead({ card, activeBoard, onOpenIssue, onHighlight }) {
  const latestPlan = card.latestPlan || null;
  const latestPR = card.latestPR || null;
  return (
    <div className="mk-pl-card-top">
      {card.featureEmoji && (
        <span className="mk-pl-card-emoji" aria-hidden="true">{card.featureEmoji}</span>
      )}
      <span className="mk-pl-card-id">{card.key}</span>
      <BlockedByBadge
        blockedBy={card.blockedBy}
        onOpenIssue={onOpenIssue}
        onHighlight={onHighlight}
      />
      <span className="mk-pl-card-icons">
        {latestPlan && (
          <Tooltip label={`Open plan: ${latestPlan.filename}`}>
            <Link
              to={documentPath(activeBoard, latestPlan.filename)}
              className="mk-pl-icobtn"
              aria-label={`Open plan: ${latestPlan.filename}`}
              onClick={(e) => e.stopPropagation()}
            >
              <Icon name="plan" />
            </Link>
          </Tooltip>
        )}
        {latestPR && (
          <Tooltip label={`Open PR: ${prLabel(latestPR.url)}`}>
            <a
              href={latestPR.url}
              target="_blank"
              rel="noreferrer noopener"
              className="mk-pl-icobtn"
              aria-label={`Open PR: ${prLabel(latestPR.url)}`}
              onClick={(e) => e.stopPropagation()}
            >
              <Icon name="pull-request" />
            </a>
          </Tooltip>
        )}
      </span>
    </div>
  );
}

function CardLabels({ tags }) {
  if (!tags || tags.length === 0) return null;
  return (
    <div className="mk-pl-card-labels">
      {tags.map(t => <span key={t} className="mk-pl-label">{t}</span>)}
    </div>
  );
}

// StageCard — the in-pipeline card that grows to fill the column. Issue
// header on top; the processing area (job chain + active job / question)
// in the body; all controls along the bottom. When no process has been
// chosen yet, a process-pick menu sits over the card.
function StageCard({
  card,
  activeBoard,
  isDragging,
  isHighlighted,
  onOpen,
  onOpenIssue,
  onHighlight,
  onDragStart,
  onDragEnd,
  onSetProcess,
  onResetProcess,
  onEditProcess,
  onStartJob,
  onStopJob,
  onRerunJob,
  onSetEngineMode,
  onShip,
  onOpenQuestion,
}) {
  const [picking, setPicking] = useState(false);
  // BACI-314 Reset is a two-step in-card confirm: unarmed → armed (Confirm /
  // Cancel) → fires. Inline in the footer, not a modal, because Reset is
  // destructive (drops completed-job history) and must match the in-card
  // interaction pattern.
  const [confirmingReset, setConfirmingReset] = useState(false);

  const jobs = card.jobs || [];
  const hasProcess = jobs.length > 0;
  const running = jobs.find(j => j.status === 'running') || null;
  // Reset is hidden while a job is running (the Stop-first rule — the engine
  // refuses ErrJobRunning anyway). If the card transitions to running while
  // the confirm is armed, drop the armed state so a stale Confirm can't
  // linger after the affordance disappears.
  useEffect(() => {
    if (running && confirmingReset) setConfirmingReset(false);
  }, [running, confirmingReset]);
  const nonShip = jobs.filter(j => !isShipStage(j.mode));
  const nextPending = jobs.find(j => j.status === 'pending');
  // allDone = every non-ship job is complete (cancelled deliberately does
  // NOT count — a Stopped job is Aborted, not "ready to hand off"; Ship
  // stays disabled on it).
  const allDone = nonShip.length > 0 && !running &&
    nonShip.every(j => j.status === 'complete');
  // aborted = the chain is wedged on a cancelled job — nothing running, no
  // pending step left to auto-run, and at least one non-ship job cancelled.
  // The user re-runs the aborted step (or Edits the process) to proceed.
  const aborted = !running && !nextPending &&
    nonShip.some(j => j.status === 'cancelled');
  const engineAuto = card.engineMode === 'auto';
  const question = (card.openQuestions || [])[0] || null;
  // BACI-296 / BACI-300: a worker that died on an Anthropic API error
  // halts the chain in place with engine_pause_reason set (no open
  // question). The reason carries the error class so the halt copy can
  // distinguish a passing outage from an account/config problem the user
  // must fix.
  const agentErrorTransient = card.enginePauseReason === 'agent_error_transient';
  const agentErrorTerminal = card.enginePauseReason === 'agent_error_terminal';
  const agentErrored = agentErrorTransient || agentErrorTerminal;
  // BACI-328: a user soft-cancelled the dispatch worker — the supervisor's
  // report_subagent_incomplete callback halted the chain in place with the
  // neutral subagent_cancelled reason. It's a deliberate stop, not a
  // failure, so it renders as a calm "Cancelled" pill (no is-error styling),
  // worded "Start to retry".
  const subagentCancelled = card.enginePauseReason === 'subagent_cancelled';
  const paused = card.enginePauseReason === 'open_question' || agentErrored || subagentCancelled || !!question;

  const showProcessMenu = picking || !hasProcess;

  return (
    <article
      className={`mk-pl-stage${isDragging ? ' is-dragging' : ''}${paused ? ' is-attn' : ''}${isHighlighted ? ' is-blocker-hl' : ''}`}
      draggable
      onDragStart={onDragStart}
      onDragEnd={onDragEnd}
    >
      <header className="mk-pl-stage-head" onClick={onOpen}>
        <CardHead card={card} activeBoard={activeBoard} onOpenIssue={onOpenIssue} onHighlight={onHighlight} />
        <h3 className="mk-pl-stage-title">{card.title}</h3>
        <CardLabels tags={card.tags} />
      </header>

      {showProcessMenu ? (
        <ProcessMenu
          dimmedHasProcess={hasProcess}
          jobs={jobs}
          onPick={(selection) => { setPicking(false); onSetProcess?.(card.key, selection); }}
          onCancel={hasProcess ? () => setPicking(false) : null}
        />
      ) : (
        <>
          <div className="mk-pl-stage-proc">
            <div className="mk-pl-proclabel">Process</div>
            <JobChain jobs={jobs} />
          </div>
          <div className="mk-pl-stage-detail">
            {question ? (
              <QuestionPanel question={question} onOpenQuestion={onOpenQuestion} />
            ) : allDone ? (
              <DoneBox jobs={nonShip} />
            ) : aborted ? (
              <AbortedBox
                jobs={nonShip}
                onRerunJob={(seq) => onRerunJob?.(card.key, seq)}
              />
            ) : running ? (
              <ActiveJob card={card} job={running} />
            ) : (
              <div className="mk-pl-proc-empty">
                Process selected. Press <b>Start</b> to run the next job, or flip
                {' '}<b>Auto</b> to run them through.
              </div>
            )}
          </div>
          <footer className="mk-pl-stage-foot">
            {running ? (
              <>
                <button
                  type="button"
                  className="mk-pl-btn is-ghost is-danger is-sm"
                  onClick={() => onStopJob?.(card.key)}
                >
                  ■ Stop
                </button>
                {/* BACI-286: steer the running worker. Only when the
                    running job's bound session is known. */}
                {card.runningSessionId && (
                  <SessionMessageButton sessionId={card.runningSessionId} compact />
                )}
              </>
            ) : (
              <button
                type="button"
                className="mk-pl-btn is-primary is-sm"
                disabled={!nextPending || allDone || engineAuto}
                onClick={() => onStartJob?.(card.key)}
              >
                ▶ Start
              </button>
            )}
            <label className="mk-pl-toggle">
              Auto
              <button
                type="button"
                role="switch"
                aria-checked={engineAuto}
                className={`mk-pl-switch${engineAuto ? ' is-on' : ''}`}
                onClick={() => onSetEngineMode?.(card.key, engineAuto ? 'off' : 'auto')}
              />
            </label>
            <button
              type="button"
              className="mk-pl-btn is-ghost is-sm"
              onClick={() => onEditProcess?.(card.key)}
              title="Edit the process"
            >
              ✎ Edit Process
            </button>
            {/* BACI-314 Reset: in-card two-step confirm. Hidden while a job
                is running (Stop first) and on a card with no process (the
                fresh picker is already showing). Unarmed → "✕ Reset"; armed
                swaps to an inline "Reset process? Confirm / Cancel" row.
                Wipes the whole chain → the card drops back to the picker. */}
            {!running && hasProcess && (
              confirmingReset ? (
                <span className="mk-pl-reset-confirm">
                  Reset process?
                  <button
                    type="button"
                    className="mk-pl-btn is-danger is-sm"
                    onClick={() => { setConfirmingReset(false); onResetProcess?.(card.key); }}
                  >
                    Confirm
                  </button>
                  <button
                    type="button"
                    className="mk-pl-btn is-ghost is-sm"
                    onClick={() => setConfirmingReset(false)}
                  >
                    Cancel
                  </button>
                </span>
              ) : (
                <button
                  type="button"
                  className="mk-pl-btn is-ghost is-danger is-sm"
                  onClick={() => setConfirmingReset(true)}
                  title="Wipe the process and pick again from scratch"
                >
                  ✕ Reset
                </button>
              )
            )}
            <span className="mk-pl-spacer" />
            {paused && (
              <span
                className={`mk-pl-halt${agentErrored ? ' is-error' : ''}`}
                title={
                  agentErrorTransient
                    ? 'API outage — Start to retry once it clears'
                    : agentErrorTerminal
                      ? 'Account / billing / auth error — fix it, then Start'
                      : subagentCancelled
                        ? 'Cancelled — Start to retry'
                        : undefined
                }
              >
                {agentErrorTransient
                  ? '⚠ API outage'
                  : agentErrorTerminal
                    ? '⚠ Account error'
                    : subagentCancelled
                      ? '⏸ Cancelled'
                      : '⏸ Auto halted'}
              </span>
            )}
            {/* BACI-314: render Ship ONLY when shippable — an un-shippable
                card shows no Ship control at all (was present-but-disabled). */}
            {allDone && (
              <button
                type="button"
                className="mk-pl-btn is-sm is-primary"
                onClick={() => onShip?.(card.key)}
              >
                ⏏ Ship
              </button>
            )}
          </footer>
        </>
      )}
    </article>
  );
}

// JobChain — the stepper across the top of the processing area. Each job
// renders as a step with a status-driven dot; the Ship sentinel renders
// as the hand-off step.
function JobChain({ jobs }) {
  return (
    <div className="mk-pl-chain">
      {jobs.map((j, i) => {
        const ship = isShipStage(j.mode);
        const cls = ship ? 'handoff' : j.status;
        let dot = `${i + 1}`;
        if (ship) dot = '⏏';
        else if (j.status === 'complete') dot = '✓';
        else if (j.status === 'running') dot = '●';
        else if (j.status === 'cancelled') dot = '✕';
        return (
          <React.Fragment key={j.sequence}>
            {i > 0 && <span className="mk-pl-connector" />}
            <span className={`mk-pl-step is-${cls}`}>
              <span className="mk-pl-dot">{dot}</span>
              <span className="mk-pl-step-lbl">{stageLabel(j.mode)}</span>
            </span>
          </React.Fragment>
        );
      })}
    </div>
  );
}

// ActiveJob — the running job's mode + live meta + the worker's todo
// list (from the card's TodoWrite projection).
function ActiveJob({ card, job }) {
  const todos = card.todos || [];
  const verb = card.activeVerb || '';
  // A queued/delivered dispatch reads as "running" on the job long before a
  // worker actually starts. card.taken flips true only once the worker opens
  // its claim, so gate the live word on it: "waiting for an agent" until then.
  const taken = !!card.taken;
  return (
    <div className="mk-pl-job">
      <div className="mk-pl-job-head">
        <span className="mk-pl-jmode">{stageLabel(job.mode)}</span>
        <span className="mk-pl-jmeta">
          {taken
            ? <span className="mk-pl-live">running</span>
            : <span className="mk-pl-live is-waiting">waiting for an agent</span>}
          {verb ? ` · ${verb}` : ''}
          {card.todosTotal ? ` · ${card.todosDone}/${card.todosTotal}` : ''}
        </span>
      </div>
      {todos.length > 0 && (
        <>
          <div className="mk-pl-proclabel">Job todos</div>
          <ul className="mk-pl-todos">
            {todos.map((t, i) => (
              <li key={i} className={`mk-pl-todo is-${t.status}`}>
                <span className="mk-pl-todo-box">
                  {t.status === 'completed' ? '✓' : t.status === 'in_progress' ? '◔' : ''}
                </span>
                {t.content}
              </li>
            ))}
          </ul>
        </>
      )}
    </div>
  );
}

// DoneBox — every non-ship job complete; ready to hand off to Shipping.
function DoneBox({ jobs }) {
  const total = jobs.length;
  return (
    <div className="mk-pl-job is-done">
      <div className="mk-pl-job-head">
        <span className="mk-pl-jmode is-done">Done</span>
        <span className="mk-pl-jmeta">{total} of {total} jobs complete · ready to hand off</span>
      </div>
    </div>
  );
}

// AbortedBox — the chain is wedged on one or more Stopped (cancelled)
// jobs. Each aborted job gets a Re-run control that resets it to pending
// and re-dispatches it. Ship stays disabled until every job is complete.
function AbortedBox({ jobs, onRerunJob }) {
  const cancelled = jobs.filter(j => j.status === 'cancelled');
  return (
    <div className="mk-pl-job is-aborted">
      <div className="mk-pl-job-head">
        <span className="mk-pl-jmode is-aborted">Aborted</span>
        <span className="mk-pl-jmeta">
          {cancelled.length === 1 ? 'Stopped before it finished' : `${cancelled.length} stopped jobs`}
          {' · re-run to continue'}
        </span>
      </div>
      <ul className="mk-pl-aborted-list">
        {cancelled.map(j => (
          <li key={j.sequence} className="mk-pl-aborted-item">
            <span className="mk-pl-aborted-lbl">{stageLabel(j.mode)}</span>
            <button
              type="button"
              className="mk-pl-btn is-sm mk-pl-rerun"
              onClick={() => onRerunJob?.(j.sequence)}
            >
              ↻ Re-run
            </button>
          </li>
        ))}
      </ul>
    </div>
  );
}

// QuestionPanel — the open question on the current job. Auto halts until
// it's answered; clicking opens the shared QuestionModal.
function QuestionPanel({ question, onOpenQuestion }) {
  return (
    <button
      type="button"
      className="mk-pl-qpanel"
      onClick={(e) => { e.stopPropagation(); onOpenQuestion?.(question.id); }}
    >
      <span className="mk-pl-qhead">❓ Waiting on you</span>
      <span className="mk-pl-qq">
        {question.firstQuestion || question.header || 'The worker needs your input — click to answer.'}
      </span>
    </button>
  );
}

// SEG_ORDER is the fixed canonical order the four stage toggles render and
// assemble in, regardless of click order (Design → Plan → Implement → Ship).
const SEG_ORDER = ['design', 'plan', 'implement', 'ship'];

// STANDALONE_OPTIONS are the no-chain single-agent rows offered below the
// segmented stage toggles — each is a distinct agent that runs one pass
// and finishes in place, mutually exclusive with the four toggles and
// with each other. Large Plan is a big planning pass; Scope and Research
// are the BACI-300 triage stages that replaced the retired manual-triage
// dispatch path.
const STANDALONE_OPTIONS = ['plan_large', 'scope', 'research'];

// ProcessMenu — the "pick a process" overlay shown over a freshly-entered
// card (no chain yet) or when editing (BACI-299, Option C: the segmented
// checklist). Four independent stage toggles (Design / Plan / Implement /
// Ship) light up when on and assemble into a chain in canonical order; the
// lit segments ARE the preview, with a muted caption beneath. Below an "or"
// divider, a set of standalone rows (Large Plan / Scope / Research) are each
// mutually exclusive with the four toggles and with each other. The issue
// card sits dimmed under the scrim.
function ProcessMenu({ dimmedHasProcess, jobs, onPick, onCancel }) {
  // Lazy initialiser: on Edit Process pre-fill from the card's existing job
  // modes (a standalone option wins if present); on a fresh card default to
  // Plan + Implement on (preserving the old stepper's selectedUpTo = 2).
  const existingStandalone = () =>
    STANDALONE_OPTIONS.find(slug => (jobs || []).some(j => j.mode === slug)) || null;
  const [stages, setStages] = useState(() => {
    if (dimmedHasProcess && existingStandalone()) {
      return { design: false, plan: false, implement: false, ship: false };
    }
    if (dimmedHasProcess) {
      const modes = new Set((jobs || []).map(j => j.mode));
      return {
        design: modes.has('design'),
        plan: modes.has('plan'),
        implement: modes.has('implement'),
        ship: modes.has('ship'),
      };
    }
    return { design: false, plan: true, implement: true, ship: false };
  });
  // `standalone` is the selected standalone slug (or null when the
  // segmented chain is active).
  const [standalone, setStandalone] = useState(() =>
    dimmedHasProcess ? existingStandalone() : null
  );

  // toggleStage flips one segment and clears any standalone selection
  // (the two are mutually exclusive).
  const toggleStage = (mode) => {
    setStandalone(null);
    setStages(s => ({ ...s, [mode]: !s[mode] }));
  };
  // selectStandalone toggles a standalone row; turning one on clears all
  // four segments and supersedes any other standalone, turning the active
  // one off leaves the segbar empty.
  const selectStandalone = (slug) => {
    setStandalone(cur => {
      const next = cur === slug ? null : slug;
      if (next) setStages({ design: false, plan: false, implement: false, ship: false });
      return next;
    });
  };

  // `selected` is the canonical-order chain assembled from the toggles (or
  // the chosen standalone) — the single source of truth for both the
  // caption and the Confirm payload, so the lit bar and the sent list never
  // drift.
  const selected = standalone
    ? [standalone]
    : SEG_ORDER.filter(mode => stages[mode]);
  const canConfirm = selected.length > 0;

  // A connector lights only when BOTH flanking segments are on, so a gap
  // (e.g. Plan skipped between Design and Implement) reads honestly.
  const segConnActive = (i) => stages[SEG_ORDER[i]] && stages[SEG_ORDER[i + 1]];

  return (
    <div className="mk-pl-procmenu">
      <div className="mk-pl-procmenu-title">
        {dimmedHasProcess ? 'Replace the process' : 'Pick a process'}
      </div>

      <div className={`mk-pl-segbar${standalone ? ' is-dim' : ''}`}>
        {SEG_ORDER.map((mode, i) => {
          const on = stages[mode];
          return (
            <React.Fragment key={mode}>
              {i > 0 && (
                <span className={`mk-pl-step-conn${segConnActive(i - 1) ? ' is-active' : ''}`} />
              )}
              <button
                type="button"
                className={`mk-pl-seg is-${mode}${on ? ' is-on' : ''}`}
                disabled={!!standalone}
                onClick={() => toggleStage(mode)}
                title={on ? `${stageLabel(mode)} on — click to drop it` : `Add ${stageLabel(mode)}`}
              >
                {on && <span className="mk-pl-seg-tick">✓</span>}
                <span className="mk-pl-seg-glyph">{stageGlyph(mode)}</span>
                <span className="mk-pl-seg-lbl">{stageLabel(mode)}</span>
              </button>
            </React.Fragment>
          );
        })}
      </div>

      <div className={`mk-pl-segcap${!standalone && !canConfirm ? ' is-empty' : ''}`}>
        {standalone
          ? ' '
          : canConfirm
            ? selected.map(stageLabel).join(' → ')
            : 'pick at least one stage'}
      </div>

      <div className="mk-pl-or"><span>or</span></div>

      {STANDALONE_OPTIONS.map((slug) => {
        const on = standalone === slug;
        return (
          <button
            key={slug}
            type="button"
            className={`mk-pl-largeplan${on ? ' is-selected' : ''}`}
            onClick={() => selectStandalone(slug)}
            title={`Run the standalone ${stageLabel(slug)} agent (can't be chained)`}
          >
            <span className="mk-pl-largeplan-glyph">{stageGlyph(slug)}</span>
            <span className="mk-pl-largeplan-lbl">{stageLabel(slug)}</span>
            <span className="mk-pl-largeplan-note">{on ? 'selected' : 'standalone'}</span>
          </button>
        );
      })}

      <button
        type="button"
        className="mk-pl-procopt is-confirm"
        disabled={!canConfirm}
        onClick={() => onPick({ stages: selected })}
      >
        Confirm
      </button>

      {onCancel && (
        <button type="button" className="mk-pl-procopt is-cancel" onClick={onCancel}>
          Cancel
        </button>
      )}
    </div>
  );
}
