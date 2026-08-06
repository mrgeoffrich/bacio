import { useEffect, useMemo, useRef, useState } from 'react';
import type React from 'react';
import * as DropdownMenu from '@radix-ui/react-dropdown-menu';
import { Plus } from 'lucide-react';
import { filterCardsByQuery } from './kanbanOffBoard';
import type { BoardCard } from '../../api';

// AddCardsMenu — the lane-header "+" that puts issues ON the Kanban.
//
// This is the affordance Phase 6A left out. Drag only moves a card
// BETWEEN lanes, and on a git repo `kanban_column_id` starts NULL by
// design, so without this the board is permanently empty unless the user
// drops to a terminal and runs `bacio kanban move`. The picker's
// candidate list is the off-board set the board derives locally (see
// kanbanOffBoard) — no new endpoint, no second fetch.
//
// Two existing idioms, joined:
//
//   - the SHELL is the orphaned `.mk-dispatch-menu*` popover the pre-pivot
//     per-card dispatch menu used (BACI-222): sticky search row on top, a
//     max-height scroll region in the middle, sticky footer at the bottom.
//     It was built for exactly this problem — an unbounded list inside a
//     Radix dropdown — and the CSS was left behind when the agentic card
//     surface was deleted.
//   - the ROWS are the command palette's (`.mk-palette-item` + the
//     combobox keyboard model): focus stays in the input, ↑↓ move a
//     highlighted row, Enter picks it. A repo has hundreds of issues, so
//     typing must be the primary gesture, not scrolling.
//
// Picking a row does NOT close the menu (`onSelect` preventDefaults), so
// "add cards" is genuinely plural: each pick writes through, the parent's
// optimistic update drops that card out of `candidates`, and the list
// shrinks under the cursor until the user is done.

// The scroll region tops out around 280px, and a portal full of hundreds
// of rows is waste nobody sees. Past this we render the head of the list
// and tell the user to keep typing — the search is the real navigation.
const MAX_ROWS = 50;

type AddCardsMenuProps = {
  laneName: string;
  // Every card in the repo that no lane holds, already derived by the
  // board. Empty is a legitimate state (everything is already placed).
  candidates: BoardCard[];
  onAdd: (key: string) => void;
};

export default function AddCardsMenu({ laneName, candidates, onAdd }: AddCardsMenuProps) {
  const [open, setOpen] = useState(false);

  return (
    <DropdownMenu.Root open={open} onOpenChange={setOpen}>
      <DropdownMenu.Trigger asChild>
        <button
          type="button"
          className="mk-col-add-btn"
          aria-label={`Add cards to ${laneName}`}
          title={`Add cards to ${laneName}`}
        >
          <Plus size={14} strokeWidth={2} aria-hidden="true" />
        </button>
      </DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenu.Content
          className="mk-card-action-menu mk-kanban-add-menu"
          align="start"
          side="bottom"
          sideOffset={6}
          collisionPadding={8}
        >
          {/* Radix unmounts the portal contents on close, so the body's
              query / highlight state resets every open for free. */}
          <AddCardsMenuBody
            laneName={laneName}
            candidates={candidates}
            onAdd={onAdd}
            onDone={() => setOpen(false)}
          />
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  );
}

type AddCardsMenuBodyProps = {
  laneName: string;
  candidates: BoardCard[];
  onAdd: (key: string) => void;
  onDone: () => void;
};

