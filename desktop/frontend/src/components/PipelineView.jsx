import React, { useState, useMemo, useEffect } from 'react';
import { AnimatePresence, m } from 'motion/react';
import KanbanCard from './KanbanCard.jsx';
import QuestionModal from './QuestionModal.jsx';
import ShippedPopover from './ShippedPopover.jsx';

// Order the zap button walks a card through. to_be_shipped is terminal.
const NEXT_STAGE = { todo: 'in_pipeline', in_pipeline: 'to_be_shipped' };

export default function PipelineView({
  cards,
  activeBoard,
  promptConfig,
  onOpenCard,
  onOpenIssue,
  onDispatch,
  onDispatchChain,
  onCancelWaiting,
  onQuickEval,
  onSetFollowOn,
  onCancelFollowOn,
  shippedCount,
  shippedScope,
  onShippedScopeChange,
}) {
  const [activeQuestionId, setActiveQuestionId] = useState(null);
  const [expanded, setExpanded] = useState(false);
  // UI-only column overrides keyed by issue key. There is no backend
  // in_pipeline / to_be_shipped state yet, so drag placement lives here
  // and is lost on unmount — a front-end mock of the drag interaction.
  const [placement, setPlacement] = useState(() => new Map());
  const [dragKey, setDragKey] = useState(null);
  const [dragOverZone, setDragOverZone] = useState(null);
  // A manual drag should NOT animate — the card lands where you drop it.
  // The flight is reserved for system-driven moves (a future backend
  // state change). noAnimKey holds the key just placed by a drag so that
  // single move renders with a zero-duration transition; we clear it on
  // the next frame so any later (non-drag) move of the same card animates
  // normally again.
  const [noAnimKey, setNoAnimKey] = useState(null);
  useEffect(() => {
    if (noAnimKey == null) return undefined;
    const id = requestAnimationFrame(() => setNoAnimKey(null));
    return () => cancelAnimationFrame(id);
  }, [noAnimKey]);

  const cardsByKey = useMemo(
    () => new Map((cards || []).map(c => [c.key, c])),
    [cards],
  );
  const colOf = (card) => placement.get(card.key) ?? card.column;
  const todos = (cards || []).filter(c => colOf(c) === 'todo');
  const inPipeline = (cards || []).filter(c => colOf(c) === 'in_pipeline');
  const toBeShipped = (cards || []).filter(c => colOf(c) === 'to_be_shipped');

  const renderCard = (card) => (
    <KanbanCard
      key={card.key}
      card={card}
      cardsByKey={cardsByKey}
      promptConfig={promptConfig}
      isDragging={dragKey === card.key}
      compact={false}
      onDragStart={() => setDragKey(card.key)}
      onDragEnd={() => setDragKey(null)}
      onOpen={() => onOpenCard(card)}
      onDispatch={onDispatch}
      onDispatchChain={onDispatchChain}
      onCancelWaiting={onCancelWaiting}
      onOpenQuestion={(id) => setActiveQuestionId(id)}
      onOpenIssue={onOpenIssue}
      onQuickEval={onQuickEval}
      onSetFollowOn={onSetFollowOn}
      onCancelFollowOn={onCancelFollowOn}
      isTrayHover={false}
      isJumping={false}
      layoutEase="easeInOut"
      skipAnimation={card.key === noAnimKey}
      onZap={advanceCard}
    />
  );

  const renderColumn = (list, emptyLabel) => {
    if (!activeBoard) {
      return <div className="mk-pipeline-empty">No repository selected</div>;
    }
    if (list.length === 0) {
      return <div className="mk-pipeline-empty">{emptyLabel}</div>;
    }
    return (
      <div className="mk-pipeline-cards">
        {/* AnimatePresence keeps a moved card's source instance mounted
            until its exit finishes, so the layoutId shared transition has an
            origin to fly from. mode="sync" (not popLayout): popLayout pinned
            the exiting twin in place and Motion animated the entering card
            toward it (a backwards leg); sync keeps the twin in flow as it
            fades and lets the entering card promote cleanly. */}
        <AnimatePresence mode="sync" initial={false}>
          {list.map(card => renderCard(card))}
        </AnimatePresence>
      </div>
    );
  };

  // placeInto records the dragged card's new column locally and clears
  // the drag state. dropZone wires the three sections as drop targets.
  const placeInto = (status) => {
    if (dragKey) {
      // Mark this key so its move renders without animation — a drop is
      // direct manipulation, not a system transition.
      setNoAnimKey(dragKey);
      setPlacement(prev => {
        const next = new Map(prev);
        next.set(dragKey, status);
        return next;
      });
    }
    setDragKey(null);
    setDragOverZone(null);
  };
  // advanceCard is the zap-button action: walk the card one stage forward
  // (todo → in_pipeline → to_be_shipped). Unlike a drag this does NOT set
  // noAnimKey, so the move animates — the "system move" path.
  const advanceCard = (key) => {
    const card = cardsByKey.get(key);
    if (!card) return;
    const next = NEXT_STAGE[colOf(card)];
    if (!next) return;
    setPlacement(prev => {
      const map = new Map(prev);
      map.set(key, next);
      return map;
    });
  };
  const dropZone = (status) => ({
    onDragOver: (e) => { e.preventDefault(); setDragOverZone(status); },
    onDragLeave: (e) => { if (e.currentTarget === e.target) setDragOverZone(null); },
    onDrop: (e) => { e.preventDefault(); placeInto(status); },
  });

  return (
    <div className="mk-pipeline">
      <aside
        className={`mk-pipeline-panel${expanded ? ' is-expanded' : ''}${dragOverZone === 'todo' ? ' is-drop-target' : ''}`}
        {...dropZone('todo')}
      >
        {renderColumn(todos, 'No todo items')}
        <button
          type="button"
          className="mk-pipeline-toggle"
          onClick={() => setExpanded(e => !e)}
          aria-label={expanded ? 'Collapse panel' : 'Expand panel'}
          aria-expanded={expanded}
        >
          {expanded ? '«' : '»'}
        </button>
      </aside>

      <section
        className={`mk-pipeline-stage${dragOverZone === 'in_pipeline' ? ' is-drop-target' : ''}`}
        {...dropZone('in_pipeline')}
      >
        {/* Empty hint and the grid coexist: the grid (and its
            AnimatePresence) stays mounted from first paint even when empty,
            so the FIRST card dropped/zapped here is an *added* child (it
            animates) rather than an initial child that initial={false}
            would suppress. */}
        {inPipeline.length === 0 && (
          <div className="mk-pipeline-stage-empty">
            Drag a card here to run it through the pipeline
          </div>
        )}
        <div className="mk-pipeline-stage-grid">
          {/* The stage-card wrapper is a motion element so AnimatePresence
              can keep it mounted through its exit. It fades in (initial/
              animate opacity) so the big tile doesn't pop in at the
              destination while the inner KanbanCard's layoutId flies in. */}
          <AnimatePresence mode="sync" initial={false}>
            {inPipeline.map(card => (
              <m.article
                key={card.key}
                layout
                initial={{ opacity: 0 }}
                animate={{ opacity: 1 }}
                exit={{ opacity: 0 }}
                transition={{ duration: 0.25 }}
                className="mk-pipeline-stage-card"
              >
                <div className="mk-pipeline-stage-card-issue">
                  {renderCard(card)}
                </div>
                <div className="mk-pipeline-stage-card-info">
                  <span className="mk-pipeline-stage-card-info-label">Processing</span>
                </div>
              </m.article>
            ))}
          </AnimatePresence>
        </div>
      </section>

      <aside
        className={`mk-pipeline-panel${dragOverZone === 'to_be_shipped' ? ' is-drop-target' : ''}`}
        {...dropZone('to_be_shipped')}
      >
        <header className="mk-pipeline-col-head">
          <ShippedPopover
            activeBoard={activeBoard}
            shippedCount={shippedCount}
            scope={shippedScope}
            onScopeChange={onShippedScopeChange}
            onOpenIssue={onOpenIssue}
          />
        </header>
        {renderColumn(toBeShipped, 'Nothing to ship')}
      </aside>
      <QuestionModal
        questionId={activeQuestionId}
        onClose={() => setActiveQuestionId(null)}
      />
    </div>
  );
}
