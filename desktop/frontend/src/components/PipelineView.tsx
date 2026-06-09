import { useState, useMemo } from 'react';
import { AnimatePresence, m } from 'motion/react';
import Icon from './Icon';
import Tooltip from './Tooltip';
import QuestionModal from './QuestionModal';
import PipelineCard from './pipeline/PipelineCard';
import StageCard from './pipeline/StageCard';
import { usePipelinePreferences } from './pipeline/usePipelinePreferences';
import { useDragDropLogic } from './pipeline/useDragDropLogic';
import type { BoardCard } from '../api';
import { useActiveRepo } from '../state/RepoProvider';
import { useCards } from '../state/CardsProvider';

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
//
// BACI-362 (Phase 4a) decomposed the card/menu/hook internals into
// components/pipeline/*; this file is the shell that wires the providers to
// the drag/drop + preferences hooks and lays out the three columns. The drag
// model: dragging a card to another column changes its state (or the Ship
// hand-off for in_pipeline → Shipping); dropping onto a card inside the same
// Backlog / Shipping column reorders it. The engine owns job progression —
// the Start / Stop / Auto controls only nudge it.

// BACI-361: PipelineView reads its cards + the ~20 mutation handlers from
// useCards(), and its active repo + the open/edit nav helpers from
// useActiveRepo(). Only the shell-owned "open composer" overlay control stays
// a prop.
type PipelineViewProps = {
  onOpenComposer?: () => void;
};

