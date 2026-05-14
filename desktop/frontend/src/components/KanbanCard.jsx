import React from 'react';

export default function KanbanCard({ card, isDragging, onDragStart, onDragEnd, onOpen }) {
  return (
    <article
      className={`mk-card ${isDragging ? 'is-dragging' : ''} ${card.claude ? 'is-claude' : ''}`}
      draggable
      onDragStart={onDragStart}
      onDragEnd={onDragEnd}
      onClick={onOpen}
    >
      <div className="mk-card-top">
        <span className={`mk-pill mk-status-${card.column}`}>{card.columnLabel}</span>
        <span className="mk-card-id">{card.key}</span>
      </div>
      <h3 className="mk-card-title">{card.title}</h3>
      {card.tags && card.tags.length > 0 && (
        <div className="mk-tag-row">
          {card.tags.map(t => <span key={t} className="mk-tag">{t}</span>)}
        </div>
      )}
      {card.assignees.length > 0 && (
        <footer className="mk-card-foot">
          <div className="mk-avatars">
            {card.assignees.map((a, i) => (
              <span key={i} className={`mk-av ${a === 'claude' ? 'is-claude' : ''}`}>
                {a === 'claude' ? 'c' : a}
              </span>
            ))}
          </div>
        </footer>
      )}
    </article>
  );
}
