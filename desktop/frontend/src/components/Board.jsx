import React, { useState, useRef, useEffect, useLayoutEffect, useCallback, useMemo } from 'react';
import { AnimatePresence } from 'motion/react';
import KanbanCard from './KanbanCard.jsx';
import QuestionModal from './QuestionModal.jsx';
import Icon from './Icon.jsx';
import { readCollapsed, persistCollapsed } from './boardCollapsePersistence.ts';
import { readCompact, persistCompact } from './boardCompactPersistence.ts';

// BACI-119: the board's horizontal scroll offset is persisted per repo to
// localStorage so navigating away (Features / Docs / single issue / etc.)
// and back lands the user where they were instead of resetting to 0. The
// board element unmounts on every nav switch (see App.jsx's activeView
// ternary), so React itself can't preserve the offset — we have to restore
// it explicitly on mount. Keyed by repo prefix because switching repos
// changes the column / card set, so a global offset would point at a
// stale position.
const BOARD_SCROLL_KEY = 'bacio-board-scroll';

// localStorage may throw under hardened browser profiles — every access
// is wrapped in try/catch and falls back to a no-op / 0 (same pattern as
// App.jsx's theme + active-repo helpers).
function readBoardScrollMap() {
  try {
    const raw = localStorage.getItem(BOARD_SCROLL_KEY);
    if (!raw) return {};
    const parsed = JSON.parse(raw);
    return parsed && typeof parsed === 'object' ? parsed : {};
  } catch {
    return {};
  }
}
function readBoardScroll(repo) {
  if (!repo) return 0;
  const map = readBoardScrollMap();
  const v = map[repo];
  return typeof v === 'number' && Number.isFinite(v) ? v : 0;
}
function persistBoardScroll(repo, offset) {
  if (!repo) return;
  try {
    const map = readBoardScrollMap();
    map[repo] = Math.max(0, Math.round(offset));
    localStorage.setItem(BOARD_SCROLL_KEY, JSON.stringify(map));
  } catch {
    /* non-fatal — the offset just won't survive the next nav/relaunch */
  }
}

