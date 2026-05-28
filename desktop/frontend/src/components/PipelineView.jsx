import React, { useState, useMemo } from 'react';
import KanbanCard from './KanbanCard.jsx';
import QuestionModal from './QuestionModal.jsx';
import ShippedPopover from './ShippedPopover.jsx';

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
  const cardsByKey = useMemo(
    () => new Map((cards || []).map(c => [c.key, c])),
    [cards],
  );
  const todos = (cards || []).filter(c => c.column === 'todo');
  const toBeShipped = (cards || []).filter(c => c.column === 'to_be_shipped');

  const renderCards = (list, emptyLabel) => {
    if (!activeBoard) {
      return <div className="mk-pipeline-empty">No repository selected</div>;
    }
    if (list.length === 0) {
      return <div className="mk-pipeline-empty">{emptyLabel}</div>;
    }
    return (
      <div className="mk-pipeline-cards">
        {list.map(card => (
          <KanbanCard
            key={card.key}
            card={card}
            cardsByKey={cardsByKey}
            promptConfig={promptConfig}
            isDragging={false}
            compact={false}
            onDragStart={() => {}}
            onDragEnd={() => {}}
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
          />
        ))}
      </div>
    );
  };

  return (
    <div className="mk-pipeline">
      <aside className={`mk-pipeline-panel${expanded ? ' is-expanded' : ''}`}>
        {renderCards(todos, 'No todo items')}
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
      <aside className="mk-pipeline-panel">
        <header className="mk-pipeline-col-head">
          <ShippedPopover
            activeBoard={activeBoard}
            shippedCount={shippedCount}
            scope={shippedScope}
            onScopeChange={onShippedScopeChange}
            onOpenIssue={onOpenIssue}
          />
        </header>
        {renderCards(toBeShipped, 'Nothing to ship')}
      </aside>
      <QuestionModal
        questionId={activeQuestionId}
        onClose={() => setActiveQuestionId(null)}
      />
    </div>
  );
}
