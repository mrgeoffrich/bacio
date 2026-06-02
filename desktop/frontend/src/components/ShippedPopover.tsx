import { useEffect, useRef, useState } from 'react';
import { reportError } from '../errors';
import * as api from '../api';
import type { ShippedIssueDTO } from '../api';
import { formatWhen } from '../lib/formatWhen';
import { SHIPPED_SCOPES, scopeLabel, scopeSinceParams } from './shippedScope.ts';
import type { ShippedScope } from './shippedScope.ts';
import ShippedPill from './ShippedPill';

// CACHE_TTL_MS: how long the popover holds onto its last successful
// fetch before refetching on the next open. Thirty seconds is long
// enough that rapid open/close doesn't thrash the network, short
// enough that a freshly-shipped issue surfaces on the next open.
const CACHE_TTL_MS = 30_000;
// LIMIT: the per-fetch row cap the popover asks the server for. The
// server clamps it independently. The scope picker drives `sinceDays`
// — see scopeSinceDays — so this file no longer hard-codes a window.
const LIMIT = 20;

// ShippedPopover (BACI-187, BACI-221) is the Pipeline Shipping column's
// "Shipped · N" pill plus its anchored menu of recently-done issues. Hand-rolled
// rather than pulling Radix Popover — matches the lightweight ref +
// mousedown + Escape recipe RepoPicker already uses in this codebase,
// and keeps the dep footprint flat.
//
// Props:
//   activeBoard  — current repo prefix; empty / "all" disables the pill.
//   shippedCount — pre-computed count for the "Shipped · N" label;
//                  App.tsx polls /shipped/count under the active scope
//                  on the same 10s cadence as the other live readouts
//                  so pill and popover never drift.
//   scope        — BACI-221: 'today' | 'week' | 'forever'. Owned by
//                  App.tsx so the pill count and the list scope stay
//                  in lockstep.
//   onScopeChange — App-level setter that also persists the choice to
//                  localStorage. Wired through PipelineView.
//   timezone     — BACI-312: the user's IANA zone (or "" before
//                  auto-detect fills it). Drives the 'today' scope's
//                  local-midnight cutoff via scopeSinceParams.
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
//
// BACI-254: the ship SFX used to live here too, wired to the rising
// edge of `shipFlashing`. That made the audio depend on Motion
// actually completing the flight — which never happens when the
// user is off the Pipeline (Features graph, Settings, …) because the
// source `motion.article` isn't mounted. The SFX has moved up into
// `App.tsx`, where BACI-295 ties it to the server-derived shippedCount
// rising (the count-rise effect) so it fires regardless of the active
// view. The visual flash stays put — it does genuinely depend on the
// flight landing.
type ShippedPopoverStatus = 'idle' | 'loading' | 'ready' | 'error';

type ShippedPopoverProps = {
  activeBoard: string;
  shippedCount: number | null;
  scope: ShippedScope;
  onScopeChange: (next: ShippedScope) => void;
  timezone: string;
  onOpenIssue: (key: string) => void;
  flyingShipKey: string | null;
  shipFlashing: boolean;
  onShipFlightDone: () => void;
};

