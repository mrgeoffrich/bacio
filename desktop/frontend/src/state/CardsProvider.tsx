import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react';
import type { Dispatch, ReactNode, SetStateAction } from 'react';
import * as api from '../api';
import type { BoardCard, WaitingState } from '../api';
import type { WaitingKind } from '../api';
import type { DispatchMode, State } from '../../bindings/github.com/mrgeoffrich/bacio/internal/model';
import type { ProcessSelection } from '../lib/pipelineProcesses';
import type { ShippedScope } from '../components/shippedScope.ts';
import { reportError } from '../errors';
import { isTerminalState, stripBlockerFromCards, restoreBlockedByFromSnapshot } from '../lib/issueState';
import { useAsyncResource } from '../lib/hooks/useAsyncResource';
import { useInterval, POLL_INTERVAL_MS } from '../lib/hooks/useInterval';
import { useLocalStorage } from '../lib/hooks/useLocalStorage';
import { useOptimisticMutation } from '../lib/hooks/useOptimisticMutation';
import { useShipFlourish } from '../lib/shipFlourish';
import { useShipSfx } from '../lib/shipSfx';
import { decideOdometerAction } from '../lib/odometer';
import { STORAGE_KEY as SHIPPED_SCOPE_KEY, DEFAULT_SCOPE, shippedScopeCodec } from '../components/shippedScopePersistence.ts';
import { scopeSinceParams } from '../components/shippedScope.ts';
import { useActiveRepo } from './RepoProvider';
import { usePreferences } from './PreferencesProvider';

// CardsProvider (BACI-361) owns the hot (10s-polled) board-cards state
// App.tsx used to carry: the per-repo cards resource, the ~22 user-driven
// mutation handlers (drag-move, dispatch, the Pipeline engine controls,
// ship / mark-done / cancel), the BACI-193 ship flourish, and the
// server-derived Shipped count + its scope + ka-ching SFX. It nests inside
// RepoProvider (reads `activeBoard` / `activeView`) and PreferencesProvider
// (reads `timezone` / `audioEnabled`), and is the innermost data provider —
// the Shell renders inside it.

export type CardsContextValue = {
  cards: BoardCard[];
  setCards: Dispatch<SetStateAction<BoardCard[]>>;
  refreshCards: (opts?: { silent?: boolean }) => void;
  // User-driven card mutations (lifted verbatim from App).
  moveCard: (key: string, toCol: string) => void;
  dispatchFromCard: (cardKey: string, mode: string) => void;
  cancelWaitingFromCard: (cardKey: string) => void;
  setCardProcess: (key: string, selection: ProcessSelection) => void;
  setCardProcessAuto: (key: string, selection: ProcessSelection) => Promise<void>;
  editCardProcessTail: (key: string, stages: string[]) => Promise<void>;
  editCardProcessTailAuto: (key: string, stages: string[]) => Promise<void>;
  startCardJob: (key: string) => void;
  stopCardJob: (key: string) => void;
  resetCardProcess: (key: string) => void;
  rerunCardJob: (key: string, seq: number) => void;
  setCardEngineMode: (key: string, mode: string) => void;
  fastTrackCard: (key: string) => Promise<void>;
  shipCardFromPipeline: (key: string) => void;
  markDoneCardFromPipeline: (key: string) => void;
  cancelCardFromPipeline: (key: string) => void;
  doneCardFromPipeline: (key: string) => void;
  reorderPipelineCard: (key: string, position: number) => void;
  onBlockCard: (sourceKey: string, targetKey: string) => void;
  setRepoAutoShip: (enabled: boolean) => Promise<unknown>;
  setBacklogCollapsed: (collapsed: boolean) => Promise<unknown>;
  setImpactPrimary: (impactPrimary: boolean) => Promise<unknown>;
  // Ship flourish + server-derived Shipped count / scope (Topbar reads these).
  flyingShipKey: string | null;
  shipFlashing: boolean;
  onShipFlightDone: () => void;
  shippedCount: number | null;
  shippedScope: ShippedScope;
  setShippedScope: Dispatch<SetStateAction<ShippedScope>>;
};

const CardsContext = createContext<CardsContextValue | null>(null);

