import React, { useState } from 'react';
import KanbanCard from './KanbanCard.jsx';

const EMPTY_COPY = {
  todo: 'drop a card here.',
  in_progress: 'doing nothing. (literally.)',
  needs_action: 'nothing needs a human. yet.',
  in_review: 'nothing in review. nice.',
  done: 'nothing shipped yet.',
  cancelled: 'no write-offs.',
};

export default function Board({ columns, cards, promptConfig, hideEmptyColumns, amLeader, onMoveCard, onOpenCard, onDispatchFromCard }) {
  const [dragKey, setDragKey] = useState(null);
  const [overCol, setOverCol] = useState(null);

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
        return (
          <div
            key={col.state}
            className={`mk-col ${overCol === col.state ? 'is-over' : ''}`}
            onDragOver={(e) => { e.preventDefault(); setOverCol(col.state); }}
            onDragLeave={() => setOverCol(null)}
            onDrop={(e) => {
              e.preventDefault();
              if (dragKey) onMoveCard(dragKey, col.state);
              setDragKey(null);
              setOverCol(null);
            }}
          >
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
                  amLeader={amLeader}
                  onDragStart={() => { if (!card.taken && !card.waitingForClaim) setDragKey(card.key); }}
                  onDragEnd={() => { setDragKey(null); setOverCol(null); }}
                  onOpen={() => onOpenCard(card)}
                  onDispatch={onDispatchFromCard}
                />
              ))}
              {colCards.length === 0 && (
                <div className="mk-col-empty">
                  {EMPTY_COPY[col.state] || 'drop a card here.'}
                </div>
              )}
            </div>
          </div>
        );
      })}
    </div>
  );
}
