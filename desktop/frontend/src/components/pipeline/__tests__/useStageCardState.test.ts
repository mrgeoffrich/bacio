import { describe, it, expect } from 'vitest';
import { renderHook } from '@testing-library/react';
import { useStageCardState } from '../useStageCardState';
import type { BoardCard, BoardCardJob } from '../../../api';

// mkCard builds a minimal in-pipeline BoardCard for the derivation tests —
// only the fields useStageCardState reads (jobs / engineMode /
// enginePauseReason / openQuestions) matter; the rest carry inert defaults.
function mkCard(over: Partial<BoardCard> = {}): BoardCard {
  return {
    key: 'BACI-1',
    column: 'in_pipeline',
    columnLabel: 'In Pipeline',
    title: 'A card',
    ...over,
  } as BoardCard;
}

function job(sequence: number, mode: string, status: string): BoardCardJob {
  return { sequence, mode, status };
}

describe('useStageCardState', () => {
  it('reports an empty card with no process', () => {
    const { result } = renderHook(() => useStageCardState(mkCard()));
    const s = result.current;
    expect(s.hasProcess).toBe(false);
    expect(s.running).toBeNull();
    expect(s.locked).toBe(false);
    expect(s.nextPending).toBeUndefined();
    expect(s.allDone).toBe(false);
    expect(s.aborted).toBe(false);
    expect(s.paused).toBe(false);
    expect(s.agentJobs).toHaveLength(0);
  });

  it('finds the next pending agent job and excludes the ship sentinel from agentJobs', () => {
    const card = mkCard({
      jobs: [
        job(1, 'plan', 'complete'),
        job(2, 'implement', 'pending'),
        job(3, 'ship', 'pending'),
      ],
    });
    const { result } = renderHook(() => useStageCardState(card));
    const s = result.current;
    expect(s.hasProcess).toBe(true);
    expect(s.nextPending?.mode).toBe('implement');
    // ship is a terminal sentinel — never an agent job.
    expect(s.agentJobs.map(j => j.mode)).toEqual(['plan', 'implement']);
    expect(s.allDone).toBe(false);
    expect(s.aborted).toBe(false);
  });

  it('locks the card while a job is running', () => {
    const card = mkCard({ jobs: [job(1, 'implement', 'running')] });
    const { result } = renderHook(() => useStageCardState(card));
    expect(result.current.running?.mode).toBe('implement');
    expect(result.current.locked).toBe(true);
  });

  it('marks allDone when every agent job is complete and nothing runs', () => {
    const card = mkCard({
      jobs: [
        job(1, 'plan', 'complete'),
        job(2, 'implement', 'complete'),
        job(3, 'ship', 'pending'),
      ],
    });
    const { result } = renderHook(() => useStageCardState(card));
    const s = result.current;
    expect(s.allDone).toBe(true);
    expect(s.aborted).toBe(false);
    expect(s.hasShelveSentinel).toBe(false);
    expect(s.hasMarkDoneSentinel).toBe(false);
  });

  it('does not mark allDone while a job is still running', () => {
    const card = mkCard({
      jobs: [job(1, 'plan', 'complete'), job(2, 'implement', 'running')],
    });
    const { result } = renderHook(() => useStageCardState(card));
    expect(result.current.allDone).toBe(false);
  });

  it('marks aborted when a job is cancelled with nothing running or pending', () => {
    const card = mkCard({
      jobs: [job(1, 'plan', 'complete'), job(2, 'implement', 'cancelled')],
    });
    const { result } = renderHook(() => useStageCardState(card));
    const s = result.current;
    expect(s.aborted).toBe(true);
    expect(s.allDone).toBe(false);
  });

  it('does not mark aborted when a pending step remains to auto-run', () => {
    const card = mkCard({
      jobs: [job(1, 'plan', 'cancelled'), job(2, 'implement', 'pending')],
    });
    const { result } = renderHook(() => useStageCardState(card));
    expect(result.current.aborted).toBe(false);
    expect(result.current.nextPending?.mode).toBe('implement');
  });

  it('detects the shelve and mark-done terminal sentinels', () => {
    const shelve = renderHook(() => useStageCardState(mkCard({
      jobs: [job(1, 'plan', 'complete'), job(2, 'shelve', 'pending')],
    })));
    expect(shelve.result.current.hasShelveSentinel).toBe(true);
    expect(shelve.result.current.hasMarkDoneSentinel).toBe(false);

    const markDone = renderHook(() => useStageCardState(mkCard({
      jobs: [job(1, 'plan', 'complete'), job(2, 'mark_done', 'pending')],
    })));
    expect(markDone.result.current.hasMarkDoneSentinel).toBe(true);
    expect(markDone.result.current.hasShelveSentinel).toBe(false);
  });

  it('reads the engine drive-mode', () => {
    const auto = renderHook(() => useStageCardState(mkCard({ engineMode: 'auto' })));
    expect(auto.result.current.engineAuto).toBe(true);
    const off = renderHook(() => useStageCardState(mkCard({ engineMode: 'off' })));
    expect(off.result.current.engineAuto).toBe(false);
  });

  it('fans the engine_pause_reason out into the right halt flags', () => {
    const transient = renderHook(() => useStageCardState(mkCard({ enginePauseReason: 'agent_error_transient' })));
    expect(transient.result.current.agentErrorTransient).toBe(true);
    expect(transient.result.current.agentErrored).toBe(true);
    expect(transient.result.current.paused).toBe(true);

    const terminal = renderHook(() => useStageCardState(mkCard({ enginePauseReason: 'agent_error_terminal' })));
    expect(terminal.result.current.agentErrorTerminal).toBe(true);
    expect(terminal.result.current.agentErrored).toBe(true);
    expect(terminal.result.current.paused).toBe(true);

    const cancelled = renderHook(() => useStageCardState(mkCard({ enginePauseReason: 'subagent_cancelled' })));
    expect(cancelled.result.current.subagentCancelled).toBe(true);
    expect(cancelled.result.current.agentErrored).toBe(false);
    expect(cancelled.result.current.paused).toBe(true);

    const blocked = renderHook(() => useStageCardState(mkCard({ enginePauseReason: 'blocked' })));
    expect(blocked.result.current.blockedWaiting).toBe(true);
    expect(blocked.result.current.paused).toBe(true);
  });

  it('pauses on an open question even without a pause reason', () => {
    const card = mkCard({ openQuestions: [{ id: 7, firstQuestion: 'Which one?' }] });
    const { result } = renderHook(() => useStageCardState(card));
    expect(result.current.question?.id).toBe(7);
    expect(result.current.paused).toBe(true);
  });
});
