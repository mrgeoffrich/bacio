import React, { useState, useMemo, useEffect, useCallback } from 'react';
import { AnimatePresence, m } from 'motion/react';
import { Link } from 'react-router';
import Icon from './Icon.jsx';
import Tooltip from './Tooltip.jsx';
import QuestionModal from './QuestionModal.jsx';
import ShippedPopover from './ShippedPopover.jsx';
import { documentPath } from '../lib/routes';
import prLabel from '../lib/prLabel';
import { PIPELINE_PROCESSES, stageLabel, isShipStage } from '../lib/pipelineProcesses';

// PipelineView (Phase 4) — the real three-column pipeline board, keyed on
// the server-side issue states: Backlog (todo) → In Pipeline
// (in_pipeline) → Shipping (to_be_shipped). Replaces the BACI mock
// (local placement Map) with persisted state: a card's column IS its
// issue state, ordering is server-side, and the per-card job chain +
// engine drive-mode come straight off the BoardCard.
//
// Card design follows pipeline-card-mockups.html (authoritative): a
// compact issue card in Backlog / Shipping, and a stage card that grows
// to fill the column when in pipeline — issue header on top, the
// processing detail (job chain + active-job todos / question) in the
// body, all operation controls along the bottom.
//
// Drag model: dragging a card to another column changes its state
// (onMoveCard, or the Ship hand-off for in_pipeline → Shipping); dropping
// onto a card inside the same Backlog / Shipping column reorders it
// (onReorder, 1-based position). The engine owns job progression — the
// Start / Stop / Auto controls only nudge it.

// AUTOSHIP_KEY: per-repo localStorage map for the Shipping auto-ship
// toggle display state. The backend exposes only a PUT (no GET), so the
// switch is seeded from here and the PUT keeps the DB — the real source
// the controller's auto-ship ticker reads — in sync. Same localStorage
// pattern as the board-scroll / pinned-keys / shipped-scope prefs.
const AUTOSHIP_KEY = 'bacio-autoship';
function readAutoShip(repo) {
  if (!repo) return false;
  try {
    const raw = localStorage.getItem(AUTOSHIP_KEY);
    const map = raw ? JSON.parse(raw) : {};
    return !!(map && map[repo]);
  } catch {
    return false;
  }
}
function persistAutoShip(repo, enabled) {
  if (!repo) return;
  try {
    const raw = localStorage.getItem(AUTOSHIP_KEY);
    const map = raw ? JSON.parse(raw) : {};
    map[repo] = !!enabled;
    localStorage.setItem(AUTOSHIP_KEY, JSON.stringify(map));
  } catch {
    /* non-fatal — toggle just won't survive a relaunch */
  }
}

