import React, { useEffect, useMemo, useRef, useState } from 'react';
import { isTerminalState } from '../lib/issueState';
import { waitingStateLabel } from '../lib/waitingLabels.ts';
import Icon from './Icon.jsx';
import { readCollapsed, persistCollapsed } from './activityTrayPersistence';

// ActivityTray (BACI-171) — a bottom-right floating panel that narrates
// what's just happened on the board. Position-fixed, overlays the
// board, never reflows the columns. Entries arrive when a card enters
// a queued / taken state or changes column; existing entries flash
// when their card transitions again; cards landing in done/cancelled
// flash on the way out and then auto-leave; entries can be manually
// dismissed.
//
// All state is local to the component: a `previousCardsRef` carries
// the last `cards` snapshot for the diff, an `entries` state holds
// the LIFO list, and a `dismissedRef` Set tracks keys the user has
// dismissed (so a fresh transition on the same card after dismissal
// re-enters). The *entries* list does not persist across reloads — a
// hard refresh starts the tray empty, which matches "things that have
// happened recently". The user's *collapsed/expanded preference* does
// persist via localStorage (BACI-186) — see activityTrayPersistence.ts.

// FLASH_MS is how long the row's pulse animation runs before the
// `.is-flashing` modifier is stripped — matches the keyframe
// duration in app.css (`mk-tray-flash`).
const FLASH_MS = 600;

// MAX_ENTRIES caps the visible tray. Older entries drop off the
// bottom when the cap is hit; the overflow within the cap is
// scrollable. 10 is the ticket's default, with overflow scroll
// inside the tray.
const MAX_ENTRIES = 10;

// isQueued captures the "queued for work — about to be acted on"
// shape from the ticket: a card with an open claim OR an active
// dispatch state. The diff treats either flip as the entry trigger
// for the queued half of the rule (the column-change half is
// handled separately).
function isQueued(card) {
  return !!card.taken || !!card.waitingState;
}

// queuedKey turns the waiting/taken shape into a comparable scalar so
// the diff can spot a *change* in the queued state — a card going
// taken → not-taken, or a dispatch flipping kind (queued → delivered).
// The kind is the only label-affecting bit of waitingState; the rest
// is metadata.
function queuedKey(card) {
  if (card.taken) return 'taken';
  if (card.waitingState) return `waiting:${card.waitingState.kind || ''}`;
  return '';
}

// computeTransitions diffs two `cards` arrays (keyed by card.key) and
// returns the list of transitions that should land in the tray. Each
// transition carries the card itself (so the renderer can pull
// title/column straight off it) plus a `reason` enum for debugging.
// Pure function — no React state — so it's testable in isolation if
// a future refactor wants to lift it into lib/.
//
// Transition rules (any one of which produces an entry):
//
//   - column changed between snapshots (the kanban-state move)
//   - queued shape changed (taken/dispatch flipped on or off, or the
//     dispatch's kind changed in a way that affects the label)
//
// Cards that disappeared from the new snapshot (deleted server-side,
// or a repo-switch removed them from scope) are skipped — they're not
// transitions the tray should narrate. A card landing in a terminal
// column (done/cancelled) still emits a transition so the tray can
// flash it out before removing it.
export function computeTransitions(prevCards, nextCards) {
  const out = [];
  if (!Array.isArray(prevCards) || !Array.isArray(nextCards)) return out;
  const prevByKey = new Map();
  for (const c of prevCards) prevByKey.set(c.key, c);
  for (const next of nextCards) {
    const prev = prevByKey.get(next.key);
    if (!prev) continue; // first time we see this card — see seeding below
    if (prev.column !== next.column) {
      out.push({ card: next, reason: 'column' });
      continue;
    }
    if (queuedKey(prev) !== queuedKey(next)) {
      out.push({ card: next, reason: 'queued' });
    }
  }
  return out;
}

