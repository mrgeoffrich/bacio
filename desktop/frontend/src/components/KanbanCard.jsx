import React, { useState, useRef, useEffect } from 'react';
import Icon from './Icon.jsx';

export default function KanbanCard({ card, promptConfig, isDragging, amLeader, onDragStart, onDragEnd, onOpen, onDispatch }) {
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

  // A taken card is held by an agent — block the human from dragging it
  // or dispatching from it until the claim is released. Opening the
  // read-only drawer stays allowed (viewing isn't a mutation).
  const taken = !!card.taken;
  // A waiting card has a dispatch queued but no agent claim yet — the
  // gap this feature closes. Show a spinner, block drag/dispatch.
  // `taken` wins: once an agent claims, waiting_for_claim is cleared, so
  // they shouldn't overlap, but render defensively if they do.
  const waiting = !!card.waitingForClaim && !taken;
  // On a standby process (amLeader=false), dispatch is also disabled —
  // only the leader runs the auto-pick so two processes don't race.
  const dispatchDisabled = taken || waiting || !amLeader;

  const hasFooter = validPrompts.length > 0 || card.assignees.length > 0 || waiting;

  return (
    <article
      className={`mk-card ${isDragging ? 'is-dragging' : ''} ${card.claude ? 'is-claude' : ''} ${taken ? 'is-taken' : ''} ${waiting ? 'is-waiting' : ''}`}
      draggable={!taken && !waiting}
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
          {card.assignees.length > 0 && (
            <span
              className={`mk-card-assignee ${card.claude ? 'is-claude' : ''}`}
              title={card.assignees.join(', ')}
            >
              {card.assignees.join(', ')}
            </span>
          )}
          {waiting ? (
            <span
              className="mk-card-spinner"
              role="status"
              aria-label="Waiting for an agent to claim this issue"
              title="Waiting for an agent to claim this issue"
            />
          ) : validPrompts.length > 0 && (
            <div className="mk-card-action" ref={actionRef}>
              <button
                className="mk-card-action-btn"
                aria-label={taken ? 'An agent is working on this issue' : !amLeader ? 'Standby — another window has control' : 'Dispatch a prompt'}
                aria-haspopup="menu"
                aria-expanded={menuOpen}
                disabled={dispatchDisabled}
                title={taken ? 'An agent is working on this issue' : !amLeader ? 'Standby — another window controls automated dispatch' : undefined}
                onClick={(e) => {
                  e.stopPropagation();
                  if (dispatchDisabled) return;
                  setMenuOpen(o => !o);
                }}
              >
                <Icon name="zap" />
              </button>
              {menuOpen && !dispatchDisabled && (
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