export default function ShippedPopover({ activeBoard, shippedCount, scope, onScopeChange, timezone, onOpenIssue, flyingShipKey, shipFlashing, onShipFlightDone }: ShippedPopoverProps) {
  const [open, setOpen] = useState(false);
  // status: 'idle' | 'loading' | 'ready' | 'error'
  // rows is the last successful fetch (preserved across closes so the
  // next open paints instantly if we're inside the cache window).
  // total is the BACI-221 server-side count under the active scope;
  // fetchedAt is the wall-clock of the last successful fetch.
  const [status, setStatus] = useState<ShippedPopoverStatus>('idle');
  const [rows, setRows] = useState<ShippedIssueDTO[]>([]);
  const [total, setTotal] = useState(0);
  const [error, setError] = useState('');
  const [fetchedAt, setFetchedAt] = useState(0);

  const rootRef = useRef<HTMLDivElement>(null);

  // Outside-click + Escape — same recipe as RepoPicker.tsx.
  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (rootRef.current && !rootRef.current.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false);
    };
    document.addEventListener('mousedown', onDown);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onDown);
      document.removeEventListener('keydown', onKey);
    };
  }, [open]);

  // Switching repos OR switching scopes OR changing the timezone (which
  // moves the 'today' midnight cutoff) invalidates the cache — the
  // popover must refetch under the new conditions rather than show
  // stale rows. Same shape as the existing activeBoard invalidation
  // effect; scope + timezone are just further axes the cache depends on.
  useEffect(() => {
    setStatus('idle');
    setRows([]);
    setTotal(0);
    setError('');
    setFetchedAt(0);
  }, [activeBoard, scope, timezone]);

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
    // BACI-312: 'today' resolves to an absolute local-midnight cutoff in
    // the user's timezone; week/forever keep the relative sinceDays window.
    const { sinceDays, sinceTs } = scopeSinceParams(scope, timezone);
    api.listShippedIssues(activeBoard, sinceDays, sinceTs, LIMIT)
      .then((next) => {
        if (cancelled) return;
        setRows(next?.rows ?? []);
        setTotal(next?.total ?? 0);
        setStatus('ready');
        setFetchedAt(Date.now());
      })
      .catch((err) => {
        if (cancelled) return;
        // Route through reportError so the unified failure log records
        // the headline, then surface inline too so the popover stays
        // self-contained — an interrupting global modal would feel
        // out of proportion for this pill affordance.
        reportError(err, { headline: "Couldn't load shipped issues" });
        setError(err?.message || String(err));
        setStatus('error');
      });
    return () => { cancelled = true; };
  }, [open, activeBoard, scope, timezone, status, fetchedAt]);

  // Click a row → open the issue workspace + close the popover.
  const pickRow = (key: string) => {
    if (typeof onOpenIssue === 'function') onOpenIssue(key);
    setOpen(false);
  };

  // Retry the fetch without closing the popover. Just reset to idle —
  // the open-effect re-runs because (status, fetchedAt) changed.
  const retry = () => {
    setFetchedAt(0);
    setStatus('idle');
  };

  // Scope click: defer to the App-level setter (which also persists),
  // then drop the cache so the open-effect re-fires immediately.
  const pickScope = (next: ShippedScope) => {
    if (next === scope) return;
    if (typeof onScopeChange === 'function') onScopeChange(next);
    // The activeBoard/scope effect above resets cache state on
    // scope change, but we set it here too so a click while open
    // re-fetches without a race against the parent re-render.
    setStatus('idle');
    setFetchedAt(0);
  };

  const disabled = !activeBoard || activeBoard === 'all';
  const safeCount = shippedCount != null && Number.isFinite(shippedCount) && shippedCount > 0 ? shippedCount : 0;
  const tooltip = safeCount > 0
    ? `${safeCount} ${safeCount === 1 ? 'issue' : 'issues'} shipped · click for the full list`
    : 'Recently-shipped issues for this repository';
  const safeScope = scope ?? 'week';

  return (
    <div className="mk-shipped-popover-root" ref={rootRef}>
      <ShippedPill
        count={safeCount}
        disabled={disabled}
        onClick={() => { if (!disabled) setOpen(o => !o); }}
        flashing={shipFlashing}
        flyingKey={flyingShipKey}
        onFlightDone={onShipFlightDone}
        title={tooltip}
        ariaHasPopup="dialog"
        ariaExpanded={open}
      />
      {open && (
        <div className="mk-shipped-popover" role="dialog" aria-label="Recently shipped issues">
          {/* BACI-244: the "Recently shipped" header strip was removed —
              the trigger pill ("Shipped · N") already labels the
              popover, so the inner header was duplicative chrome.
              BACI-221 scope picker: segmented strip of Today / Last
              Week / Forever buttons. Renders above the body so the
              picker is one tab-stop above the rows. */}
          <div className="mk-shipped-scope-strip" role="tablist" aria-label="Shipped time scope">
            {SHIPPED_SCOPES.map((s) => (
              <button
                key={s}
                type="button"
                role="tab"
                aria-selected={safeScope === s}
                className={`mk-shipped-scope-button${safeScope === s ? ' is-active' : ''}`}
                onClick={() => pickScope(s)}
              >
                {scopeLabel(s)}
              </button>
            ))}
          </div>
          <div className="mk-shipped-popover-body">
            {status === 'loading' && (
              // Skeleton: three muted placeholder rows so the popover
              // doesn't reflow when the real data arrives. BACI-244
              // mirrors the new two-line shape — a wide title bar on
              // the title-line and a short KEY · when bar on the
              // sub-line — so the placeholder heights match the real
              // rows and the body height settles cleanly when data
              // lands.
              [0, 1, 2].map((i) => (
                <div key={i} className="mk-shipped-row is-skeleton">
                  <span className="mk-shipped-row-title-line">
                    <span className="mk-shipped-row-title" />
                  </span>
                  <span className="mk-shipped-row-sub-line">
                    <span className="mk-shipped-row-key" />
                    <span className="mk-shipped-row-when" />
                  </span>
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
                Nothing shipped in this window — try a wider scope.
              </div>
            )}
            {status === 'ready' && rows.length > 0 && rows.map((r) => (
              // BACI-244: row shape is now a two-line stack mirroring
              // ActivityTray's entry pattern — title-line carries
              // [feature emoji] [title] with the title getting the
              // dominant 13px weight, sub-line carries [KEY] · [when]
              // in muted mono micro-type. The PR pill and feature-slug
              // text are dropped (the row click already opens the
              // issue; the emoji alone signals the feature).
              <button
                key={r.key}
                type="button"
                className="mk-shipped-row"
                onClick={() => pickRow(r.key)}
                title={r.title}
              >
                <span className="mk-shipped-row-title-line">
                  {r.featureEmoji && (
                    <span className="mk-shipped-row-feature" aria-hidden="true">{r.featureEmoji}</span>
                  )}
                  <span className="mk-shipped-row-title">{r.title}</span>
                </span>
                <span className="mk-shipped-row-sub-line">
                  <span className="mk-shipped-row-key mk-card-id">{r.key}</span>
                  <span className="mk-shipped-row-sub-sep" aria-hidden="true">·</span>
                  <span className="mk-shipped-row-when">{formatWhen(r.terminalAt)}</span>
                </span>
              </button>
            ))}
          </div>
          {/* Footer: "showing N of TOTAL" when the list was capped by
              the server-side limit. Hidden when total <= LIMIT so an
              uncapped list doesn't get a confusing "showing 3 of 3"
              chrome. */}
          {status === 'ready' && rows.length > 0 && total > rows.length && (
            <div className="mk-shipped-popover-footer">
              Showing {rows.length} of {total}
            </div>
          )}
        </div>
      )}
    </div>
  );
}