function AddCardsMenuBody({ laneName, candidates, onAdd, onDone }: AddCardsMenuBodyProps) {
  const [query, setQuery] = useState('');
  const [active, setActive] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLDivElement>(null);

  const matches = useMemo(() => filterCardsByQuery(candidates, query), [candidates, query]);
  const visible = matches.length > MAX_ROWS ? matches.slice(0, MAX_ROWS) : matches;

  // Clamp at render rather than in an effect: the list shrinks under the
  // highlight on every keystroke AND on every successful add, and a
  // corrective effect would render one frame pointing past the end.
  const activeIndex = visible.length === 0 ? 0 : Math.min(active, visible.length - 1);

  // Focus the search input on mount. This is also what keeps Radix's
  // focus scope out of the way: FocusScope only steals focus when nothing
  // inside the popover already holds it, and a child effect runs before
  // its ancestor's — so by the time FocusScope looks, the caret is
  // already in the input and it leaves well alone.
  useEffect(() => {
    inputRef.current?.focus({ preventScroll: true });
  }, []);

  const highlight = (index: number) => {
    setActive(index);
    const row = listRef.current?.children[index];
    if (row instanceof HTMLElement) row.scrollIntoView({ block: 'nearest' });
  };

  const add = (key: string) => {
    onAdd(key);
    // The picked card leaves `candidates`, so whatever slid into this slot
    // is now under the highlight — keep it there rather than jumping.
    setActive(activeIndex);
  };

  const onKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    // Escape belongs to Radix, and unavoidably so: DismissableLayer
    // listens on `document` in the CAPTURE phase, so it has already run
    // by the time this handler sees the key and no amount of
    // stopPropagation would give us a "first Escape clears the query"
    // stage. Escape closes the menu, which is what a dropdown is expected
    // to do anyway.
    if (e.key === 'Escape') return;
    // Tab belongs to Radix too (it closes the menu and restores focus).
    if (e.key === 'Tab') return;
    // Everything else is ours. The stopPropagation matters: Radix's menu
    // content runs a typeahead on every character key, which would yank
    // focus out of this input and onto a row that happens to start with
    // the letter just typed.
    e.stopPropagation();
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      if (visible.length > 0) highlight((activeIndex + 1) % visible.length);
      return;
    }
    if (e.key === 'ArrowUp') {
      e.preventDefault();
      if (visible.length > 0) highlight((activeIndex - 1 + visible.length) % visible.length);
      return;
    }
    if (e.key === 'Enter') {
      e.preventDefault();
      const choice = visible[activeIndex];
      if (choice) add(choice.key);
    }
  };

  return (
    <div className="mk-dispatch-menu" onClick={(e) => e.stopPropagation()}>
      <div className="mk-dispatch-menu-search-row">
        <input
          ref={inputRef}
          type="text"
          className="mk-dispatch-menu-search"
          placeholder="Search issues…"
          aria-label={`Search issues to add to ${laneName}`}
          value={query}
          onChange={(e) => { setQuery(e.target.value); setActive(0); }}
          onKeyDown={onKeyDown}
        />
      </div>
      <div className="mk-card-action-menu-label">Add to {laneName}</div>
      <div className="mk-dispatch-menu-list" ref={listRef}>
        {visible.length === 0 ? (
          <div className="mk-dispatch-menu-empty">
            {candidates.length === 0
              ? 'Every issue in this repository is already on the board.'
              : 'No matching issues.'}
          </div>
        ) : (
          visible.map((card, i) => (
            <DropdownMenu.Item
              key={card.key}
              className={`mk-palette-item${i === activeIndex ? ' is-active' : ''}`}
              // Stay open so several cards can be placed in one visit —
              // that is the whole point of a multi-add picker.
              onSelect={(e) => { e.preventDefault(); add(card.key); }}
              onMouseMove={() => setActive(i)}
            >
              <span className="mk-mono mk-palette-id">{card.key}</span>
              <span className="mk-palette-title">{card.title}</span>
            </DropdownMenu.Item>
          ))
        )}
      </div>
      <div className="mk-dispatch-menu-footer">
        {matches.length > visible.length && (
          <div className="mk-card-action-menu-label">
            Showing {visible.length} of {matches.length} — keep typing to narrow
          </div>
        )}
        <DropdownMenu.Item className="mk-card-action-item" onSelect={onDone}>
          Done
        </DropdownMenu.Item>
      </div>
    </div>
  );
}
