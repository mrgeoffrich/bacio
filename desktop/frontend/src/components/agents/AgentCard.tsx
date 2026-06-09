import type { AgentCard as AgentCardDTO } from '../../api';
import SessionMessageButton from '../SessionMessageButton';
import { relTime } from './relTime';
import AgentQuestionsSection from './AgentQuestionsSection';
import AgentClaimsSection from './AgentClaimsSection';
import AgentTodosSection from './AgentTodosSection';
import AgentDispatchesSection from './AgentDispatchesSection';

type AgentCardProps = {
  agent: AgentCardDTO;
  rescuing: Set<number>;
  rescueError: Record<number, string>;
  onRescue: (dispatchID: number) => void;
  onOpenQuestion: (questionID: number) => void;
};

// AgentCard renders one session as a self-contained grid tile: an identity
// head + status pill, a model/branch sub-line, busy/stale/no-channel badges,
// the at-a-glance stat row, the steer-the-worker message button, then the
// four conditional sections (questions / claims / todos / dispatches).
export default function AgentCard({
  agent: a,
  rescuing,
  rescueError,
  onRescue,
  onOpenQuestion,
}: AgentCardProps) {
  const name = a.agentName || a.sessionId.slice(0, 12);
  const dispatches = a.dispatches || [];
  const claims = a.claims || [];
  const todos = a.todos || [];

  return (
    <article className={`mk-agent-card mk-agent-card--${a.status}`}>
      <header className="mk-agent-card-head">
        <span className="mk-agent-name" title={a.sessionId}>
          {name}
        </span>
        <span
          className={`mk-pill mk-status-${a.status}`}
          title={a.status === 'errored' ? (a.errorMessage || a.errorType || 'Anthropic API error') : undefined}
        >
          {a.status === 'errored' && a.errorType ? `errored · ${a.errorType}` : a.status}
        </span>
      </header>

      <div className="mk-agent-card-sub">
        <span className="mk-mono">{a.model || '—'}</span>
        <span className="mk-meta-sep">·</span>
        <span className="mk-mono" title={a.branch}>
          {a.branch || '—'}
        </span>
      </div>

      <div className="mk-agent-card-badges">
        {a.busy ? (
          <span className="mk-pill mk-status-busy">busy · {a.busyIssue}</span>
        ) : null}
        {a.bacioVersionStale && (
          <span className="mk-pill mk-status-ended" title={`bacio ${a.bacioVersion}`}>
            stale build
          </span>
        )}
        {!a.hasChannel && (
          <span className="mk-pill mk-status-ended" title="No MCP channel attached">
            no channel
          </span>
        )}
      </div>

      <dl className="mk-agent-stats">
        <div className="mk-agent-stat">
          <dt>seen</dt>
          <dd>{relTime(a.lastSeenAt)}</dd>
        </div>
        <div className="mk-agent-stat">
          <dt>claims</dt>
          <dd>{claims.length}</dd>
        </div>
        <div className="mk-agent-stat">
          <dt>dispatches</dt>
          <dd>{dispatches.length}</dd>
        </div>
        {a.todosTotal > 0 && (
          <div className="mk-agent-stat">
            <dt>todos</dt>
            <dd>
              {a.todosDone}/{a.todosTotal}
            </dd>
          </div>
        )}
      </dl>

      {/* BACI-286: steer the running worker. Gated on a live channel —
          without one there's nothing to push the message through. */}
      {a.hasChannel && (
        <section className="mk-agent-section mk-agent-section--message">
          <SessionMessageButton sessionId={a.sessionId} />
        </section>
      )}

      <AgentQuestionsSection
        questions={a.openQuestions || []}
        onOpenQuestion={onOpenQuestion}
      />

      <AgentClaimsSection claims={claims} />

      <AgentTodosSection todos={todos} done={a.todosDone} total={a.todosTotal} />

      <AgentDispatchesSection
        dispatches={dispatches}
        rescuing={rescuing}
        rescueError={rescueError}
        onRescue={onRescue}
      />
    </article>
  );
}
