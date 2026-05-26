import React, { useEffect, useMemo, useRef, useState } from 'react';
import { filterPrompts } from './dispatchMenuFilter.ts';

// BACI-222: the popover body for the kanban dispatch menus (follow-on
// chip + zap button). Replaces the unbounded `DropdownMenu.Content`
// flat-list bodies on `KanbanCard.jsx` with a sized shell:
//   - sticky filter input at the top
//   - scrollable list region in the middle (max-height + overflow-y)
//   - optional sticky footer at the bottom (Cancel follow-on / status)
//
// The component intentionally does NOT own the row markup — the two
// call sites render different row shapes (compound "Plan, then
// Implement" matrix on zap, flat list on follow-on). It hands the
// caller back the filtered prompt list via `renderRows({ visible })`
// and takes care of:
//   - filtering against `actionLabel` / `label` / `mode` (substring)
//   - keyboard model: ↑↓ moves the active row, Enter clicks it,
//     Esc clears the filter (then closes on a second Esc — handled
//     by Radix's own onEscapeKeyDown when the filter is already empty)
//   - autofocus on the search input when the menu opens
//   - pre-scrolling the row tagged `.is-current` into view on open
//
// We track the active-row index by querying `[data-dispatch-row]`
// elements off the list ref each keystroke. Enter dispatches the
// active row's click — that way the caller's `onSelect` / onClick
// handler (already wired into each row) fires without needing a
// separate pick callback prop. Radix `DropdownMenu.Item`s underneath
// receive the click and run their `onSelect` plus the menu-close
// behaviour for free.
export default function DispatchMenuContent({
  prompts,
  currentMode,
  menuLabel,
  footer,
  emptyText = 'No matching modes.',
  placeholder = 'Filter modes…',
  renderRows,
}) {
  const [query, setQuery] = useState('');
  const [activeIndex, setActiveIndex] = useState(0);
  const inputRef = useRef(null);
  const listRef = useRef(null);

  const visible = useMemo(() => filterPrompts(prompts || [], query), [prompts, query]);

  // Clamp the active index whenever the visible list shrinks beneath
  // it. Keeps Enter sane after typing a query that filters out the
  // previously-active row.
  useEffect(() => {
    if (activeIndex >= visible.length) {
      setActiveIndex(visible.length === 0 ? 0 : visible.length - 1);
    }
  }, [visible.length, activeIndex]);

  // Autofocus the filter input on mount (the menu is only rendered when
  // open — Radix unmounts the Portal contents on close, so a single
  // mount-time focus is enough).
  useEffect(() => {
    inputRef.current?.focus({ preventScroll: true });
  }, []);

  // On open, scroll the `.is-current` row into view so a user with an
  // already-attached follow-on lands on "this is what's queued"
  // without needing to scroll. Re-runs only when the prompt list ref
  // changes (mount + repopulation), not on every keystroke.
  useEffect(() => {
    const root = listRef.current;
    if (!root) return;
    const current = root.querySelector('[data-dispatch-row].is-current');
    if (current && typeof current.scrollIntoView === 'function') {
      current.scrollIntoView({ block: 'nearest' });
    }
  }, [prompts]);

  const rowEls = () => {
    const root = listRef.current;
    if (!root) return [];
    return Array.from(root.querySelectorAll('[data-dispatch-row]'));
  };

  const moveActive = (delta) => {
    const rows = rowEls();
    if (rows.length === 0) return;
    setActiveIndex(prev => {
      const next = (prev + delta + rows.length) % rows.length;
      const el = rows[next];
      if (el && typeof el.scrollIntoView === 'function') {
        el.scrollIntoView({ block: 'nearest' });
      }
      return next;
    });
  };

  const activateActive = () => {
    const rows = rowEls();
    const el = rows[activeIndex];
    if (!el) return;
    // The row's own onClick (and Radix's onSelect bound to it) fires
    // and the dropdown closes via the click-through to the Item.
    el.click();
  };

  const onInputKeyDown = (e) => {
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      moveActive(1);
      return;
    }
    if (e.key === 'ArrowUp') {
      e.preventDefault();
      moveActive(-1);
      return;
    }
    if (e.key === 'Enter') {
      e.preventDefault();
      activateActive();
      return;
    }
    if (e.key === 'Escape' && query) {
      // First Esc clears a non-empty filter; second Esc (handled by
      // Radix's onEscapeKeyDown on Content) closes the menu.
      e.preventDefault();
      e.stopPropagation();
      setQuery('');
      setActiveIndex(0);
    }
  };

  // Paint the active-row hint: set `data-active="true"` on the row at
  // `activeIndex` (and clear it on every other row). Runs every render
  // — cheap (linear in row count, ≤30 rows) and avoids tying the
  // caller's row markup to a per-row React-state prop.
  useEffect(() => {
    const rows = rowEls();
    rows.forEach((el, i) => {
      if (i === activeIndex) el.setAttribute('data-active', 'true');
      else el.removeAttribute('data-active');
    });
  });

  return (
    <div
      className="mk-dispatch-menu"
      onClick={(e) => e.stopPropagation()}
    >
      <div className="mk-dispatch-menu-search-row">
        <input
          ref={inputRef}
          type="text"
          className="mk-dispatch-menu-search"
          placeholder={placeholder}
          value={query}
          onChange={(e) => { setQuery(e.target.value); setActiveIndex(0); }}
          onKeyDown={onInputKeyDown}
          aria-label="Filter dispatch modes"
        />
      </div>
      {menuLabel && (
        <div className="mk-card-action-menu-label">{menuLabel}</div>
      )}
      <div
        ref={listRef}
        className="mk-dispatch-menu-list"
      >
        {visible.length === 0 ? (
          <div className="mk-dispatch-menu-empty">{emptyText}</div>
        ) : (
          renderRows({ visible, currentMode })
        )}
      </div>
      {footer && (
        <div className="mk-dispatch-menu-footer">
          {footer}
        </div>
      )}
    </div>
  );
}