export default function Board({ activeBoard, columns, cards, promptConfig, onMoveCard, onOpenCard, onOpenIssue, onDispatchFromCard, onCancelWaitingCard, onAfterQuestionResolved, onQuickEval, pinnedKeys, onTogglePin, onSetFollowOn, onCancelFollowOn, hoveredKey, jumpKey, flyingShipKey }) {
  const [dragKey, setDragKey] = useState(null);
  const [overCol, setOverCol] = useState(null);
  // BACI-53: the kanban card "? N" pill opens the shared
  // QuestionModal. State lives here (rather than per-card) so the
  // modal stays mounted across re-renders of the underlying card and
  // closes cleanly when the user submits/dismisses.
  const [activeQuestionId, setActiveQuestionId] = useState(null);
  // BACI-119: ref on the scrolling .mk-board element so the restore
  // effect can drive scrollLeft and the onScroll handler can read it.
  const boardRef = useRef(null);
  // BACI-119: debounce token for the scroll-write so we don't hit
  // localStorage on every scroll tick.
  const scrollWriteTimer = useRef(null);
  // BACI-188: per-column user-collapsed set, persisted to localStorage
  // keyed by repo prefix. Seeded on mount and re-seeded on every repo
  // switch (the persisted shape is per-repo, so the cross-repo carry-over
  // would be wrong). Empty columns still collapse via the existing rule
  // — `isCollapsed` below ORs the two predicates.
  const [userCollapsed, setUserCollapsed] = useState(() => readCollapsed(activeBoard));
  useEffect(() => {
    setUserCollapsed(readCollapsed(activeBoard));
  }, [activeBoard]);
  // BACI-191: per-column user-compact set, persisted to localStorage.
  // Parallel to userCollapsed — same per-repo seeding, same re-seed on
  // repo switch. Compact and collapsed are independent: a column can be
  // compact without being collapsed.
  const [userCompact, setUserCompact] = useState(() => readCompact(activeBoard));
  useEffect(() => {
    setUserCompact(readCompact(activeBoard));
  }, [activeBoard]);

  // BACI-114: per-key card lookup so each KanbanCard's blocked popover
  // can join in the blocker's title from the same `cards` array it
  // already has — Option A's "thin renderer over data the brief
  // already has" rule. Built once per render and re-derived when the
  // poll lands a fresh cards array.
  const cardsByKey = useMemo(() => new Map(cards.map(c => [c.key, c])), [cards]);

  // BACI-188: toggle helpers. `collapseColumn` adds the state to the
  // user-collapsed set and persists; `expandColumn` removes it. Both
  // are no-ops on a redundant flip, so the persisted shape stays tidy.
  const collapseColumn = useCallback((state) => {
    setUserCollapsed(prev => {
      if (prev.has(state)) return prev;
      const next = new Set(prev);
      next.add(state);
      persistCollapsed(activeBoard, next);
      return next;
    });
  }, [activeBoard]);
  const expandColumn = useCallback((state) => {
    setUserCollapsed(prev => {
      if (!prev.has(state)) return prev;
      const next = new Set(prev);
      next.delete(state);
      persistCollapsed(activeBoard, next);
      return next;
    });
  }, [activeBoard]);

  // BACI-191: compact/uncompact toggle helpers. `compactColumn` adds
  // the state to the user-compact set; `uncompactColumn` removes it.
  // Both are no-ops on a redundant flip, so the persisted shape stays
  // tidy. Only populated columns carry the toggle button (same
  // constraint as BACI-188's collapse button).
  const compactColumn = useCallback((state) => {
    setUserCompact(prev => {
      if (prev.has(state)) return prev;
      const next = new Set(prev);
      next.add(state);
      persistCompact(activeBoard, next);
      return next;
    });
  }, [activeBoard]);
  const uncompactColumn = useCallback((state) => {
    setUserCompact(prev => {
      if (!prev.has(state)) return prev;
      const next = new Set(prev);
      next.delete(state);
      persistCompact(activeBoard, next);
      return next;
    });
  }, [activeBoard]);

  // BACI-119: restore the saved horizontal scrollLeft on mount, on repo
  // switch, and whenever the rendered card/column set changes width.
  // The dependency on cards.length / columns.length matters: cards
  // load async, so the first mount may render a 0-width board (clamping
  // any restore to 0). Re-running once cards land lets the assignment
  // stick. Setting scrollLeft past the current scrollWidth is a no-op
  // (the browser clamps); once content is wide enough it lands. Using
  // useLayoutEffect (not useEffect) restores before paint — no flash at
  // offset 0 first.
  useLayoutEffect(() => {
    const el = boardRef.current;
    if (!el || !activeBoard) return;
    const saved = readBoardScroll(activeBoard);
    if (saved > 0 && el.scrollLeft !== saved) {
      el.scrollLeft = saved;
    }
  }, [activeBoard, cards.length, columns.length]);

  // BACI-119: persist the offset on scroll with a small debounce. A
  // useEffect cleanup on unmount would also work, but capturing live
  // also covers tab-close / window-quit paths where React doesn't get
  // a clean unmount.
  const onBoardScroll = useCallback((e) => {
    const left = e.currentTarget.scrollLeft;
    if (scrollWriteTimer.current) clearTimeout(scrollWriteTimer.current);
    scrollWriteTimer.current = setTimeout(() => {
      persistBoardScroll(activeBoard, left);
    }, 150);
  }, [activeBoard]);

  return (
    <div className="mk-board" ref={boardRef} onScroll={onBoardScroll}>
      {columns.map(col => {
        // BACI-198: filter the in-flight card out of the Done column
        // while the BACI-193 ship-flourish is animating. The card has
        // already polled in as `column: done`, so it would otherwise
        // mount in this column with the same Motion `layoutId` as the
        // ShippedPopover's destination slot — three live matches in
        // total (exiting card in source column via popLayout, mounting
        // Done card, popover slot) make the FLIP target unpredictable,
        // so the card was just as likely to land in the Done column
        // as in the pill. Hiding the Done instance during flight
        // leaves the popover slot as the single destination. It pops
        // back into Done when `flyingShipKey` clears (after
        // `onFlightDone` in App / shipFlourish).
        const colCards = cards.filter(c => c.column === col.state && c.key !== flyingShipKey);
        // BACI-77 + BACI-188: a column collapses to the narrow vertical-
        // title strip when it's empty (the original behaviour) OR when
        // the user has explicitly collapsed it via the header chevron.
        // The two predicates render the same compact visual; the only
        // difference is which one the strip's expand button can clear
        // (user-collapse only — emptiness is intrinsic).
        const isUserCollapsed = userCollapsed.has(col.state);
        const isCollapsed = colCards.length === 0 || isUserCollapsed;
        return (
          <div
            key={col.state}
            className={`mk-col ${isCollapsed ? 'is-collapsed' : ''} ${overCol === col.state ? 'is-over' : ''}`}
            onDragOver={(e) => { e.preventDefault(); setOverCol(col.state); }}
            onDragLeave={() => setOverCol(null)}
            onDrop={(e) => {
              e.preventDefault();
              if (dragKey) {
                onMoveCard(dragKey, col.state);
                // BACI-188: a drop onto a user-collapsed strip expands
                // the destination so the user sees the card land instead
                // of it vanishing behind the rotated title.
                if (isUserCollapsed) expandColumn(col.state);
              }
              setDragKey(null);
              setOverCol(null);
            }}
          >
            {isCollapsed ? (
              <>
                {/* BACI-188: only user-collapsed strips carry the expand
                    affordance — an empty column has nothing to expand to.
                    Mounted above the rotated title so clicking the
                    chevron doesn't fight the title text for the cursor. */}
                {isUserCollapsed && (
                  <button
                    type="button"
                    className="mk-col-expand-btn"
                    aria-label={`Expand ${col.label} column`}
                    onClick={() => expandColumn(col.state)}
                  >
                    <Icon name="maximize-2" />
                  </button>
                )}
                <div className={`mk-col-collapsed-title mk-status-${col.state}`}>
                  {col.label}
                </div>
              </>
            ) : (
              <>
                <header className="mk-col-head">
                  <span className={`mk-col-pill mk-status-${col.state}`}>{col.label}</span>
                  <span className="mk-col-count">{colCards.length}</span>
                  {/* BACI-191 / BACI-207: compact-cards toggle sits between
                      the count and the collapse chevron. The glyph swaps
                      with state — converging chevrons (down-up) when the
                      column is expanded read as "squeeze together",
                      diverging chevrons (up-down) when already compact
                      read as "let breathe". Mirrors the BACI-201
                      Minimize2 / Maximize2 pairing on the collapse
                      strip. is-active still lights the icon when compact
                      so the toggle state reads even mid-flick. */}
                  <button
                    type="button"
                    className={`mk-col-compact-btn${userCompact.has(col.state) ? ' is-active' : ''}`}
                    aria-label={userCompact.has(col.state) ? `Expand cards in ${col.label} column` : `Compact cards in ${col.label} column`}
                    aria-pressed={userCompact.has(col.state)}
                    onClick={() => userCompact.has(col.state) ? uncompactColumn(col.state) : compactColumn(col.state)}
                  >
                    <Icon name={userCompact.has(col.state) ? 'chevrons-up-down' : 'chevrons-down-up'} />
                  </button>
                  {/* BACI-188: the collapse affordance only appears on
                      populated columns — an empty column already wears
                      the strip via the existing rule. Right-aligned so
                      the title pill + count keep the natural reading
                      order on the left edge of the header. */}
                  <button
                    type="button"
                    className="mk-col-collapse-btn"
                    aria-label={`Collapse ${col.label} column`}
                    onClick={() => collapseColumn(col.state)}
                  >
                    <Icon name="minimize-2" />
                  </button>
                </header>
                <div className="mk-col-body">
                  {/* BACI-193: AnimatePresence wraps the card list so a
                      removed card (poll lands the issue in `done`, drag
                      pulls it to another column, etc.) plays its exit
                      animation before unmount. `mode="popLayout"` keeps
                      siblings FLIP-ing while the exit runs — the
                      alternative ("wait") would freeze the column on
                      removal until the exit finishes, which fights the
                      cross-column layout move. `initial={false}` skips
                      the entry animation on first mount so a freshly-
                      loaded board doesn't pop in. */}
                  <AnimatePresence mode="popLayout" initial={false}>
                    {colCards.map(card => (
                      <KanbanCard
                        key={card.key}
                        card={card}
                        cardsByKey={cardsByKey}
                        promptConfig={promptConfig}
                        isDragging={dragKey === card.key}
                        compact={userCompact.has(col.state)}
                        onDragStart={() => { if (!card.taken && !card.waitingState) setDragKey(card.key); }}
                        onDragEnd={() => { setDragKey(null); setOverCol(null); }}
                        onOpen={() => onOpenCard(card)}
                        onDispatch={onDispatchFromCard}
                        onCancelWaiting={onCancelWaitingCard}
                        onOpenQuestion={(id) => setActiveQuestionId(id)}
                        onOpenIssue={onOpenIssue}
                        onQuickEval={onQuickEval}
                        isPinned={pinnedKeys?.has(card.key) || false}
                        onTogglePin={onTogglePin}
                        onSetFollowOn={onSetFollowOn}
                        onCancelFollowOn={onCancelFollowOn}
                        isTrayHover={hoveredKey === card.key}
                        isJumping={jumpKey === card.key}
                      />
                    ))}
                  </AnimatePresence>
                </div>
              </>
            )}
          </div>
        );
      })}
      <QuestionModal
        questionId={activeQuestionId}
        onClose={() => {
          setActiveQuestionId(null);
          if (onAfterQuestionResolved) onAfterQuestionResolved();
        }}
      />
    </div>
  );
}
