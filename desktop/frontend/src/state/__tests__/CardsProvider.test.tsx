import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act, renderHook } from '@testing-library/react';
import type { ReactNode } from 'react';
import { CardsProvider, useCards } from '../CardsProvider';
import { POLL_INTERVAL_MS } from '../../lib/hooks/useInterval';
import * as api from '../../api';

// CardsProvider nests inside RepoProvider + PreferencesProvider; the suite
// stubs those two context hooks (a hoisted holder lets a test flip the active
// view) and the api seam, so the provider can be exercised in isolation.
const repo = vi.hoisted(() => ({ activeBoard: 'BACI', activeView: 'pipeline' }));
vi.mock('../RepoProvider', () => ({ useActiveRepo: () => repo }));
vi.mock('../PreferencesProvider', () => ({
  usePreferences: () => ({ timezone: '', audioEnabled: false }),
}));

vi.mock('../../api', () => ({
  listCards: vi.fn(() => Promise.resolve([])),
  setIssueState: vi.fn(() => Promise.resolve(undefined)),
  dispatchIssue: vi.fn(() => Promise.resolve(undefined)),
  countShippedIssues: vi.fn(() => Promise.resolve(0)),
}));

function wrapper({ children }: { children: ReactNode }) {
  return <CardsProvider>{children}</CardsProvider>;
}

// Let a resolved fetch's .then chain (and the eager-load re-render) commit.
async function flush() {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
}

describe('CardsProvider / useCards', () => {
  let errSpy: ReturnType<typeof vi.spyOn>;
  beforeEach(() => {
    repo.activeBoard = 'BACI';
    repo.activeView = 'pipeline';
    vi.clearAllMocks();
    vi.mocked(api.listCards).mockResolvedValue([]);
    vi.mocked(api.countShippedIssues).mockResolvedValue(0);
    errSpy = vi.spyOn(console, 'error').mockImplementation(() => {});
  });
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('eager-loads cards and polls only on the board / pipeline views', async () => {
    vi.useFakeTimers();
    try {
      const { rerender } = renderHook(() => useCards(), { wrapper });
      await flush(); // eager load (+ the board/pipeline screen-switch refetch)
      const afterMount = vi.mocked(api.listCards).mock.calls.length;
      expect(afterMount).toBeGreaterThan(0);

      // One tick on the pipeline view → the cards poll fires.
      await act(async () => { vi.advanceTimersByTime(POLL_INTERVAL_MS); });
      await flush();
      const afterPoll = vi.mocked(api.listCards).mock.calls.length;
      expect(afterPoll).toBeGreaterThan(afterMount);

      // Switch off the board/pipeline views → re-render so the poll's enabled
      // predicate recomputes and the timer tears down.
      repo.activeView = 'agents';
      rerender();
      await act(async () => { vi.advanceTimersByTime(POLL_INTERVAL_MS * 2); });
      await flush();
      // No further cards fetches once the poll is disabled (only board/pipeline
      // refetch on a view switch, so the switch to agents adds nothing either).
      expect(vi.mocked(api.listCards).mock.calls.length).toBe(afterPoll);
    } finally {
      vi.useRealTimers();
    }
  });

  it('applies an optimistic flip immediately and rolls it back on a rejected persist', async () => {
    vi.mocked(api.listCards).mockResolvedValue([
      { key: 'BACI-1', column: 'in_pipeline', waitingState: null },
    ] as unknown as Awaited<ReturnType<typeof api.listCards>>);
    // dispatchFromCard sets an optimistic waitingState, persists, and reverts
    // to null on failure — the same snapshot/optimistic/rollback contract as
    // every card mutation, without the prev-column capture moveCard adds.
    vi.mocked(api.dispatchIssue).mockRejectedValue(new Error('nope'));

    const { result } = renderHook(() => useCards(), { wrapper });
    await flush(); // eager load
    expect(result.current.cards.find(c => c.key === 'BACI-1')?.waitingState).toBeNull();

    // Sync act: flush only the synchronous optimistic update, not the persist
    // rejection's microtask (which would roll back before we can observe it).
    act(() => { result.current.dispatchFromCard('BACI-1', 'implement'); });
    // The optimistic waiting spinner lands and the persist was attempted.
    expect(result.current.cards.find(c => c.key === 'BACI-1')?.waitingState).not.toBeNull();
    expect(api.dispatchIssue).toHaveBeenCalledWith('BACI', 'BACI-1', 'implement');

    await flush(); // the persist rejects → rollback clears the waiting state
    expect(result.current.cards.find(c => c.key === 'BACI-1')?.waitingState).toBeNull();
  });
});