// entryFromCard builds a tray Entry from a board card. The renderer
// reads these directly; `addedAt` keeps the LIFO ordering stable when
// two entries share the same poll tick.
//
// BACI-183: the Entry only carries the immutable-at-insert-time fields
// (key, title, column, columnLabel, addedAt). The status row that
// replaced the old tags + description preview reads from the *live*
// card snapshot via `cardsByKey` at render time, so a verb flip
// ("designing" → "implementing") on a card already in the tray
// updates in place without re-inserting the row.
function entryFromCard(card) {
  return {
    key: card.key,
    title: card.title || '',
    column: card.column,
    columnLabel: card.columnLabel || card.column,
    addedAt: Date.now() + Math.random(), // unique even within one tick
  };
}

// entryStatus picks the single status row to render under the entry's
// title, with a precedence ladder: needs-action (the user must act
// before anything moves) wins over an active-claim verb ("still
// working" — interesting but secondary) wins over the pre-claim
// waiting label ("not yet started"). Returns `{kind: 'none'}` when no
// row should render — the entry collapses to head + title only, which
// is the common shape for a column-move with no dispatch in flight.
//
// The verb path title-cases the wire string for the user-visible
// "{Verb}" wording (BACI-197: dropped the trailing " in progress" —
// the italic mint treatment carries the in-flight meaning on its
// own) without mutating the wire format; the waiting path reuses
// `waitingStateLabel` so the tray narrates the same sentence the
// kanban card already shows.
//
// Falls back to `{kind:'none'}` when `card` is null — entries whose
// underlying card dropped from `cards` between polls still render
// their immutable fields without a stale status line.
export function entryStatus(card) {
  if (!card) return { kind: 'none' };
  if (card.column === 'needs_action') {
    return { kind: 'needs_action', text: 'Needs action' };
  }
  if (card.taken && card.activeVerb) {
    const verb = card.activeVerb;
    const titled = verb.charAt(0).toUpperCase() + verb.slice(1);
    return { kind: 'verb', text: `${titled}` };
  }
  if (card.waitingState) {
    const label = waitingStateLabel(card.waitingState);
    if (label) return { kind: 'waiting', text: label };
  }
  return { kind: 'none' };
}

// stateClass maps a card's column to the existing .mk-status-* token
// vocabulary so the entry's right-edge pill picks up the column's
// colour family (todo / doing / review / done / blocked) without a
// new token set. Matches the per-status colours used by .mk-pill on
// the topbar's agent counters and the question header chip.
function stateClass(column) {
  switch (column) {
    case 'in_progress':
    case 'needs_action':
      return 'mk-status-doing';
    case 'in_review':
      return 'mk-status-review';
    case 'done':
      return 'mk-status-done';
    case 'cancelled':
      return 'mk-status-blocked';
    default:
      return 'mk-status-todo';
  }
}

