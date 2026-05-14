import React from 'react';
import Icon from './Icon.jsx';
import { columnStatus } from '../data.js';

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
        <span className={`mk-pill mk-status-${columnStatus(card.column)}`}>
          {columnStatus(card.column).toUpperCase()}
        </span>
        {card.priority && <span className="mk-pill mk-priority">!</span>}
        <span className="mk-card-id">{card.id}</span>
      </div>
      <h3 className="mk-card-title">{card.title}</h3>
      {card.tags && card.tags.length > 0 && (
        <div className="mk-tag-row">
          {card.tags.map(t => <span key={t} className="mk-tag">{t}</span>)}
        </div>
      )}
      <footer className="mk-card-foot">
        <div className="mk-avatars">
          {card.assignees.map((a, i) => (
            <span key={i} className={`mk-av ${a === 'claude' ? 'is-claude' : ''}`}>
              {a === 'claude' ? 'c' : a}
            </span>
          ))}
        </div>
        {card.note && <span className="mk-card-note">{card.note}</span>}
        <div className="mk-card-meta">
          {card.comments && (
            <span className="mk-meta-row" title={`${card.comments} comments`}>
              <Icon name="comment" /> {card.comments}
            </span>
          )}
          {card.pr && (
            <span className="mk-meta-row" title={`PR ${card.pr}`}>
              <Icon name="pr" /> {card.pr}
            </span>
          )}
        </div>
      </footer>
    </article>
  );
}