export function CardsProvider({ children }: { children: ReactNode }) {
  const { activeBoard, activeView } = useActiveRepo();
  const { timezone, audioEnabled } = usePreferences();

  // BACI-357: the snapshot → optimistic update → persist → reconcile/rollback
  // → reportError orchestrator the card mutation handlers below route through.
  const mutate = useOptimisticMutation();

  // cards is the App-owned card list for the active repo, sourced from
  // useAsyncResource (BACI-356). The hook owns the list state + the
  // stale-load guard and eager-loads on every repo change — it stays loaded
  // regardless of the active view (CommandPalette reads cards, IssueWorkspace
  // reads them for prev/next siblings). `setCards` keeps the optimistic flips
  // in the mutation handlers working.
  const {
    data: cards,
    setData: setCards,
    refresh: refreshCards,
  } = useAsyncResource<BoardCard[]>(
    () => api.listCards(activeBoard),
    [],
    [activeBoard],
    { enabled: !!activeBoard, errorHeadline: "Couldn't refresh board" },
  );

  // Re-fetch on switch to the Board / Pipeline screens so they're fresh on
  // arrival. Repo switches are handled by the eager load above, so
  // activeBoard is read via closure rather than listed as a dep.
  useEffect(() => {
    if (!activeBoard) return;
    if (activeView === 'board' || activeView === 'pipeline') refreshCards();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeView, refreshCards]);

  // Poll the Board / Pipeline screens every 10s so they don't go stale while
  // open. `enabled` is false off those screens / before a board is picked,
  // which tears the timer down — no leaks, no redundant off-screen fetches.
  useInterval(
    () => refreshCards({ silent: true }),
    POLL_INTERVAL_MS,
    !!activeBoard && (activeView === 'board' || activeView === 'pipeline'),
  );

  // ─── Card mutation handlers (lifted verbatim from App.tsx) ──────────────

  // Drag-to-move: optimistically move the card to the new column, then
  // persist. On failure, revert. BACI-146: a terminal landing also strips
  // the card's key from every sibling's blockedBy so the lock clears
  // immediately; the server runs the same rule, so this is a convergence
  // shortcut the next refresh re-asserts.
  const moveCard = useCallback((key: string, toCol: string) => {
    let prevCol: string | null = null;
    let prevBlockedBy: Map<string, BoardCard['blockedBy']> | null = null;
    mutate({
      optimisticUpdate: () => {
        setCards(cs => {
          const prev = cs.find(c => c.key === key);
          if (!prev || prev.column === toCol) return cs;
          prevCol = prev.column;
          const enteringTerminal = isTerminalState(toCol) && !isTerminalState(prevCol);
          if (enteringTerminal) {
            prevBlockedBy = new Map();
            for (const c of cs) {
              if ((c.blockedBy || []).some(b => b.key === key)) {
                prevBlockedBy.set(c.key, c.blockedBy ?? []);
              }
            }
          }
          const moved = cs.map(c => c.key === key ? { ...c, column: toCol } : c);
          return enteringTerminal ? stripBlockerFromCards(moved, key) : moved;
        });
        return prevCol !== null;
      },
      persist: () => api.setIssueState(activeBoard, key, toCol),
      onSuccess: () => {
        if (isTerminalState(prevCol!) !== isTerminalState(toCol)) {
          refreshCards();
        }
      },
      rollback: () => {
        const fromCol = prevCol!;
        const blockedBySnapshot = prevBlockedBy;
        setCards(cs => {
          const reverted = cs.map(c => c.key === key ? { ...c, column: fromCol } : c);
          return blockedBySnapshot ? restoreBlockedByFromSnapshot(reverted, blockedBySnapshot) : reverted;
        });
      },
      errorHeadline: "Couldn't move card",
    });
  }, [activeBoard, refreshCards, setCards, mutate]);

  // Dispatch a prompt from a card's action button. BACI-145: set an
  // optimistic waitingState before the request so the spinner kicks in on
  // the click; the first poll replaces it with the authoritative state.
  const dispatchFromCard = useCallback((cardKey: string, mode: string) => {
    const optimistic: WaitingState = { kind: 'queued_no_agent' as WaitingKind, mode: mode as DispatchMode };
    mutate({
      optimisticUpdate: () => {
        setCards(cs => cs.map(c => c.key === cardKey ? { ...c, waitingState: optimistic } : c));
      },
      persist: () => api.dispatchIssue(activeBoard, cardKey, mode),
      rollback: () => {
        setCards(cs => cs.map(c => c.key === cardKey ? { ...c, waitingState: null } : c));
      },
      errorHeadline: "Couldn't dispatch agent",
    });
  }, [activeBoard, setCards, mutate]);

  // BACI-51 spinner-as-cancel-button: withdraw a card's queued / pending /
  // delivered dispatch. Optimistically clears the local waitingState; the
  // refresh poll reads the authoritative state.
  const cancelWaitingFromCard = useCallback((cardKey: string) => {
    api.cancelWaitingDispatch(activeBoard, cardKey)
      .then(() => {
        setCards(cs => cs.map(c => c.key === cardKey ? { ...c, waitingState: null } : c));
      })
      .catch(err => reportError(err, { headline: "Couldn't cancel queued dispatch" }));
  }, [activeBoard, setCards]);

  // ─── Pipeline handlers ─────────────────────────────────────────────────

  // Assign a process to a card — the in-pipeline "pick a process" picker.
  const setCardProcess = useCallback((key: string, selection: ProcessSelection) => {
    api.setCardProcess(activeBoard, key, selection)
      .then(() => refreshCards({ silent: true }))
      .catch(err => reportError(err, { headline: "Couldn't set the process" }));
  }, [activeBoard, refreshCards]);

  // BACI-334 "Confirm + Auto" — set the process and turn Auto on in one
  // click (process must land before Auto). The `finally` refresh leaves a
  // coherent partial state if the Auto call fails after the process saved.
  const setCardProcessAuto = useCallback(async (key: string, selection: ProcessSelection) => {
    try {
      await api.setCardProcess(activeBoard, key, selection);
      await api.setEngineMode(activeBoard, key, 'auto');
    } catch (err) {
      reportError(err, { headline: "Couldn't set the process" });
    } finally {
      refreshCards({ silent: true });
    }
  }, [activeBoard, refreshCards]);

  // BACI-294 Edit Process Save: persist the re-ordered pending tail. Returns
  // the promise so ProcessEditor can navigate back on success / show its own
  // error banner (no reportError here — the editor owns the surface).
  const editCardProcessTail = useCallback((key: string, stages: string[]) => {
    return api.editCardProcessTail(activeBoard, key, stages)
      .then(() => refreshCards({ silent: true }));
  }, [activeBoard, refreshCards]);

  // BACI-334 "Save + Auto" — persist the tail then turn Auto on.
  const editCardProcessTailAuto = useCallback((key: string, stages: string[]) => {
    return api.editCardProcessTail(activeBoard, key, stages)
      .then(() => api.setEngineMode(activeBoard, key, 'auto'))
      .then(() => refreshCards({ silent: true }));
  }, [activeBoard, refreshCards]);

  // Manual Start — advance one step (start the next pending job, or run the
  // Ship hand-off when the chain ends in one).
  const startCardJob = useCallback((key: string) => {
    api.startCardJob(activeBoard, key)
      .then(() => refreshCards({ silent: true }))
      .catch(err => reportError(err, { headline: "Couldn't start the job" }));
  }, [activeBoard, refreshCards]);

  // Manual Stop — cancel the running job and halt Auto.
  const stopCardJob = useCallback((key: string) => {
    api.stopCardJob(activeBoard, key)
      .then(() => refreshCards({ silent: true }))
      .catch(err => reportError(err, { headline: "Couldn't stop the job" }));
  }, [activeBoard, refreshCards]);

  // Reset the process (BACI-314) — wipe the card's whole job chain so it
  // drops back to the from-scratch "Pick a process" picker.
  const resetCardProcess = useCallback((key: string) => {
    api.resetCardProcess(activeBoard, key)
      .then(() => refreshCards({ silent: true }))
      .catch(err => reportError(err, { headline: "Couldn't reset the process" }));
  }, [activeBoard, refreshCards]);

  // Per-job Re-run — reset an aborted (stopped) step to pending and
  // re-dispatch it (BACI-291). seq is the job's 1-based sequence.
  const rerunCardJob = useCallback((key: string, seq: number) => {
    api.rerunCardJob(activeBoard, key, seq)
      .then(() => refreshCards({ silent: true }))
      .catch(err => reportError(err, { headline: "Couldn't re-run the job" }));
  }, [activeBoard, refreshCards]);

  // Engine drive-mode toggle ("off" | "auto"). Optimistic flip on the card;
  // the refresh re-asserts.
  const setCardEngineMode = useCallback((key: string, mode: string) => {
    mutate({
      optimisticUpdate: () => {
        setCards(cs => cs.map(c => c.key === key ? { ...c, engineMode: mode } : c));
      },
      persist: () => api.setEngineMode(activeBoard, key, mode),
      onSuccess: () => refreshCards({ silent: true }),
      rollback: () => refreshCards({ silent: true }),
      errorHeadline: "Couldn't change the drive mode",
    });
  }, [activeBoard, refreshCards, setCards, mutate]);

  // Fast-track (BACI-311) — collapse the manual drag → pick-process →
  // toggle-Auto flow into one click on a Backlog card, in strict order.
  const fastTrackCard = useCallback(async (key: string) => {
    try {
      await api.setIssueState(activeBoard, key, 'in_pipeline');
      await api.setCardProcess(activeBoard, key, { process: 'plan-implement-ship' });
      await api.setEngineMode(activeBoard, key, 'auto');
    } catch (err) {
      reportError(err, { headline: "Couldn't fast-track the card" });
    } finally {
      refreshCards({ silent: true });
    }
  }, [activeBoard, refreshCards]);

  // Ship hand-off — move an in_pipeline card to to_be_shipped (the ship agent
  // fires from the Shipping column). Optimistic column move mirrors moveCard.
  const shipCardFromPipeline = useCallback((key: string) => {
    let prevCol: string | null = null;
    mutate({
      optimisticUpdate: () => {
        setCards(cs => cs.map(c => {
          if (c.key !== key) return c;
          prevCol = c.column;
          return { ...c, column: 'to_be_shipped' };
        }));
      },
      persist: () => api.shipCard(activeBoard, key),
      onSuccess: () => refreshCards({ silent: true }),
      rollback: () => {
        const fromCol = prevCol;
        if (fromCol) setCards(cs => cs.map(c => c.key === key ? { ...c, column: fromCol } : c));
      },
      errorHeadline: "Couldn't ship the card",
    });
  }, [activeBoard, refreshCards, setCards, mutate]);

  // BACI-352: Mark-done hand-off — close an in_pipeline card out as done
  // directly, bypassing the Shipping column. Optimistic column flip to
  // 'done', then a non-silent refresh to reconcile.
  const markDoneCardFromPipeline = useCallback((key: string) => {
    let prevCol: string | null = null;
    mutate({
      optimisticUpdate: () => {
        setCards(cs => cs.map(c => {
          if (c.key !== key) return c;
          prevCol = c.column;
          return { ...c, column: 'done' };
        }));
      },
      persist: () => api.markDoneCard(activeBoard, key),
      onSuccess: () => refreshCards(),
      rollback: () => {
        const fromCol = prevCol;
        if (fromCol) setCards(cs => cs.map(c => c.key === key ? { ...c, column: fromCol } : c));
      },
      errorHeadline: "Couldn't mark the card done",
    });
  }, [activeBoard, refreshCards, setCards, mutate]);

  // BACI-268 trash-bin drag-to-cancel — routes through the terminal-move
  // path (moveCard handles the optimistic flip, blocker-strip, rollback).
  const cancelCardFromPipeline = useCallback((key: string) => {
    moveCard(key, 'cancelled');
  }, [moveCard]);

  // BACI-330 drag-to-done zone — twin of cancelCardFromPipeline.
  const doneCardFromPipeline = useCallback((key: string) => {
    moveCard(key, 'done');
  }, [moveCard]);

  // Backlog / Shipping drag-to-reorder. position is 1-based within the
  // card's (repo, state) band. PipelineView handles the optimistic in-list
  // move during the drag; this persists + reconciles.
  const reorderPipelineCard = useCallback((key: string, position: number) => {
    api.reorderCard(activeBoard, key, position)
      .then(() => refreshCards({ silent: true }))
      .catch(err => {
        reportError(err, { headline: "Couldn't reorder" });
        refreshCards({ silent: true });
      });
  }, [activeBoard, refreshCards]);

  // BACI-342 drag-to-block: dropping a card's blocked-by chip onto another
  // card marks the dragged card (sourceKey) blocked by the drop target
  // (targetKey). A `blocks` edge is { from: targetKey, to: sourceKey }.
  // Optimistically splice the target into the source's blockedBy; the silent
  // refresh re-asserts, a failure reverts via reportError.
  const onBlockCard = useCallback((sourceKey: string, targetKey: string) => {
    if (!sourceKey || !targetKey || sourceKey === targetKey) return;
    let prevBlockedBy: BoardCard['blockedBy'] | null = null;
    mutate({
      optimisticUpdate: () => {
        setCards(cs => {
          const source = cs.find(c => c.key === sourceKey);
          const target = cs.find(c => c.key === targetKey);
          if (!source || !target) return cs;
          const existing = source.blockedBy || [];
          if (existing.some(b => b.key === targetKey)) return cs; // already blocked — no-op
          prevBlockedBy = existing;
          const nextBlockedBy = [...existing, { key: targetKey, state: target.column as State }];
          return cs.map(c => c.key === sourceKey ? { ...c, blockedBy: nextBlockedBy } : c);
        });
        return prevBlockedBy !== null; // guard tripped — nothing to persist
      },
      persist: () => api.createRelation(activeBoard, targetKey, sourceKey),
      onSuccess: () => refreshCards({ silent: true }),
      rollback: () => {
        if (prevBlockedBy === null) return;
        const snapshot = prevBlockedBy;
        setCards(cs => cs.map(c => c.key === sourceKey ? { ...c, blockedBy: snapshot } : c));
      },
      errorHeadline: "Couldn't link cards",
    });
  }, [activeBoard, refreshCards, setCards, mutate]);

  // Per-repo Shipping auto-ship toggle. PipelineView owns the display state
  // (seeded from localStorage); this persists the change and returns the
  // promise so the view can revert its optimistic flip on failure.
  const setRepoAutoShip = useCallback((enabled: boolean) => {
    return api.setAutoShip(activeBoard, enabled)
      .catch(err => { reportError(err, { headline: "Couldn't toggle auto-ship" }); throw err; });
  }, [activeBoard]);

  // Per-repo Pipeline Backlog-column collapse preference (BACI-288).
  const setBacklogCollapsed = useCallback((collapsed: boolean) => {
    return api.setBacklogCollapsed(activeBoard, collapsed)
      .catch(err => { reportError(err, { headline: "Couldn't collapse backlog" }); throw err; });
  }, [activeBoard]);

  // Per-repo Pipeline impact-primary display preference (BACI-349).
  const setImpactPrimary = useCallback((impactPrimary: boolean) => {
    return api.setImpactPrimary(activeBoard, impactPrimary)
      .catch(err => { reportError(err, { headline: "Couldn't toggle impact-first view" }); throw err; });
  }, [activeBoard]);

  // ─── Shipped count / scope / ship flourish + SFX ────────────────────────

  // shippedScope (BACI-221) is the active Today / Last Week / Forever window
  // for the Shipped pill + its popover, seeded from localStorage on mount.
  const [shippedScope, setShippedScope] = useLocalStorage<ShippedScope>(
    SHIPPED_SCOPE_KEY,
    DEFAULT_SCOPE,
    shippedScopeCodec,
  );

  // shippedCount (BACI-187, server-derived for BACI-221) feeds the "Shipped ·
  // N" pill. Polled on the standard cadence. `null` is the loading sentinel
  // the SFX watch treats as a snap (not a ding) on scope / repo change.
  const [shippedCount, setShippedCount] = useState<number | null>(0);
  // BACI-295: previous-count ref the count-rise SFX watch diffs against.
  const prevShippedCountRef = useRef<number | null>(null);
  // refreshShippedCount (BACI-276): the single fetch the poll effect and the
  // on-ship trigger both invoke. Stable across renders; re-derived only when
  // the board or scope changes.
  const refreshShippedCount = useCallback(() => {
    if (!activeBoard || activeBoard === 'all') {
      setShippedCount(0);
      return;
    }
    // BACI-312: 'today' resolves to an absolute local-midnight cutoff in the
    // user's timezone (sinceTs); week/forever keep the relative sinceDays.
    const { sinceDays, sinceTs } = scopeSinceParams(shippedScope, timezone);
    api.countShippedIssues(activeBoard, sinceDays, sinceTs)
      .then((n) => setShippedCount(n))
      .catch(() => { /* pill is best-effort; the popover surfaces failures */ });
  }, [activeBoard, shippedScope, timezone]);
  useEffect(() => {
    if (!activeBoard || activeBoard === 'all') {
      setShippedCount(0);
      return;
    }
    // Blank the count to `null` (not 0) on scope / repo change so a stale
    // number doesn't sit on the pill while the first fetch is in flight, AND
    // so the post-navigation refill never dings (BACI-295). decideOdometerAction
    // snaps on a null on both sides, so the refill reads as a fresh first-load.
    setShippedCount(null);
    refreshShippedCount();
  }, [activeBoard, shippedScope, refreshShippedCount]);
  // Poll the count on the standard cadence while a concrete board is open.
  useInterval(refreshShippedCount, POLL_INTERVAL_MS, !!activeBoard && activeBoard !== 'all');

  // BACI-240 ka-ching SFX. The hook returns a stable `play`; the enabled
  // flag + autoplay-policy lock live inside it.
  const { play: playShipSfx } = useShipSfx({ enabled: audioEnabled });

  // BACI-295: tie the ka-ching to the same signal that rolls the odometer —
  // the server-derived shippedCount rising. A ref (not state) holds the
  // previous count so the watch doesn't itself trigger a render; first mount
  // snaps (prev null → no sound). A +N burst rolls the odometer once and
  // plays a single ka-ching. The null loading sentinel makes navigation
  // refills snap rather than ding.
  useEffect(() => {
    if (shippedCount !== null) {
      const action = decideOdometerAction(prevShippedCountRef.current, shippedCount);
      if (action.kind === 'roll') playShipSfx();
    }
    prevShippedCountRef.current = shippedCount;
  }, [shippedCount, playShipSfx]);

  // BACI-254 / BACI-276 callback for locally-observed ships. The audio rides
  // the count-rise effect above now (BACI-295); this callback's remaining job
  // is to bump the Pipeline odometer the moment a card lands in done rather
  // than waiting for the next 10s poll, so the count rolls — and the sound
  // fires — in lockstep with the glow.
  const onCardsShipped = useCallback(() => {
    refreshShippedCount();
  }, [refreshShippedCount]);

  // BACI-193 ship flourish: detect cards that just transitioned into `done`
  // and expose the flying key + flash signal to the Topbar's Shipped pill.
  const { flyingKey: flyingShipKey, flashing: shipFlashing, onFlightDone: onShipFlightDone } =
    useShipFlourish(cards, { onShip: onCardsShipped });

  const value = useMemo<CardsContextValue>(
    () => ({
      cards,
      setCards,
      refreshCards,
      moveCard,
      dispatchFromCard,
      cancelWaitingFromCard,
      setCardProcess,
      setCardProcessAuto,
      editCardProcessTail,
      editCardProcessTailAuto,
      startCardJob,
      stopCardJob,
      resetCardProcess,
      rerunCardJob,
      setCardEngineMode,
      fastTrackCard,
      shipCardFromPipeline,
      markDoneCardFromPipeline,
      cancelCardFromPipeline,
      doneCardFromPipeline,
      reorderPipelineCard,
      onBlockCard,
      setRepoAutoShip,
      setBacklogCollapsed,
      setImpactPrimary,
      flyingShipKey,
      shipFlashing,
      onShipFlightDone,
      shippedCount,
      shippedScope,
      setShippedScope,
    }),
    [
      cards,
      setCards,
      refreshCards,
      moveCard,
      dispatchFromCard,
      cancelWaitingFromCard,
      setCardProcess,
      setCardProcessAuto,
      editCardProcessTail,
      editCardProcessTailAuto,
      startCardJob,
      stopCardJob,
      resetCardProcess,
      rerunCardJob,
      setCardEngineMode,
      fastTrackCard,
      shipCardFromPipeline,
      markDoneCardFromPipeline,
      cancelCardFromPipeline,
      doneCardFromPipeline,
      reorderPipelineCard,
      onBlockCard,
      setRepoAutoShip,
      setBacklogCollapsed,
      setImpactPrimary,
      flyingShipKey,
      shipFlashing,
      onShipFlightDone,
      shippedCount,
      shippedScope,
      setShippedScope,
    ],
  );

  return <CardsContext.Provider value={value}>{children}</CardsContext.Provider>;
}

// useCards reads the board-cards resource + mutations + ship flourish.
// Throws when called outside the provider.
export function useCards(): CardsContextValue {
  const ctx = useContext(CardsContext);
  if (!ctx) throw new Error('useCards must be used within a CardsProvider');
  return ctx;
}
