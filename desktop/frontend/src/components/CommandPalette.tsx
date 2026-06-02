import React, { useState, useEffect, useRef } from 'react';
import Icon from './Icon';

export default function CommandPalette({ open, cards, onClose, onPick }) {
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
    ? cards.filter(c => (c.key + ' ' + c.title).toLowerCase().includes(q.toLowerCase())).slice(0, 6)
    : cards.slice(0, 6);

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
            <button key={c.key} className="mk-palette-item" onClick={() => { onPick(c); onClose(); }}>
              <span className={`mk-pill mk-status-${c.column}`}>{c.columnLabel}</span>
              <span className="mk-mono mk-palette-id">{c.key}</span>
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
