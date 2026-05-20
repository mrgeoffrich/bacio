import React, { useState } from 'react';
import KanbanCard from './KanbanCard.jsx';
import QuestionModal from './QuestionModal.jsx';

export default function Board({ columns, cards, promptConfig, hideEmptyColumns, onMoveCard, onOpenCard, onDispatchFromCard, onCancelWaitingCard, onAfterQuestionResolved }) {
  const [dragKey, setDragKey] = useState(null);
  const [overCol, setOverCol] = useState(null);
  // BACI-53: the kanban card "? N" pill opens the shared
  // QuestionModal. State lives here (rather than per-card) so the
  // modal stays mounted across re-renders of the underlying card and
  // closes cleanly when the user submits/dismisses.
  const [activeQuestionId, setActiveQuestionId] = useState(null);

  // With the preference on, drop any column that has no cards. The
  // filter is derived from `cards`, which App refreshes on repo change
  // and on the poll — so the visible set recomputes on every refresh.
  const visibleColumns = hideEmptyColumns
    ? columns.filter(col => cards.some(c => c.column === col.state))
    : columns;

  // Everything hidden — a genuinely empty repo with the toggle on.
  // Show a placeholder rather than a blank board region.
  if (visibleColumns.length === 0) {
    return (
      <div className="mk-board mk-board-empty-wrap">
        <div className="mk-board-empty">No cards in this repo yet.</div>
      </div>
    );
  }

  return (
    <div className="mk-board">
      {visibleColumns.map(col => {
        const colCards = cards.filter(c => c.column === col.state);
        // BACI-77: an empty column collapses to a narrow vertical-title
        // strip instead of eating a full 304px slot. `hideEmptyColumns`
        // (which removes the column entirely) wins by construction —
        // `visibleColumns` has already filtered hidden columns out, so
        // by the time we reach here an empty column is always collapsed.
        const isCollapsed = colCards.length === 0;
        return (
          <div
            key={col.state}
            className={`mk-col ${isCollapsed ? 'is-collapsed' : ''} ${overCol === col.state ? 'is-over' : ''}`}
            onDragOver={(e) => { e.preventDefault(); setOverCol(col.state); }}
            onDragLeave={() => setOverCol(null)}
            onDrop={(e) => {
              e.preventDefault();
              if (dragKey) onMoveCard(dragKey, col.state);
              setDragKey(null);
              setOverCol(null);
            }}
          >
            {isCollapsed ? (
              <div className={`mk-col-collapsed-title mk-status-${col.state}`}>
                {col.label}
              </div>
            ) : (
              <>
                <header className="mk-col-head">
                  <span className={`mk-col-pill mk-status-${col.state}`}>{col.label}</span>
                  <span className="mk-col-count">{colCards.length}</span>
                </header>
                <div className="mk-col-body">
                  {colCards.map(card => (
                    <KanbanCard
                      key={card.key}
                      card={card}
                      promptConfig={promptConfig}
                      isDragging={dragKey === card.key}
                      onDragStart={() => { if (!card.taken && !card.waitingForClaim) setDragKey(card.key); }}
                      onDragEnd={() => { setDragKey(null); setOverCol(null); }}
                      onOpen={() => onOpenCard(card)}
                      onDispatch={onDispatchFromCard}
                      onCancelWaiting={onCancelWaitingCard}
                      onOpenQuestion={(id) => setActiveQuestionId(id)}
                    />
                  ))}
                </div>
              </>
            )}
          </div>
        );
      })}
      <QuestionModal
        questionId={activeQuestionId}
        onClose={() => {
          setActiveQuestionId(null);
          if (onAfterQuestionResolved) onAfterQuestionResolved();
        }}
      />
    </div>
  );
}
