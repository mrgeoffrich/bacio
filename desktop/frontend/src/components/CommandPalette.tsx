import { useState, useEffect, useRef } from 'react';
import type { BoardCard } from '../api';
import Icon from './Icon';
import { useCards } from '../state/CardsProvider';

type CommandPaletteProps = {
  open: boolean;
  onClose: () => void;
  onPick: (card: BoardCard) => void;
};

// BACI-361: cards are read from useCards() rather than prop-drilled. The
// overlay flags (open) + the shell-owned pick/close handlers stay props.
//
// BACI-366 a11y: the palette is the WAI-ARIA combobox+listbox pattern. Focus
// stays on the input; Up/Down move a highlighted option via aria-activedescendant
// (so a screen reader announces the active row without the focus leaving the
// text field), Enter picks it, Escape closes. The results carry role="option" /
// aria-selected so assistive tech reads the list as a single-select listbox.
export default function CommandPalette({ open, onClose, onPick }: CommandPaletteProps) {
  const { cards } = useCards();
  const [q, setQ] = useState('');
  // active is the keyboard-highlighted result index (the activedescendant).
  // Reset to the top on open and on every query change so it never points past
  // the filtered list.
  const [active, setActive] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (open) {
      setQ('');
      setActive(0);
      setTimeout(() => inputRef.current?.focus(), 50);
    }
  }, [open]);

  if (!open) return null;

  const filtered = q
    ? cards.filter(c => (c.key + ' ' + c.title).toLowerCase().includes(q.toLowerCase())).slice(0, 6)
    : cards.slice(0, 6);

  const pick = (card: BoardCard) => { onPick(card); onClose(); };

  // All keys are handled on the input — focus never leaves it (the
  // activedescendant pattern), so arrows / Enter / Escape route here whether
  // the user is typing or navigating results.
  const onKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Escape') { onClose(); return; }
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      setActive(a => Math.min(a + 1, filtered.length - 1));
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      setActive(a => Math.max(a - 1, 0));
    } else if (e.key === 'Enter') {
      const choice = filtered[active];
      if (choice) pick(choice);
    }
  };

  const activeId = filtered[active] ? `mk-palette-opt-${filtered[active].key}` : undefined;

  return (
    <>
      <div className="mk-scrim" onClick={onClose} aria-hidden="true" />
      <div className="mk-palette" role="dialog" aria-modal="true" aria-label="Command palette">
        <div className="mk-palette-input-row">
          <Icon name="search" />
          <input
            ref={inputRef}
            className="mk-palette-input"
            placeholder="Jump to issue, board, or branch…"
            aria-label="Jump to issue, board, or branch"
            role="combobox"
            aria-expanded
            aria-controls="mk-palette-listbox"
            aria-activedescendant={activeId}
            aria-autocomplete="list"
            value={q}
            onChange={(e) => { setQ(e.target.value); setActive(0); }}
            onKeyDown={onKeyDown}
          />
          <span className="mk-kbd">esc</span>
        </div>
        <div className="mk-palette-results">
          <div className="mk-palette-section" id="mk-palette-section">Issues</div>
          {filtered.length === 0 ? (
            <div className="mk-palette-empty">no matches.</div>
          ) : (
            <div role="listbox" id="mk-palette-listbox" aria-labelledby="mk-palette-section">
              {filtered.map((c, i) => (
                <button
                  key={c.key}
                  id={`mk-palette-opt-${c.key}`}
                  role="option"
                  aria-selected={i === active}
                  className={`mk-palette-item${i === active ? ' is-active' : ''}`}
                  onMouseMove={() => setActive(i)}
                  onClick={() => pick(c)}
                >
                  <span className={`mk-pill mk-status-${c.column}`}>{c.columnLabel}</span>
                  <span className="mk-mono mk-palette-id">{c.key}</span>
                  <span className="mk-palette-title">{c.title}</span>
                  <span className="mk-kbd">↵</span>
                </button>
              ))}
            </div>
          )}
        </div>
      </div>
    </>
  );
}
