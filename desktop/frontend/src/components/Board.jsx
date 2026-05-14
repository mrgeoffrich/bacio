import React, { useState } from 'react';
import Icon from './Icon.jsx';
import KanbanCard from './KanbanCard.jsx';
import { columns } from '../data.js';

export default function Board({ cards, onMoveCard, onOpenCard }) {
  const [dragId, setDragId] = useState(null);
  const [overCol, setOverCol] = useState(null);

  return (
    <div className="mk-board">
      {columns.map(col => {
        const colCards = cards.filter(c => c.column === col.id);
        return (
          <div
            key={col.id}
            className={`mk-col ${overCol === col.id ? 'is-over' : ''}`}
            onDragOver={(e) => { e.preventDefault(); setOverCol(col.id); }}
            onDragLeave={() => setOverCol(null)}
            onDrop={(e) => {
              e.preventDefault();
              if (dragId) onMoveCard(dragId, col.id);
              setDragId(null);
              setOverCol(null);
            }}
          >
            <header className="mk-col-head">
              <span className={`mk-col-pill mk-status-${col.status}`}>{col.name}</span>
              <span className="mk-col-count">{colCards.length}</span>
              <button className="mk-icbtn mk-col-add" aria-label="Add card"><Icon name="plus" /></button>
            </header>
            <div className="mk-col-body">
              {colCards.map(card => (
                <KanbanCard
                  key={card.id}
                  card={card}
                  isDragging={dragId === card.id}
                  onDragStart={() => setDragId(card.id)}
                  onDragEnd={() => { setDragId(null); setOverCol(null); }}
                  onOpen={() => onOpenCard(card)}
                />
              ))}
              {colCards.length === 0 && (
                <div className="mk-col-empty">
                  {col.id === 'blocked' ? 'no blockers. for now.'
                  : col.id === 'doing'  ? 'doing nothing. (literally.)'
                  : col.id === 'review' ? 'nothing in review. nice.'
                  : 'drop a card here.'}
                </div>
              )}
            </div>
          </div>
        );
      })}
    </div>
  );
}
