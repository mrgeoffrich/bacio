import React from 'react';

// One- or two-letter badge for an avatar: initials for multi-word
// names, otherwise the first two characters.
function initials(name) {
  const parts = name.trim().split(/\s+/).filter(Boolean);
  if (parts.length >= 2) return (parts[0][0] + parts[1][0]).toUpperCase();
  return name.slice(0, 2).toUpperCase();
}

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
                {a === 'claude' ? 'c' : initials(a)}
              </span>
            ))}
          </div>
        </footer>
      )}
    </article>
  );
}