export default function ActivityTray({ cards, pinnedKeys, onTogglePin, onOpenCard, onHoverCard, onCardClickFromTray }) {
  const [entries, setEntries] = useState([]);
  // Seed from localStorage via the lazy initialiser so the first paint
  // already reflects the user's saved preference (BACI-186); writes go
  // through setCollapsedPersisted below.
  const [collapsed, setCollapsed] = useState(readCollapsed);
  // setCollapsedPersisted is the single write seam — every flip of the
  // collapse state updates React and writes through to localStorage so
  // the next reload picks it up.
  const setCollapsedPersisted = (v) => {
    setCollapsed(v);
    persistCollapsed(v);
  };
  // Track which entry keys are currently flashing — toggled per entry
  // rather than re-rendering the whole list on every flash on/off.
  const [flashingKeys, setFlashingKeys] = useState(() => new Set());

  // previousCardsRef holds the last `cards` snapshot the diff ran
  // against. On the very first poll there's no previous snapshot, so
  // the diff returns empty — seeding behaviour: the tray starts empty
  // and only narrates transitions, never the initial state.
  const previousCardsRef = useRef(null);
  // dismissedRef tracks keys the user has dismissed via the × button.
  // Used to suppress *current* entries on the next diff so the row
  // doesn't bounce back if a poll re-derives the same transition.
  // Cleared per-key when a fresh transition arrives — see the diff
  // loop below.
  const dismissedRef = useRef(new Set());
  // Track flash + removal timers so we can clear them on unmount and
  // avoid setState calls on a dead component.
  const timersRef = useRef(new Map());

  // Run the diff every time `cards` changes (the App's 10s poll
  // already passes a fresh array on every tick). Synchronous — no
  // async path of its own.
  useEffect(() => {
    const prev = previousCardsRef.current;
    previousCardsRef.current = cards;
    if (!prev) return; // first mount: seed the snapshot, emit nothing
    const transitions = computeTransitions(prev, cards);
    if (transitions.length === 0) return;

    // Build a key → entry map for fast in-place updates on rows
    // that are already in the tray. We rebuild `entries` once per
    // tick so multiple transitions in the same tick land atomically.
    setEntries(current => {
      const byKey = new Map();
      for (const e of current) byKey.set(e.key, e);
      const updatedKeys = new Set();
      const newKeys = new Set();
      for (const { card } of transitions) {
        // Cards landing in terminal columns flash out then leave —
        // we still need an entry for them to do that, so they go
        // through the same insert/update path here; the post-flash
        // cleanup further down removes them.
        if (byKey.has(card.key)) {
          // Update in place: refresh title/column from the latest
          // card snapshot but keep the original addedAt so the row
          // doesn't jump back to the top. Mark for flash. The
          // status row (BACI-183) is derived live from cardsByKey
          // at render time, so it doesn't need re-seeding here.
          const existing = byKey.get(card.key);
          byKey.set(card.key, {
            ...entryFromCard(card),
            addedAt: existing.addedAt,
          });
          updatedKeys.add(card.key);
          // A fresh transition on a previously-dismissed card
          // re-admits it — the user said "I've clocked *that*
          // change", but this is a new one.
          dismissedRef.current.delete(card.key);
        } else {
          // New entry — but only if the user hasn't dismissed this
          // card's most recent transition. (dismissedRef is cleared
          // per-key on every real transition, so a card the user
          // dismissed and that then transitions again still lands.)
          if (dismissedRef.current.has(card.key)) continue;
          byKey.set(card.key, entryFromCard(card));
          newKeys.add(card.key);
        }
      }
      // Rebuild the list: newest entries on top (LIFO by addedAt
      // descending), cap at MAX_ENTRIES, drop older entries off the
      // tail when over.
      const merged = Array.from(byKey.values())
        .sort((a, b) => b.addedAt - a.addedAt)
        .slice(0, MAX_ENTRIES);

      // Schedule flash on every entry that updated (and on freshly
      // added entries — the visual cue is the same: "this row
      // changed"). Skipping flashes on rows that didn't transition.
      const flashKeys = new Set([...updatedKeys, ...newKeys]);
      if (flashKeys.size > 0) {
        scheduleFlash(flashKeys);
      }
      // Terminal-state rows flash out then auto-leave. The flash
      // timer above gives the visual cue; chain a second timer that
      // strips the row after FLASH_MS so the user sees the close
      // transition (ticket open-question: "brief flash on the way
      // out so you see the closing transition").
      for (const { card } of transitions) {
        if (isTerminalState(card.column)) {
          scheduleRemoval(card.key);
        }
      }
      return merged;
    });
    // We intentionally only react to `cards`; the helpers above
    // operate on refs / setState callbacks.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [cards]);

  // Strip entries whose underlying card no longer exists (e.g. a
  // repo switch wiped the cards array) so the tray doesn't carry
  // ghosts. The cheap shape: build a key set from `cards` and
  // filter `entries` against it. Skips work when nothing dropped.
  useEffect(() => {
    if (!cards) return;
    const liveKeys = new Set(cards.map(c => c.key));
    setEntries(current => {
      const filtered = current.filter(e => liveKeys.has(e.key));
      return filtered.length === current.length ? current : filtered;
    });
  }, [cards]);

  // scheduleFlash adds the flash class for FLASH_MS then strips it.
  // Stable per-call: each call captures its own key set so a quick
  // burst of transitions on the same row resets the animation
  // cleanly. Uses the timersRef so unmount can drain pending timers.
  function scheduleFlash(keys) {
    setFlashingKeys(prev => {
      const next = new Set(prev);
      for (const k of keys) next.add(k);
      return next;
    });
    for (const k of keys) {
      // Clear any pending flash-off timer for this key — restart the
      // animation rather than ending mid-pulse if a second transition
      // lands before the first finishes.
      const prevTimer = timersRef.current.get(`flash:${k}`);
      if (prevTimer) clearTimeout(prevTimer);
      const t = setTimeout(() => {
        setFlashingKeys(prev => {
          if (!prev.has(k)) return prev;
          const next = new Set(prev);
          next.delete(k);
          return next;
        });
        timersRef.current.delete(`flash:${k}`);
      }, FLASH_MS);
      timersRef.current.set(`flash:${k}`, t);
    }
  }

  // scheduleRemoval (for terminal-state landings) waits for the
  // flash to finish before stripping the entry. A small grace beyond
  // FLASH_MS lets the colour fade complete before the row vanishes.
  function scheduleRemoval(key) {
    const prevTimer = timersRef.current.get(`rm:${key}`);
    if (prevTimer) clearTimeout(prevTimer);
    const t = setTimeout(() => {
      setEntries(current => current.filter(e => e.key !== key));
      timersRef.current.delete(`rm:${key}`);
    }, FLASH_MS + 50);
    timersRef.current.set(`rm:${key}`, t);
  }

  // Drain timers on unmount so we don't setState into the void.
  useEffect(() => {
    return () => {
      for (const t of timersRef.current.values()) clearTimeout(t);
      timersRef.current.clear();
    };
  }, []);

  // dismiss removes an entry on the × click. The key joins the
  // dismissedRef set so the next diff doesn't re-emit the same
  // already-clocked transition; a *new* transition on the same card
  // re-admits it (see the diff loop above).
  const dismiss = (key) => {
    dismissedRef.current.add(key);
    setEntries(current => current.filter(e => e.key !== key));
  };

  // BACI-183: build a live key → card lookup once per render so the
  // per-entry status row can read the freshest card snapshot (verb,
  // waitingState, column). The Entry itself only carries
  // immutable-at-insert-time fields; the status row updates in place
  // when the poll lands a new verb on a card already in the tray.
  // Same pattern as KanbanCard's `cardsByKey` prop.
  const cardsByKey = useMemo(() => {
    const m = new Map();
    if (Array.isArray(cards)) {
      for (const c of cards) m.set(c.key, c);
    }
    return m;
  }, [cards]);

  // BACI-208: merge the pinned rows into the main transitions list
  // rather than rendering them as a separate "Pinned" section above
  // "Recent". The list is built in two passes:
  //
  //   1. pinned rows first (in pinnedKeys iteration order) — these
  //      are derived from pinnedKeys ∩ cards on every poll, so a
  //      pinned key whose card dropped from `cards` is skipped
  //      (corner-button persistence keeps the key alive in storage
  //      so a re-fetched card resurfaces with its pin intact);
  //   2. then transitions in their existing LIFO (addedAt-desc)
  //      order, skipping any whose key is already pinned (one row
  //      per card, pinned form wins).
  //
  // The MAX_ENTRIES cap in the setEntries reducer above still caps
  // the *transitions* feed, but pinned rows are inherently exempt
  // because they're built from a different source — a noisy board
  // can churn the bottom of the list but can't push a pinned row
  // off (the original motivation for BACI-192's section split,
  // preserved without the visual separator).
  //
  // Each row carries a `kind` so the renderer can wire the right
  // dismiss-× behaviour: 'pinned' rows × calls onTogglePin (unpin);
  // 'entry' rows × calls dismiss (mark dismissed, drop from tray).
  const rows = useMemo(() => {
    const out = [];
    const seen = new Set();
    if (pinnedKeys && pinnedKeys.size > 0) {
      for (const key of pinnedKeys) {
        const card = cardsByKey.get(key);
        if (!card) continue;
        out.push({ kind: 'pinned', key, card });
        seen.add(key);
      }
    }
    for (const e of entries) {
      if (seen.has(e.key)) continue;
      out.push({ kind: 'entry', key: e.key, entry: e });
    }
    return out;
  }, [pinnedKeys, cardsByKey, entries]);

  // Empty tray — return null. An "no activity" placeholder would be
  // pure visual clutter for a feature whose whole point is being
  // ignorable when nothing's happening.
  if (rows.length === 0) return null;

  const totalCount = rows.length;

  if (collapsed) {
    return (
      <button
        type="button"
        className="mk-tray-badge mk-pill mk-status-doing"
        onClick={() => setCollapsedPersisted(false)}
        title="Show activity tray"
      >
        Activity · {totalCount}
      </button>
    );
  }

  return (
    <aside className="mk-tray" aria-label="Recent board activity">
      <div className="mk-tray-head">
        <span className="mk-tray-title">Activity</span>
        <span className="mk-tray-count">{totalCount}</span>
        <button
          type="button"
          className="mk-icbtn mk-tray-collapse"
          onClick={() => setCollapsedPersisted(true)}
          title="Minimise activity tray"
          aria-label="Minimise activity tray"
        >
          –
        </button>
      </div>
      {/*
        BACI-208: single unified list — pinned rows on top (so a noisy
        board can't push them off, replacing BACI-192's separate
        section + header without losing the visibility guarantee),
        then auto-driven transitions in LIFO order. Pinned rows show
        the live card snapshot and their × calls onTogglePin (unpin);
        entry rows show the immutable-at-insert-time fields with a
        live status row (BACI-183) and their × calls dismiss.
      */}
      <ul className="mk-tray-list">
        {rows.map(row => {
          if (row.kind === 'pinned') {
            // Pinned row: read straight from the live card snapshot
            // (the entry-form's frozen columnLabel doesn't apply —
            // the user pinned the *card*, not a transition). No flash
            // for pinned rows; they're sticky, not transitions.
            const { card } = row;
            const status = entryStatus(card);
            return (
              <li
                key={`pin-${card.key}`}
                className="mk-tray-entry"
                onClick={() => {
                  if (onOpenCard) onOpenCard(card);
                  if (onCardClickFromTray) onCardClickFromTray(card.key);
                }}
                onMouseEnter={() => onHoverCard && onHoverCard(card.key)}
                onMouseLeave={() => onHoverCard && onHoverCard(null)}
                role={onOpenCard ? 'button' : undefined}
                tabIndex={onOpenCard ? 0 : undefined}
                onKeyDown={(e) => {
                  if ((e.key === 'Enter' || e.key === ' ') && onOpenCard) {
                    e.preventDefault();
                    onOpenCard(card);
                    if (onCardClickFromTray) onCardClickFromTray(card.key);
                  }
                }}
              >
                <div className="mk-tray-entry-head">
                  <span className="mk-card-id">{card.key}</span>
                  <span className={`mk-pill ${stateClass(card.column)}`}>{card.columnLabel || card.column}</span>
                  <button
                    type="button"
                    className="mk-tray-dismiss"
                    onClick={(e) => {
                      e.stopPropagation();
                      if (onTogglePin) onTogglePin(card.key);
                    }}
                    title="Unpin"
                    aria-label={`Unpin ${card.key}`}
                  >
                    ×
                  </button>
                </div>
                <div className="mk-tray-entry-title">{card.title}</div>
                {status.kind === 'needs_action' && (
                  <div className="mk-tray-entry-status is-needs-action">
                    <Icon name="alert-triangle" />
                    <span>{status.text}</span>
                  </div>
                )}
                {status.kind === 'verb' && (
                  <div className="mk-tray-entry-status">
                    <span className="mk-tray-entry-verb">{status.text}</span>
                  </div>
                )}
                {status.kind === 'waiting' && (
                  <div className="mk-tray-entry-status is-waiting">
                    <span>{status.text}</span>
                  </div>
                )}
              </li>
            );
          }
          // Entry row (auto-fed transition).
          const e = row.entry;
          // BACI-183: read the live card for the status row. Falls
          // back to undefined when the card dropped from `cards`
          // between polls — entryStatus handles that by returning
          // `{kind:'none'}`, so the entry still renders its title.
          // Column on the head pill stays on the Entry's
          // immutable-at-insert-time snapshot (we want the head to
          // reflect the *transition* that put this row in the tray,
          // not the column the card moved to afterwards).
          const liveCard = cardsByKey.get(e.key);
          const status = entryStatus(liveCard);
          // BACI-197: Recent rows now mirror the pinned-row affordances
          // — hover lights up the matching board card, click opens the
          // workspace and bounces the card. The card we hand to
          // onOpenCard prefers the live snapshot from cardsByKey (so
          // the workspace lands on the freshest column/state) and falls
          // back to a synthesised {key, title} shape when the card
          // dropped from the latest poll, so the workspace still opens.
          const openTarget = liveCard || { key: e.key, title: e.title };
          return (
            <li
              key={e.key}
              className={`mk-tray-entry${flashingKeys.has(e.key) ? ' is-flashing' : ''}`}
              onClick={() => {
                if (onOpenCard) onOpenCard(openTarget);
                if (onCardClickFromTray) onCardClickFromTray(e.key);
              }}
              onMouseEnter={() => onHoverCard && onHoverCard(e.key)}
              onMouseLeave={() => onHoverCard && onHoverCard(null)}
              role={onOpenCard ? 'button' : undefined}
              tabIndex={onOpenCard ? 0 : undefined}
              onKeyDown={(ev) => {
                if ((ev.key === 'Enter' || ev.key === ' ') && onOpenCard) {
                  ev.preventDefault();
                  onOpenCard(openTarget);
                  if (onCardClickFromTray) onCardClickFromTray(e.key);
                }
              }}
            >
              <div className="mk-tray-entry-head">
                <span className="mk-card-id">{e.key}</span>
                <span className={`mk-pill ${stateClass(e.column)}`}>{e.columnLabel}</span>
                <button
                  type="button"
                  className="mk-tray-dismiss"
                  onClick={(ev) => { ev.stopPropagation(); dismiss(e.key); }}
                  title="Dismiss"
                  aria-label={`Dismiss ${e.key}`}
                >
                  ×
                </button>
              </div>
              <div className="mk-tray-entry-title">{e.title}</div>
              {status.kind === 'needs_action' && (
                <div className="mk-tray-entry-status is-needs-action">
                  <Icon name="alert-triangle" />
                  <span>{status.text}</span>
                </div>
              )}
              {status.kind === 'verb' && (
                <div className="mk-tray-entry-status">
                  <span className="mk-tray-entry-verb">{status.text}</span>
                </div>
              )}
              {status.kind === 'waiting' && (
                <div className="mk-tray-entry-status is-waiting">
                  <span>{status.text}</span>
                </div>
              )}
            </li>
          );
        })}
      </ul>
    </aside>
  );
}

// MemoEntries: not currently memoised — at ~10 visible entries the
// render cost is negligible and a React.memo wrapper would add an
// equality-check overhead that doesn't pay off. If the cap ever
// grows to 50+, revisit.
