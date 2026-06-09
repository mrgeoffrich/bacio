import { useMemo, useState } from 'react';
import QuestionModal from './QuestionModal';
import AgentCard from './agents/AgentCard';
import { useRescueDispatch } from './agents/useRescueDispatch';
import { mockAgents } from './agents/__mocks__/agents';
import { useAgents } from '../state/AgentsProvider';

// AgentsView is the desktop Agents screen: a responsive grid where each
// live agent session is a self-contained card showing status, current
// work, todos and recent dispatches at a glance. Ended sessions are
// filtered out — once a session ends there's nothing actionable left on
// it, and the history is visible via the History view if needed. Read-
// only — agents are dispatched work from the issue drawer.
// BACI-361: AgentsView self-sources the agents resource + refresh from
// useAgents() rather than props.
// BACI-365: the card + its four sections, the rescue flow, and the ?mock=1
// dataset live under components/agents/ — this shell wires them together.
export default function AgentsView() {
  const { agents, refreshAgents: onRefresh } = useAgents();
  // BACI-53 ask_user_question modal state. activeQuestionId is the
  // pending row's primary key (null when no modal is open); when set
  // the modal fetches the full payload + renders the answer form.
  const [activeQuestionId, setActiveQuestionId] = useState<number | null>(null);
  // BACI-190 rescue flow — the per-dispatch in-flight + error bookkeeping
  // and the handler the dispatch section's Rescue button fires.
  const { rescuing, rescueError, rescue } = useRescueDispatch(onRefresh);

  // TEMP demo toggle — ?mock=1 in the URL appends a varied set of fake
  // agents so the grid redesign can be reviewed against a realistic
  // mix of states. Remove together with mockAgents() before merging.
  const showMock =
    typeof window !== 'undefined' &&
    new URLSearchParams(window.location.search).has('mock');
  const allAgents = useMemo(
    () => (showMock ? [...agents, ...mockAgents()] : agents),
    [agents, showMock],
  );

  const liveAgents = useMemo(
    () => allAgents.filter((a) => a.status !== 'ended'),
    [allAgents],
  );

  return (
    <div className="mk-agents-view">
      {liveAgents.length === 0 ? (
        <p className="mk-drawer-text mk-meta-empty">
          No live agent sessions for this repo.
        </p>
      ) : (
        <div className="mk-agents-grid">
          {liveAgents.map((a) => (
            <AgentCard
              key={a.sessionId}
              agent={a}
              rescuing={rescuing}
              rescueError={rescueError}
              onRescue={rescue}
              onOpenQuestion={setActiveQuestionId}
            />
          ))}
        </div>
      )}

      <QuestionModal
        questionId={activeQuestionId}
        onClose={() => {
          setActiveQuestionId(null);
          onRefresh?.();
        }}
      />
    </div>
  );
}
