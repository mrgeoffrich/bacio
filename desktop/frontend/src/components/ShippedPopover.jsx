import React, { useEffect, useRef, useState } from 'react';
import { m } from 'motion/react';
import { reportError } from '../errors';
import * as api from '../api';
import { formatWhen } from '../lib/formatWhen';

// CACHE_TTL_MS: how long the popover holds onto its last successful
// fetch before refetching on the next open. Thirty seconds is long
// enough that rapid open/close doesn't thrash the network, short
// enough that a freshly-shipped issue surfaces on the next open.
const CACHE_TTL_MS = 30_000;
// SINCE_DAYS / LIMIT: the window + cap the popover asks for. The
// server clamps both — these are sensible defaults for the "what
// shipped lately" glance the ticket asked for.
const SINCE_DAYS = 30;
const LIMIT = 20;

// ShippedPopover (BACI-187) is the topbar's "Shipped · N" pill plus
// its anchored menu of recently-done issues. Hand-rolled rather than
// pulling Radix Popover — matches the lightweight ref + mousedown +
// Escape recipe RepoPicker already uses in this codebase, and keeps
// the dep footprint flat.
//
// Props:
//   activeBoard  — current repo prefix; empty / "all" disables the pill.
//   shippedCount — pre-computed count for the "Shipped · N" label;
//                  derived in App.jsx via useMemo over the polled cards.
//   onOpenIssue  — invoked with the row's canonical key on click;
//                  the popover closes itself afterwards.
//   flyingShipKey — BACI-193: when set, the popover renders an
//                  absolutely-positioned destination slot inside the
//                  pill with a matching Motion `layoutId`, so a card
//                  leaving the kanban flies into the pill. Cleared
//                  by App once the destination reports completion.
//   shipFlashing — BACI-193: true for ~520ms after a flight lands.
//                  Toggles `.is-flash` on the pill so it pulses.
//   onShipFlightDone — BACI-193: invoked by the destination slot's
//                  `onLayoutAnimationComplete`. Triggers the flash.
export default function ShippedPopover({ activeBoard, shippedCount, onOpenIssue, flyingShipKey, shipFlashing, onShipFlightDone }) {
  const [open, setOpen] = useState(false);
  // status: 'idle' | 'loading' | 'ready' | 'error'
  // rows is the last successful fetch (preserved across closes so the
  // next open paints instantly if we're inside the cache window).
  // fetchedAt is the wall-clock of the last successful fetch.
  const [status, setStatus] = useState('idle');
  const [rows, setRows] = useState([]);
  const [error, setError] = useState('');
  const [fetchedAt, setFetchedAt] = useState(0);

  const rootRef = useRef(null);

  // Outside-click + Escape — same recipe as RepoPicker.jsx.
  useEffect(() => {
    if (!open) return;
    const onDown = (e) => {
      if (rootRef.current && !rootRef.current.contains(e.target)) setOpen(false);
    };
    const onKey = (e) => {
      if (e.key === 'Escape') setOpen(false);
    };
    document.addEventListener('mousedown', onDown);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onDown);
      document.removeEventListener('keydown', onKey);
    };
  }, [open]);

  // Switching repos invalidates the cache — a popover opened against
  // repo A then re-opened against repo B must refetch, not reuse the
  // stale rows.
  useEffect(() => {
    setStatus('idle');
    setRows([]);
    setError('');
    setFetchedAt(0);
  }, [activeBoard]);

  // First-open (or cache-expired) fetch. Runs only when we just
  // opened — closing the popover preserves the rows so a fast
  // re-open paints from cache.
  useEffect(() => {
    if (!open) return;
    if (!activeBoard || activeBoard === 'all') return;
    const fresh = Date.now() - fetchedAt < CACHE_TTL_MS;
    if (status === 'ready' && fresh) return;
    let cancelled = false;
    setStatus('loading');
    setError('');
    api.listShippedIssues(activeBoard, SINCE_DAYS, LIMIT)
      .then((next) => {
        if (cancelled) return;
        setRows(next ?? []);
        setStatus('ready');
        setFetchedAt(Date.now());
      })
      .catch((err) => {
        if (cancelled) return;
        // Route through reportError so the unified failure log records
        // the headline, then surface inline too so the popover stays
        // self-contained — an interrupting global modal would feel
        // out of proportion for a topbar affordance.
        reportError(err, { headline: "Couldn't load shipped issues" });
        setError(err?.message || String(err));
        setStatus('error');
      });
    return () => { cancelled = true; };
  }, [open, activeBoard, status, fetchedAt]);

  // Click a row → open the issue workspace + close the popover.
  const pickRow = (key) => {
    if (typeof onOpenIssue === 'function') onOpenIssue(key);
    setOpen(false);
  };

  // Retry the fetch without closing the popover. Just reset to idle —
  // the open-effect re-runs because (status, fetchedAt) changed.
  const retry = () => {
    setFetchedAt(0);
    setStatus('idle');
  };

  const disabled = !activeBoard || activeBoard === 'all';
  const pillLabel = shippedCount > 0 ? `Shipped · ${shippedCount}` : 'Shipped';
  const tooltip = shippedCount > 0
    ? `${shippedCount} ${shippedCount === 1 ? 'issue' : 'issues'} shipped in the last 7 days · click for the full list`
    : 'Recently-shipped issues for this repository';

  return (
    <div className="mk-shipped-popover-root" ref={rootRef}>
      <button
        type="button"
        className={`mk-pill mk-shipped-pill${shipFlashing ? ' is-flash' : ''}`}
        title={tooltip}
        disabled={disabled}
        onClick={() => { if (!disabled) setOpen(o => !o); }}
        aria-haspopup="dialog"
        aria-expanded={open}
      >
        {pillLabel}
        {/* BACI-193 ship-flourish destination slot. Mounted only
            while a card is mid-flight (flyingShipKey set); the matching
            layoutId on the kanban card makes Motion animate the card-
            shape from its column position to this slot inside the
            pill. After completion, onShipFlightDone clears the slot
            and triggers .is-flash on the parent pill. */}
        {flyingShipKey && (
          <m.div
            key={flyingShipKey}
            layoutId={flyingShipKey}
            className="mk-shipped-flight-target"
            aria-hidden="true"
            onLayoutAnimationComplete={onShipFlightDone}
          />
        )}
      </button>
      {open && (
        <div className="mk-shipped-popover" role="dialog" aria-label="Recently shipped issues">
          <div className="mk-shipped-popover-header">
            Recently shipped
          </div>
          <div className="mk-shipped-popover-body">
            {status === 'loading' && (
              // Skeleton: three muted placeholder rows so the popover
              // doesn't reflow when the real data arrives.
              [0, 1, 2].map((i) => (
                <div key={i} className="mk-shipped-row is-skeleton">
                  <span className="mk-shipped-row-key" />
                  <span className="mk-shipped-row-title" />
                </div>
              ))
            )}
            {status === 'error' && (
              <div className="mk-shipped-popover-empty">
                Couldn’t load shipped issues.{' '}
                <button type="button" className="mk-link" onClick={retry}>Retry</button>
                {error && <div className="mk-shipped-popover-error-detail">{error}</div>}
              </div>
            )}
            {status === 'ready' && rows.length === 0 && (
              <div className="mk-shipped-popover-empty">
                Nothing shipped yet — drag a card to Done.
              </div>
            )}
            {status === 'ready' && rows.length > 0 && rows.map((r) => (
              <button
                key={r.key}
                type="button"
                className="mk-shipped-row"
                onClick={() => pickRow(r.key)}
                title={r.title}
              >
                <span className="mk-shipped-row-top">
                  <span className="mk-shipped-row-key mk-card-id">{r.key}</span>
                  <span className="mk-shipped-row-title">{r.title}</span>
                  <span className="mk-shipped-row-when">{formatWhen(r.terminalAt)}</span>
                </span>
              </button>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}
