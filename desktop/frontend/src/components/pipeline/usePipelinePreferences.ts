import { useEffect } from 'react';
import { useOptimisticToggle } from '../../lib/hooks/useOptimisticToggle';
import * as api from '../../api';

// PipelinePreferenceHandlers — the three per-repo display-preference
// persisters the board wires from useCards(). All optional, matching the
// handler shape on the board.
export type PipelinePreferenceHandlers = {
  onSetBacklogCollapsed?: (next: boolean) => void;
  onSetAutoShip?: (next: boolean) => void;
  onSetImpactPrimary?: (next: boolean) => void;
};

// PipelinePreferences — the three per-repo Pipeline display toggles, each an
// optimistic boolean (flip on screen, persist, silent-revert on failure).
export type PipelinePreferences = {
  // collapsed (BACI-288) — Backlog shrunk to a thin rail.
  collapsed: boolean;
  toggleCollapsed: () => void;
  // autoShip — the per-repo Shipping-column auto-ship.
  autoShip: boolean;
  toggleAutoShip: () => void;
  // impactPrimary (BACI-349) — customer-impact as the card headline.
  impactPrimary: boolean;
  toggleImpactPrimary: () => void;
};

// usePipelinePreferences owns the three per-repo Pipeline display toggles
// (BACI-357 optimistic flip + persist + silent revert via useOptimisticToggle)
// and seeds each from the backend GET on the active board so the toggle
// reflects the DB source of truth across machines — not a local cache. The
// fetch effects re-seed whenever the active board changes and reset to off
// when there's no board.
export function usePipelinePreferences(
  activeBoard: string,
  handlers: PipelinePreferenceHandlers,
): PipelinePreferences {
  const { onSetBacklogCollapsed, onSetAutoShip, onSetImpactPrimary } = handlers;

  // collapsed (BACI-288) shrinks the Backlog column to a thin rail so the
  // In Pipeline + Shipping columns get the full width. Persisted per-repo
  // in the tui_settings KV — seeded from the backend GET. It overrides the
  // transient `expanded` grid toggle (handled in the shell).
  const { value: collapsed, setValue: setCollapsed, toggle: toggleCollapsed } =
    useOptimisticToggle(false, (next) => onSetBacklogCollapsed?.(next));
  // autoShip seeds from the backend (GET /auto-ship) — the DB value the
  // controller's auto-ship ticker actually acts on — so the toggle
  // reflects the source of truth across machines, not a local cache.
  const { value: autoShip, setValue: setAutoShip, toggle: toggleAutoShip } =
    useOptimisticToggle(false, (next) => onSetAutoShip?.(next));
  // impactPrimary (BACI-349) flips every card from title-primary (the
  // default) to customer-impact-primary for cards whose customer_impact is
  // set. Per-repo display chrome, seeded from the backend GET — same pattern
  // as autoShip / collapsed.
  const { value: impactPrimary, setValue: setImpactPrimary, toggle: toggleImpactPrimary } =
    useOptimisticToggle(false, (next) => onSetImpactPrimary?.(next));

  useEffect(() => {
    if (!activeBoard) { setAutoShip(false); return; }
    let cancelled = false;
    api.getAutoShip(activeBoard)
      .then((v) => { if (!cancelled) setAutoShip(!!v); })
      .catch(() => { if (!cancelled) setAutoShip(false); });
    return () => { cancelled = true; };
  }, [activeBoard, setAutoShip]);

  useEffect(() => {
    if (!activeBoard) { setCollapsed(false); return; }
    let cancelled = false;
    api.getBacklogCollapsed(activeBoard)
      .then((v) => { if (!cancelled) setCollapsed(!!v); })
      .catch(() => { if (!cancelled) setCollapsed(false); });
    return () => { cancelled = true; };
  }, [activeBoard, setCollapsed]);

  useEffect(() => {
    if (!activeBoard) { setImpactPrimary(false); return; }
    let cancelled = false;
    api.getImpactPrimary(activeBoard)
      .then((v) => { if (!cancelled) setImpactPrimary(!!v); })
      .catch(() => { if (!cancelled) setImpactPrimary(false); });
    return () => { cancelled = true; };
  }, [activeBoard, setImpactPrimary]);

  return {
    collapsed, toggleCollapsed,
    autoShip, toggleAutoShip,
    impactPrimary, toggleImpactPrimary,
  };
}
