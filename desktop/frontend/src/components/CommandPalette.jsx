import React, { useState, useEffect, useRef } from 'react';
import Icon from './Icon.jsx';
import { cards as allCards, columnStatus } from '../data.js';

export default function CommandPalette({ open, onClose, onPick }) {
  const [q, setQ] = useState('');
  const inputRef = useRef(null);

  useEffect(() => {
    if (open) {
      setQ('');
      setTimeout(() => inputRef.current?.focus(), 50);
    }
  }, [open]);

  if (!open) return null;

  const filtered = q
    ? allCards.filter(c => (c.id + ' ' + c.title).toLowerCase().includes(q.toLowerCase())).slice(0, 6)
    : allCards.slice(0, 6);

  return (
    <>
      <div className="mk-scrim" onClick={onClose} />
      <div className="mk-palette" role="dialog">
        <div className="mk-palette-input-row">
          <Icon name="search" />
          <input
            ref={inputRef}
            className="mk-palette-input"
            placeholder="Jump to issue, board, or branch…"
            value={q}
            onChange={(e) => setQ(e.target.value)}
            onKeyDown={(e) => { if (e.key === 'Escape') onClose(); }}
          />
          <span className="mk-kbd">esc</span>
        </div>
        <div className="mk-palette-results">
          <div className="mk-palette-section">Issues</div>
          {filtered.map(c => (
            <button key={c.id} className="mk-palette-item" onClick={() => { onPick(c); onClose(); }}>
              <span className={`mk-pill mk-status-${columnStatus(c.column)}`}>{columnStatus(c.column).toUpperCase()}</span>
              <span className="mk-mono mk-palette-id">{c.id}</span>
              <span className="mk-palette-title">{c.title}</span>
              <span className="mk-kbd">↵</span>
            </button>
          ))}
          {filtered.length === 0 && <div className="mk-palette-empty">no matches.</div>}
        </div>
      </div>
    </>
  );
}
