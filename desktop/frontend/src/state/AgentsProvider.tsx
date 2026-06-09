import { createContext, useContext, useEffect, useMemo } from 'react';
import type { ReactNode } from 'react';
import * as api from '../api';
import type { AgentCard } from '../api';
import { useAsyncResource } from '../lib/hooks/useAsyncResource';
import { useInterval, POLL_INTERVAL_MS } from '../lib/hooks/useInterval';
import { useActiveRepo } from './RepoProvider';

// AgentsProvider (BACI-361) owns the hot (10s-polled) agents list App.tsx
// used to carry: the per-repo agents resource, the BACI-74 available/busy
// counters the top-nav reads, and the always-on poll. It is isolated from
// the cards resource (also hot) so an agents tick doesn't re-render the
// Pipeline subtree and vice versa.

export type AgentCounts = { available: number; busy: number };

export type AgentsContextValue = {
  agents: AgentCard[];
  refreshAgents: (opts?: { silent?: boolean }) => void;
  agentCounts: AgentCounts;
};

const AgentsContext = createContext<AgentsContextValue | null>(null);

export function AgentsProvider({ children }: { children: ReactNode }) {
  const { activeBoard, activeView } = useActiveRepo();

  // agents is sourced from useAsyncResource (BACI-356): the hook owns the
  // list state + the stale-load guard and eager-loads on every repo change.
  // It stays loaded regardless of the active view (the top-nav counters read
  // it from any screen). `enabled: !!activeBoard` is the old guard;
  // `refreshAgents` is the manual refresh (silent on the poll path).
  const { data: agents, refresh: refreshAgents } = useAsyncResource<AgentCard[]>(
    () => api.listAgents(activeBoard),
    [],
    [activeBoard],
    { enabled: !!activeBoard, errorHeadline: "Couldn't refresh agents" },
  );

  // Re-fetch on switch to the Agents screen so it's fresh on arrival, not
  // mount-time/cached. Repo switches are handled by the eager load above, so
  // activeBoard is read via closure rather than listed as a dep.
  useEffect(() => {
    if (!activeBoard) return;
    if (activeView === 'agents') refreshAgents();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeView, refreshAgents]);

  // BACI-74: keep the top-nav Agents counters live regardless of which view
  // is showing — one small GET per 10s while a board is selected. `enabled`
  // tears the timer down before a board is picked.
  useInterval(() => refreshAgents({ silent: true }), POLL_INTERVAL_MS, !!activeBoard);

  // BACI-74: derive available/busy counts from the polled agents array.
  // `status: ended` is excluded; `busy` (which folds `waiting` in) decides
  // the busy bucket; available = non-ended - busy.
  const agentCounts = useMemo<AgentCounts>(() => {
    let available = 0;
    let busy = 0;
    for (const a of agents) {
      if (a.status === 'ended') continue;
      if (a.busy) busy++;
      else available++;
    }
    return { available, busy };
  }, [agents]);

  const value = useMemo<AgentsContextValue>(
    () => ({ agents, refreshAgents, agentCounts }),
    [agents, refreshAgents, agentCounts],
  );

  return <AgentsContext.Provider value={value}>{children}</AgentsContext.Provider>;
}

// useAgents reads the agents resource + counts. Throws when called outside
// the provider.
export function useAgents(): AgentsContextValue {
  const ctx = useContext(AgentsContext);
  if (!ctx) throw new Error('useAgents must be used within an AgentsProvider');
  return ctx;
}