export default function PipelineView({ onOpenComposer }: PipelineViewProps) {
  const {
    activeBoard,
    openCard: onOpenCard,
    openIssue: onOpenIssue,
    openProcessEditor: onEditProcess,
  } = useActiveRepo();
  const {
    cards,
    moveCard: onMoveCard,
    fastTrackCard: onFastTrack,
    cancelCardFromPipeline: onCancelCard,
    doneCardFromPipeline: onDoneCard,
    reorderPipelineCard: onReorder,
    setCardProcess: onSetProcess,
    setCardProcessAuto: onSetProcessAuto,
    resetCardProcess: onResetProcess,
    startCardJob: onStartJob,
    stopCardJob: onStopJob,
    rerunCardJob: onRerunJob,
    setCardEngineMode: onSetEngineMode,
    shipCardFromPipeline: onShip,
    markDoneCardFromPipeline: onMarkDone,
    setRepoAutoShip: onSetAutoShip,
    setBacklogCollapsed: onSetBacklogCollapsed,
    setImpactPrimary: onSetImpactPrimary,
    dispatchFromCard: onShipDispatch,
    cancelWaitingFromCard: onCancelWaiting,
    onBlockCard,
  } = useCards();
  const [activeQuestionId, setActiveQuestionId] = useState<number | null>(null);
  const [expanded, setExpanded] = useState(false);

  // The three per-repo Pipeline display toggles (BACI-357 optimistic flip +
  // persist + silent revert), each seeded from the backend GET on the active
  // board. `expanded` (the transient widen-to-grid toggle) stays shell-local
  // and is overridden by `collapsed`.
  const { collapsed, toggleCollapsed, autoShip, toggleAutoShip, impactPrimary, toggleImpactPrimary } =
    usePipelinePreferences(activeBoard, { onSetBacklogCollapsed, onSetAutoShip, onSetImpactPrimary });

  const list = useMemo(() => cards || [], [cards]);
  const backlog = useMemo(() => list.filter(c => c.column === 'todo'), [list]);
  const inPipeline = useMemo(() => list.filter(c => c.column === 'in_pipeline'), [list]);
  const shipping = useMemo(() => list.filter(c => c.column === 'to_be_shipped'), [list]);
  const cardByKey = useMemo(() => new Map(list.map((c): [string, BoardCard] => [c.key, c])), [list]);

  // The drag/drop behaviour — card move-vs-reorder, the BACI-342 drag-to-block
  // gesture, and the BACI-268 / BACI-330 trash / done terminal drop zones —
  // lives in useDragDropLogic, which owns the raw drag state and derives the
  // drop handlers from the current card lookup + the card mutation handlers.
  const {
    dragKey, setDragKey, dragOverCol, setDragOverCol,
    highlightKey, onHighlight,
    onBlockDragStart, onBlockDragEnd,
    blockTargetKind, dropOnCard, dropBlockOnCard,
    colDropProps, trashDropProps, doneDropProps,
  } = useDragDropLogic(cardByKey, { onMoveCard, onShip, onReorder, onCancelCard, onDoneCard, onBlockCard });

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
      {/* BACI-335: the rainbow gradient the fast-track zap's stroke paints
          with (CSS `stroke: url(#mk-zap-rainbow)`). Rendered once, zero-size
          and aria-hidden so it never affects layout or assistive tech. */}
      <svg width="0" height="0" aria-hidden="true" focusable="false" style={{ position: 'absolute' }}>
        <defs>
          <linearGradient id="mk-zap-rainbow" x1="0" y1="0" x2="1" y2="1">
            <stop offset="0%" stopColor="#FF5E5E" />
            <stop offset="25%" stopColor="#FFB14E" />
            <stop offset="50%" stopColor="#5ED66E" />
            <stop offset="75%" stopColor="#4EA8FF" />
            <stop offset="100%" stopColor="#B96EFF" />
          </linearGradient>
        </defs>
      </svg>
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
                    impactPrimary={impactPrimary}
                    isDragging={dragKey === card.key}
                    isHighlighted={card.key === highlightKey}
                    canBlock
                    blockKind={blockTargetKind(card)}
                    onBlockDragStart={onBlockDragStart}
                    onBlockDragEnd={onBlockDragEnd}
                    onBlockDrop={() => dropBlockOnCard(card)}
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
                  impactPrimary={impactPrimary}
                  isDragging={dragKey === card.key}
                  isHighlighted={card.key === highlightKey}
                  canBlock
                  blockKind={blockTargetKind(card)}
                  onBlockDragStart={onBlockDragStart}
                  onBlockDragEnd={onBlockDragEnd}
                  onBlockDrop={() => dropBlockOnCard(card)}
                  onOpen={() => onOpenCard?.(card)}
                  onOpenIssue={onOpenIssue}
                  onHighlight={onHighlight}
                  onDragStart={() => setDragKey(card.key)}
                  onDragEnd={() => { setDragKey(null); setDragOverCol(null); }}
                  onSetProcess={onSetProcess}
                  onSetProcessAuto={onSetProcessAuto}
                  onResetProcess={onResetProcess}
                  onEditProcess={onEditProcess}
                  onStartJob={onStartJob}
                  onStopJob={onStopJob}
                  onRerunJob={onRerunJob}
                  onSetEngineMode={onSetEngineMode}
                  onShip={onShip}
                  onMarkDone={onMarkDone}
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
          <label className="mk-pl-toggle" title="Show each card's customer impact as the headline (title becomes a subtitle)">
            Impact first
            <button
              type="button"
              role="switch"
              aria-checked={impactPrimary}
              className={`mk-pl-switch${impactPrimary ? ' is-on' : ''}`}
              onClick={toggleImpactPrimary}
            />
          </label>
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
                impactPrimary={impactPrimary}
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

      {/* ── Drop tray (BACI-330) — the two terminal-outcome drop zones,
          stacked bottom-right of the pipeline area. Only mounted while a
          card is being dragged (dragKey != null), so there's no always-on
          chrome. Mark Done (green) sits above Cancel (red); each lights up
          with the shared `is-drop` highlight on drag-over via its own
          `dragOverCol` sentinel. Drop fires before dragend clears dragKey,
          so the zone is still mounted when the drop lands. ── */}
      {dragKey != null && (
        <div className="mk-pl-droptray">
          <div
            className={`mk-pl-dropzone is-done${dragOverCol === 'done' ? ' is-drop' : ''}`}
            aria-label="Mark done (drop here)"
            {...doneDropProps()}
          >
            <Icon name="check" />
            <Tooltip label="Close as done without shipping">
              <span className="mk-pl-dropzone-lbl">Mark Done</span>
            </Tooltip>
          </div>
          <div
            className={`mk-pl-dropzone is-cancel${dragOverCol === 'trash' ? ' is-drop' : ''}`}
            aria-label="Cancel issue (drop here)"
            {...trashDropProps()}
          >
            <Icon name="trash" />
            <span className="mk-pl-dropzone-lbl">Cancel</span>
          </div>
        </div>
      )}

      <QuestionModal
        questionId={activeQuestionId}
        onClose={() => setActiveQuestionId(null)}
      />
    </div>
  );
}
