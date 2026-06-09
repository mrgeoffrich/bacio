import { useMemo } from 'react';
import { isShipStage, isShelveStage, isMarkDoneStage } from '../../lib/pipelineProcesses';
import type { BoardCard, BoardCardJob, BoardCardQuestion } from '../../api';

// StageCardState is the ~15 derived booleans/lookups a StageCard reads off
// its card — the job chain's running/pending/aborted shape, the terminal
// sentinels, the engine drive-mode, and the engine_pause_reason fan-out.
// Pulled out of StageCard verbatim so the body and footer split (which both
// consume it) read from one source of truth instead of recomputing.
export type StageCardState = {
  jobs: BoardCardJob[];
  hasProcess: boolean;
  running: BoardCardJob | null;
  locked: boolean;
  agentJobs: BoardCardJob[];
  nextPending: BoardCardJob | undefined;
  hasShelveSentinel: boolean;
  hasMarkDoneSentinel: boolean;
  allDone: boolean;
  aborted: boolean;
  engineAuto: boolean;
  question: BoardCardQuestion | null;
  agentErrorTransient: boolean;
  agentErrorTerminal: boolean;
  agentErrored: boolean;
  subagentCancelled: boolean;
  blockedWaiting: boolean;
  paused: boolean;
};

// useStageCardState derives the StageCard's processing state from its card.
// A pure derivation (no internal React state) memoised on the card so the
// filters/finds don't re-run on every unrelated render.
export function useStageCardState(card: BoardCard): StageCardState {
  return useMemo(() => {
    const jobs = card.jobs || [];
    const hasProcess = jobs.length > 0;
    const running = jobs.find(j => j.status === 'running') || null;
    // BACI-330: a card with a running job can't be dragged — the engine
    // refuses to move a card mid-job, so we gate the gesture at the source.
    // `locked` removes `draggable` (so a drag never starts), highlights the
    // card border (BACI-335 — no longer dims it, so the job chain stays
    // legible), and arms a "stop the job first" tooltip.
    const locked = !!running;
    // agentJobs are the real agent-dispatch stages — every job that isn't a
    // terminal sentinel (Ship hand-off / Shelve demote / Mark-done, BACI-332 /
    // BACI-352). DoneBox / AbortedBox and the all-complete gate count only
    // these, never a sentinel.
    const agentJobs = jobs.filter(j => !isShipStage(j.mode) && !isShelveStage(j.mode) && !isMarkDoneStage(j.mode));
    const nextPending = jobs.find(j => j.status === 'pending');
    // hasShelveSentinel: the chain ends in a Shelve demote, so it never
    // offers a Ship hand-off — reaching the sentinel returns it to Backlog.
    const hasShelveSentinel = jobs.some(j => isShelveStage(j.mode));
    // hasMarkDoneSentinel: the chain ends in a Mark-done sentinel (BACI-352),
    // so it offers a "Mark done" control instead of Ship — reaching the
    // sentinel closes the card out as done, bypassing Shipping.
    const hasMarkDoneSentinel = jobs.some(j => isMarkDoneStage(j.mode));
    // allDone = every agent job is complete (cancelled deliberately does
    // NOT count — a Stopped job is Aborted, not "ready to hand off"; Ship
    // stays disabled on it).
    const allDone = agentJobs.length > 0 && !running &&
      agentJobs.every(j => j.status === 'complete');
    // aborted = the chain is wedged on a cancelled job — nothing running, no
    // pending step left to auto-run, and at least one agent job cancelled.
    // The user re-runs the aborted step (or Edits the process) to proceed.
    const aborted = !running && !nextPending &&
      agentJobs.some(j => j.status === 'cancelled');
    const engineAuto = card.engineMode === 'auto';
    const question = (card.openQuestions || [])[0] || null;
    // BACI-296 / BACI-300: a worker that died on an Anthropic API error
    // halts the chain in place with engine_pause_reason set (no open
    // question). The reason carries the error class so the halt copy can
    // distinguish a passing outage from an account/config problem the user
    // must fix.
    const agentErrorTransient = card.enginePauseReason === 'agent_error_transient';
    const agentErrorTerminal = card.enginePauseReason === 'agent_error_terminal';
    const agentErrored = agentErrorTransient || agentErrorTerminal;
    // BACI-328: a user soft-cancelled the dispatch worker — the supervisor's
    // report_subagent_incomplete callback halted the chain in place with the
    // neutral subagent_cancelled reason. It's a deliberate stop, not a
    // failure, so it renders as a calm "Cancelled" pill (no is-error styling),
    // worded "Start to retry".
    const subagentCancelled = card.enginePauseReason === 'subagent_cancelled';
    // BACI-343: Auto is on but the card has open blockers, so the engine is
    // holding off starting the next job until they clear — then it auto-resumes
    // with no re-arm. The blocked-by badge already names the blockers; this is
    // the calm "armed but waiting" pill that keeps the card from reading as a
    // stalled one. Distinct from the error/cancelled reasons (which disarm Auto).
    const blockedWaiting = card.enginePauseReason === 'blocked';
    const paused = card.enginePauseReason === 'open_question' || agentErrored || subagentCancelled || blockedWaiting || !!question;

    return {
      jobs, hasProcess, running, locked, agentJobs, nextPending,
      hasShelveSentinel, hasMarkDoneSentinel, allDone, aborted,
      engineAuto, question,
      agentErrorTransient, agentErrorTerminal, agentErrored,
      subagentCancelled, blockedWaiting, paused,
    };
  }, [card]);
}
