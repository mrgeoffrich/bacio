import { useState, useEffect } from 'react';
import Tooltip from '../Tooltip';
import SessionMessageButton from '../SessionMessageButton';
import type { StageCardState } from './useStageCardState';
import type { BoardCard } from '../../api';

// StageCardFooter — the controls row along the bottom of an in-pipeline
// card: Start / Stop (+ steer-the-worker), the Auto drive-mode toggle,
// Edit Process, the two-step Reset confirm, the engine-pause halt pill +
// Retry / Resume, and the terminal Ship / Mark-done hand-off. Split out of
// StageCard (BACI-362). The reset-confirm state is footer-local.
type StageCardFooterProps = {
  card: BoardCard;
  state: StageCardState;
  onResetProcess?: (key: string) => void;
  onEditProcess?: (key: string) => void;
  onStartJob?: (key: string) => void;
  onStopJob?: (key: string) => void;
  onSetEngineMode?: (key: string, mode: string) => void;
  onShip?: (key: string) => void;
  onMarkDone?: (key: string) => void;
};

export default function StageCardFooter({
  card,
  state,
  onResetProcess,
  onEditProcess,
  onStartJob,
  onStopJob,
  onSetEngineMode,
  onShip,
  onMarkDone,
}: StageCardFooterProps) {
  const {
    running, nextPending, allDone, engineAuto, hasProcess, paused,
    agentErrored, agentErrorTransient, agentErrorTerminal, subagentCancelled,
    blockedWaiting, hasShelveSentinel, hasMarkDoneSentinel,
  } = state;

  // BACI-314 Reset is a two-step in-card confirm: unarmed → armed (Confirm /
  // Cancel) → fires. Inline in the footer, not a modal, because Reset is
  // destructive (drops completed-job history) and must match the in-card
  // interaction pattern.
  const [confirmingReset, setConfirmingReset] = useState(false);
  // Reset is hidden while a job is running (the Stop-first rule — the engine
  // refuses ErrJobRunning anyway). If the card transitions to running while
  // the confirm is armed, drop the armed state so a stale Confirm can't
  // linger after the affordance disappears.
  useEffect(() => {
    if (running && confirmingReset) setConfirmingReset(false);
  }, [running, confirmingReset]);

  return (
    <footer className="mk-pl-stage-foot">
      {running ? (
        <>
          <button
            type="button"
            className="mk-pl-btn is-ghost is-danger is-sm"
            onClick={() => onStopJob?.(card.key)}
          >
            ■ Stop
          </button>
          {/* BACI-286: steer the running worker. Only when the
              running job's bound session is known. */}
          {card.runningSessionId && (
            <SessionMessageButton sessionId={card.runningSessionId} compact />
          )}
        </>
      ) : (
        <button
          type="button"
          className="mk-pl-btn is-primary is-sm"
          disabled={!nextPending || allDone || engineAuto}
          onClick={() => onStartJob?.(card.key)}
        >
          ▶ Start
        </button>
      )}
      <label className="mk-pl-toggle">
        Auto
        <button
          type="button"
          role="switch"
          aria-checked={engineAuto}
          className={`mk-pl-switch${engineAuto ? ' is-on' : ''}`}
          onClick={() => onSetEngineMode?.(card.key, engineAuto ? 'off' : 'auto')}
        />
      </label>
      <button
        type="button"
        className="mk-pl-btn is-ghost is-sm"
        onClick={() => onEditProcess?.(card.key)}
        title="Edit the process"
      >
        ✎ Edit Process
      </button>
      {/* BACI-314 Reset: in-card two-step confirm. Hidden while a job
          is running (Stop first) and on a card with no process (the
          fresh picker is already showing). Unarmed → "✕ Reset"; armed
          swaps to an inline "Reset process? Confirm / Cancel" row.
          Wipes the whole chain → the card drops back to the picker. */}
      {!running && hasProcess && (
        confirmingReset ? (
          <span className="mk-pl-reset-confirm">
            Reset process?
            <button
              type="button"
              className="mk-pl-btn is-danger is-sm"
              onClick={() => { setConfirmingReset(false); onResetProcess?.(card.key); }}
            >
              Confirm
            </button>
            <button
              type="button"
              className="mk-pl-btn is-ghost is-sm"
              onClick={() => setConfirmingReset(false)}
            >
              Cancel
            </button>
          </span>
        ) : (
          <button
            type="button"
            className="mk-pl-btn is-ghost is-danger is-sm"
            onClick={() => setConfirmingReset(true)}
            title="Wipe the process and pick again from scratch"
          >
            ✕ Reset
          </button>
        )
      )}
      <span className="mk-pl-spacer" />
      {paused && (
        <span
          className={`mk-pl-halt${agentErrored ? ' is-error' : ''}${blockedWaiting ? ' is-blocked' : ''}`}
          title={
            agentErrorTransient
              ? 'API outage — Start to retry once it clears'
              : agentErrorTerminal
                ? 'Account / billing / auth error — fix it, then Start'
                : subagentCancelled
                  ? 'Cancelled — Start to retry'
                  : blockedWaiting
                    ? 'Waiting for blockers to clear — see the blocked-by badge. Auto resumes on its own.'
                    : undefined
          }
        >
          {agentErrorTransient
            ? '⚠ API outage'
            : agentErrorTerminal
              ? '⚠ Account error'
              : subagentCancelled
                ? '⏸ Cancelled'
                : blockedWaiting
                  ? '⏳ Waiting on blockers'
                  : '⏸ Auto halted'}
        </span>
      )}
      {/* BACI-347: a dedicated retry control for the engine-paused halts,
          beside the pill (which stays as the at-a-glance explanation).
          It calls the same onStartJob handler as the footer Start button,
          so it advances regardless of Auto and clears the pause reason —
          the dedicated affordance that ignores the Start gate. Error
          halts read as an alarm-red ↻ Retry; the soft-cancel halt is a
          deliberate stop, so it reads as a calm ▶ Resume (no error
          styling). blockedWaiting is excluded — Auto resumes it on its
          own, so it needs no manual retry. */}
      {(agentErrored || subagentCancelled) && (
        <Tooltip label={
          agentErrorTerminal
            ? 'Account / billing / auth error — fix it first, then retry'
            : agentErrorTransient
              ? 'Retry once the API outage clears'
              : 'Resume the cancelled job'
        }>
          <button
            type="button"
            className={`mk-pl-btn is-sm ${agentErrored ? 'is-retry' : 'is-primary'}`}
            onClick={() => onStartJob?.(card.key)}
          >
            {agentErrored ? '↻ Retry' : '▶ Resume'}
          </button>
        </Tooltip>
      )}
      {/* BACI-314: render Ship ONLY when shippable — an un-shippable
          card shows no Ship control at all (was present-but-disabled).
          BACI-332: a Shelve-terminal chain never offers Ship — reaching
          the sentinel returns the card to Backlog, not Shipping.
          BACI-352: a Mark-done-terminal chain offers "Mark done"
          instead of Ship — it closes the card out as done directly. */}
      {allDone && !hasShelveSentinel && !hasMarkDoneSentinel && (
        <button
          type="button"
          className="mk-pl-btn is-sm is-primary"
          onClick={() => onShip?.(card.key)}
        >
          ⏏ Ship
        </button>
      )}
      {/* BACI-352: the Mark-done control — parallel to Ship, shown once
          all agent jobs are complete on a Mark-done-terminal chain.
          Moves the card straight to done, bypassing Shipping and the
          ship agent. */}
      {allDone && hasMarkDoneSentinel && (
        <button
          type="button"
          className="mk-pl-btn is-sm is-done"
          onClick={() => onMarkDone?.(card.key)}
        >
          ✓ Mark done
        </button>
      )}
    </footer>
  );
}