export default function PipelineView({
  cards,
  activeBoard,
  onOpenCard,
  onOpenIssue,
  onMoveCard,
  onReorder,
  onSetProcess,
  onStartJob,
  onStopJob,
  onSetEngineMode,
  onShip,
  onSetAutoShip,
  onShipDispatch,
  onCancelWaiting,
  shippedCount,
  shippedScope,
  onShippedScopeChange,
}) {
  const [activeQuestionId, setActiveQuestionId] = useState(null);
  const [expanded, setExpanded] = useState(false);
  const [dragKey, setDragKey] = useState(null);
  const [dragOverCol, setDragOverCol] = useState(null);
  const [autoShip, setAutoShip] = useState(() => readAutoShip(activeBoard));

  useEffect(() => {
    setAutoShip(readAutoShip(activeBoard));
  }, [activeBoard]);

  const list = cards || [];
  const backlog = useMemo(() => list.filter(c => c.column === 'todo'), [list]);
  const inPipeline = useMemo(() => list.filter(c => c.column === 'in_pipeline'), [list]);
  const shipping = useMemo(() => list.filter(c => c.column === 'to_be_shipped'), [list]);
  const cardByKey = useMemo(() => new Map(list.map(c => [c.key, c])), [list]);

  const toggleAutoShip = useCallback(() => {
    const next = !autoShip;
    setAutoShip(next);
    persistAutoShip(activeBoard, next);
    Promise.resolve(onSetAutoShip?.(next)).catch(() => {
      // Revert the optimistic flip if the persist failed.
      setAutoShip(!next);
      persistAutoShip(activeBoard, !next);
    });
  }, [autoShip, activeBoard, onSetAutoShip]);

  // Cross-column drop: change the dragged card's column (= its state).
  // in_pipeline → Shipping goes through the Ship hand-off; everything
  // else is a plain state move.
  const dropToColumn = (col) => {
    setDragOverCol(null);
    const key = dragKey;
    setDragKey(null);
    if (!key) return;
    const card = cardByKey.get(key);
    if (!card || card.column === col) return;
    if (col === 'to_be_shipped' && card.column === 'in_pipeline') {
      onShip?.(key);
    } else {
      onMoveCard?.(key, col);
    }
  };

  // Within-column reorder: dropping onto a card in the same Backlog /
  // Shipping list moves the dragged card to that card's 1-based slot.
  const dropOnCard = (targetCard, index) => {
    const key = dragKey;
    setDragKey(null);
    setDragOverCol(null);
    if (!key || key === targetCard.key) return;
    const dragged = cardByKey.get(key);
    if (!dragged || dragged.column !== targetCard.column) return;
    onReorder?.(key, index + 1);
  };

  const colDropProps = (col) => ({
    onDragOver: (e) => { e.preventDefault(); setDragOverCol(col); },
    onDragLeave: (e) => { if (e.currentTarget === e.target) setDragOverCol(null); },
    onDrop: (e) => { e.preventDefault(); dropToColumn(col); },
  });

  if (!activeBoard) {
    return (
      <div className="mk-pl">
        <div className="mk-pl-empty">Select a repository to view its pipeline.</div>
      </div>
    );
  }

  return (
    <div className={`mk-pl${expanded ? ' is-backlog-expanded' : ''}`}>
      {/* ── Backlog ── */}
      <section
        className={`mk-pl-col mk-pl-backlog${dragOverCol === 'todo' ? ' is-drop' : ''}`}
        {...colDropProps('todo')}
      >
        <header className="mk-pl-col-head">
          <span className="mk-pl-pill is-todo">Backlog</span>
          <span className="mk-pl-count">{backlog.length}</span>
          <button
            type="button"
            className="mk-pl-drawer-toggle"
            onClick={() => setExpanded(e => !e)}
            aria-label={expanded ? 'Collapse backlog' : 'Expand backlog'}
            aria-expanded={expanded}
          >
            {expanded ? '«' : '»'}
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
                isDragging={dragKey === card.key}
                onOpen={() => onOpenCard?.(card)}
                onDragStart={() => setDragKey(card.key)}
                onDragEnd={() => { setDragKey(null); setDragOverCol(null); }}
                onDropCard={() => dropOnCard(card, i)}
              />
            ))
          )}
        </div>
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
                  isDragging={dragKey === card.key}
                  onOpen={() => onOpenCard?.(card)}
                  onDragStart={() => setDragKey(card.key)}
                  onDragEnd={() => { setDragKey(null); setDragOverCol(null); }}
                  onSetProcess={onSetProcess}
                  onStartJob={onStartJob}
                  onStopJob={onStopJob}
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
            onOpenIssue={onOpenIssue}
          />
        </div>
        <div className="mk-pl-col-body">
          {shipping.length === 0 ? (
            <div className="mk-pl-col-empty">Nothing to ship</div>
          ) : (
            shipping.map((card, i) => (
              <PipelineCard
                key={card.key}
                card={card}
                index={i}
                showBadge
                shipping
                isNextToShip={i === 0}
                autoShip={autoShip}
                isDragging={dragKey === card.key}
                onOpen={() => onOpenCard?.(card)}
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

      <QuestionModal
        questionId={activeQuestionId}
        onClose={() => setActiveQuestionId(null)}
      />
    </div>
  );
}

// PipelineCard — the compact issue card for Backlog and Shipping. Issue
// info only (feature glyph, key, plan/PR icon buttons, title, labels),
// per the mockup's "the issue card only ever shows the issue itself"
// rule. Shipping adds a position badge + the Next-to-ship SHIP row /
// waiting status.
function PipelineCard({
  card,
  index,
  showBadge,
  shipping,
  isNextToShip,
  autoShip,
  isDragging,
  onOpen,
  onDragStart,
  onDragEnd,
  onDropCard,
  onShipDispatch,
  onCancelWaiting,
}) {
  const [over, setOver] = useState(false);
  const waiting = !!card.waitingState && !card.taken;
  const shippingInFlight = shipping && (card.taken || waiting);

  return (
    <article
      className={`mk-pl-card${isDragging ? ' is-dragging' : ''}${over ? ' is-drop-before' : ''}${shippingInFlight ? ' is-shipping' : ''}`}
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
      <CardHead card={card} />
      <h3 className="mk-pl-card-title">{card.title}</h3>
      <CardLabels tags={card.tags} />
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

// CardHead — feature glyph · issue key · plan / PR icon buttons (each
// only when it exists). Shared by the compact card and the stage card's
// header so the anatomy stays identical.
function CardHead({ card }) {
  const latestPlan = card.latestPlan || null;
  const latestPR = card.latestPR || null;
  return (
    <div className="mk-pl-card-top">
      {card.featureEmoji && (
        <span className="mk-pl-card-emoji" aria-hidden="true">{card.featureEmoji}</span>
      )}
      <span className="mk-pl-card-id">{card.key}</span>
      <span className="mk-pl-card-icons">
        {latestPlan && (
          <Tooltip label={`Open plan: ${latestPlan.filename}`}>
            <Link
              to={documentPath(latestPlan.filename)}
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
  isDragging,
  onOpen,
  onDragStart,
  onDragEnd,
  onSetProcess,
  onStartJob,
  onStopJob,
  onSetEngineMode,
  onShip,
  onOpenQuestion,
}) {
  const [picking, setPicking] = useState(false);

  const jobs = card.jobs || [];
  const hasProcess = jobs.length > 0;
  const running = jobs.find(j => j.status === 'running') || null;
  const nonShip = jobs.filter(j => !isShipStage(j.mode));
  const allDone = nonShip.length > 0 && !running &&
    nonShip.every(j => j.status === 'complete' || j.status === 'cancelled');
  const nextPending = jobs.find(j => j.status === 'pending');
  const engineAuto = card.engineMode === 'auto';
  const question = (card.openQuestions || [])[0] || null;
  const paused = card.enginePauseReason === 'open_question' || !!question;

  const showProcessMenu = picking || !hasProcess;

  return (
    <article
      className={`mk-pl-stage${isDragging ? ' is-dragging' : ''}${paused ? ' is-attn' : ''}`}
      draggable
      onDragStart={onDragStart}
      onDragEnd={onDragEnd}
    >
      <header className="mk-pl-stage-head" onClick={onOpen}>
        <CardHead card={card} />
        <h3 className="mk-pl-stage-title">{card.title}</h3>
        <CardLabels tags={card.tags} />
      </header>

      {showProcessMenu ? (
        <ProcessMenu
          dimmedHasProcess={hasProcess}
          onPick={(slug) => { setPicking(false); onSetProcess?.(card.key, slug); }}
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
            ) : running ? (
              <ActiveJob card={card} job={running} />
            ) : (
              <div className="mk-pl-proc-empty">
                Process selected. Press <b>Start</b> to run the first job, or flip
                {' '}<b>Auto</b> to run them through.
              </div>
            )}
          </div>
          <footer className="mk-pl-stage-foot">
            {running ? (
              <button
                type="button"
                className="mk-pl-btn is-ghost is-danger is-sm"
                onClick={() => onStopJob?.(card.key)}
              >
                ■ Stop
              </button>
            ) : (
              <button
                type="button"
                className="mk-pl-btn is-primary is-sm"
                disabled={!nextPending || allDone}
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
              onClick={() => setPicking(true)}
              title="Edit / replace the process"
            >
              ✎ Edit
            </button>
            <span className="mk-pl-spacer" />
            {paused && <span className="mk-pl-halt">⏸ Auto halted</span>}
            <button
              type="button"
              className={`mk-pl-btn is-sm${allDone ? ' is-primary' : ' is-ghost'}`}
              disabled={!allDone}
              onClick={() => onShip?.(card.key)}
            >
              ⏏ Ship
            </button>
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
  return (
    <div className="mk-pl-job">
      <div className="mk-pl-job-head">
        <span className="mk-pl-jmode">{stageLabel(job.mode)}</span>
        <span className="mk-pl-jmeta">
          <span className="mk-pl-live">running</span>
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

// ProcessMenu — the "pick a process" overlay shown over a freshly-entered
// card (no chain yet) or when editing. The issue card sits dimmed under
// the scrim.
function ProcessMenu({ dimmedHasProcess, onPick, onCancel }) {
  return (
    <div className="mk-pl-procmenu">
      <div className="mk-pl-procmenu-title">
        {dimmedHasProcess ? 'Replace the process' : 'Pick a process'}
      </div>
      {PIPELINE_PROCESSES.map(p => (
        <button
          key={p.slug}
          type="button"
          className="mk-pl-procopt"
          onClick={() => onPick(p.slug)}
        >
          <span>{p.name}</span>
          <span className="mk-pl-procopt-seq">
            {p.stages.map((s, i) => (
              <span key={i} className={`mk-pl-chip is-${isShipStage(s) ? 'ship' : s}`}>
                {stageLabel(s).charAt(0)}
              </span>
            ))}
          </span>
        </button>
      ))}
      {onCancel && (
        <button type="button" className="mk-pl-procopt is-cancel" onClick={onCancel}>
          Cancel
        </button>
      )}
    </div>
  );
}
