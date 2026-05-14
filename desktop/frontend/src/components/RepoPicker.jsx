import React, { useState, useRef, useEffect } from 'react';
import Icon from './Icon.jsx';

// RepoPicker is the topbar's repository selector — a searchable dropdown that
// replaces the plain native <select>. Clicking the trigger opens a menu with
// a filter input, the matching repos, and an "Add Repository" action that
// opens a native folder picker so bacio can register a new git working tree.
export default function RepoPicker({ boards, activeBoard, onPick, onAddRepository }) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState('');
  const rootRef = useRef(null);
  const inputRef = useRef(null);

  const active = boards.find(b => b.prefix === activeBoard);
  const label = active?.name || activeBoard || 'Select repository';

  // While open: focus the filter, and close on Escape or an outside click.
  useEffect(() => {
    if (!open) {
      setQuery('');
      return;
    }
    inputRef.current?.focus();
    const onDown = (e) => {
      if (rootRef.current && !rootRef.current.contains(e.target)) setOpen(false);
    };
    const onKey = (e) => { if (e.key === 'Escape') setOpen(false); };
    document.addEventListener('mousedown', onDown);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onDown);
      document.removeEventListener('keydown', onKey);
    };
  }, [open]);

  const q = query.trim().toLowerCase();
  const filtered = q
    ? boards.filter(b =>
        b.name.toLowerCase().includes(q) || b.prefix.toLowerCase().includes(q))
    : boards;

  const pick = (prefix) => {
    onPick(prefix);
    setOpen(false);
  };

  const add = () => {
    setOpen(false);
    onAddRepository();
  };

  return (
    <div className="mk-repo-picker" ref={rootRef}>
      <button
        className="mk-repo-picker-trigger"
        onClick={() => setOpen(o => !o)}
        aria-haspopup="listbox"
        aria-expanded={open}
      >
        <Icon name="branch" />
        <span className="mk-repo-picker-label">{label}</span>
        {active && (
          <>
            <span className="mk-repo-picker-sep">·</span>
            <span className="mk-repo-picker-code">{active.prefix}</span>
          </>
        )}
      </button>

      {open && (
        <div className="mk-repo-picker-menu" role="listbox">
          <input
            ref={inputRef}
            className="mk-repo-picker-search"
            type="text"
            placeholder="Search repositories…"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
          />
          <div className="mk-repo-picker-list">
            {filtered.length === 0 ? (
              <div className="mk-repo-picker-empty">No matching repositories.</div>
            ) : (
              filtered.map(b => (
                <button
                  key={b.prefix}
                  className={`mk-repo-picker-item ${b.prefix === activeBoard ? 'is-active' : ''}`}
                  onClick={() => pick(b.prefix)}
                  role="option"
                  aria-selected={b.prefix === activeBoard}
                >
                  <span className="mk-repo-picker-item-name">{b.name}</span>
                  <span className="mk-repo-picker-item-prefix">{b.prefix}</span>
                </button>
              ))
            )}
          </div>
          <button className="mk-repo-picker-add" onClick={add}>
            <Icon name="plus" />
            Add Repository…
          </button>
        </div>
      )}
    </div>
  );
}
