import React, { useState, useRef, useEffect } from 'react';
import Icon from './Icon.jsx';

// One- or two-letter badge for an avatar: initials for multi-word
// names, otherwise the first two characters. Falls back to '?' for
// empty / missing names rather than rendering a blank badge.
function initials(name) {
  if (!name) return '?';
  const parts = name.trim().split(/\s+/).filter(Boolean);
  if (parts.length >= 2) return (parts[0][0] + parts[1][0]).toUpperCase();
  return (parts[0] ?? '').slice(0, 2).toUpperCase() || '?';
}

export default function KanbanCard({ card, promptConfig, isDragging, onDragStart, onDragEnd, onOpen, onDispatch }) {
  // The prompts valid to dispatch from this card's current state — the
  // state-gate config is global (App-owned), filtered per-card here.
  const validPrompts = (promptConfig || []).filter(
    p => (p.allowedStates || []).includes(card.column),
  );

  const [menuOpen, setMenuOpen] = useState(false);
  const actionRef = useRef(null);

  // Close the prompt menu on an outside click or Escape — the menu is a
  // self-contained popover, so the card owns its own dismissal.
  useEffect(() => {
    if (!menuOpen) return undefined;
    const onDown = (e) => {
      if (actionRef.current && !actionRef.current.contains(e.target)) setMenuOpen(false);
    };
    const onKey = (e) => { if (e.key === 'Escape') setMenuOpen(false); };
    document.addEventListener('mousedown', onDown);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onDown);
      document.removeEventListener('keydown', onKey);
    };
  }, [menuOpen]);

  const pick = (e, mode) => {
    e.stopPropagation();
    setMenuOpen(false);
    onDispatch(card.key, mode);
  };

  const hasFooter = validPrompts.length > 0 || card.assignees.length > 0;

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
      {hasFooter && (
        <footer className="mk-card-foot">
          <div className="mk-avatars">
            {card.assignees.map((a, i) => (
              <span key={i} className={`mk-av ${a === 'claude' ? 'is-claude' : ''}`}>
                {a === 'claude' ? 'c' : initials(a)}
              </span>
            ))}
          </div>
          {validPrompts.length > 0 && (
            <div className="mk-card-action" ref={actionRef}>
              <button
                className="mk-card-action-btn"
                aria-label="Dispatch a prompt"
                aria-haspopup="menu"
                aria-expanded={menuOpen}
                onClick={(e) => { e.stopPropagation(); setMenuOpen(o => !o); }}
              >
                <Icon name="zap" />
              </button>
              {menuOpen && (
                <div className="mk-card-action-menu" role="menu">
                  {validPrompts.map(p => (
                    <button
                      key={p.mode}
                      className="mk-card-action-item"
                      role="menuitem"
                      onClick={(e) => pick(e, p.mode)}
                    >
                      {p.label}
                    </button>
                  ))}
                </div>
              )}
            </div>
          )}
        </footer>
      )}
    </article>
  );
}
