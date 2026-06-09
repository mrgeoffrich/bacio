import { useMemo, useState } from 'react';
import type {
  AgentCard as AgentCardDTO,
  ClaimDTO,
  DispatchDTO,
} from '../api';
import QuestionModal from './QuestionModal';
import SessionMessageButton from './SessionMessageButton';
import { todoGlyph } from '../lib/todoGlyph';
import * as api from '../api';
import { useAgents } from '../state/AgentsProvider';

// relTime renders a coarse "time since" for the last-seen line.
function relTime(iso: string): string {
  const ms = Date.now() - new Date(iso).getTime();
  const m = Math.floor(ms / 60000);
  if (m < 1) return 'just now';
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  return `${Math.floor(h / 24)}d ago`;
}

// TEMP demo data — appended to real agents when ?mock=1 is in the URL.
// Covers the spectrum of card states so the grid redesign can be
// reviewed against a realistic mix. Remove before merging.
function mockAgents(): AgentCardDTO[] {
  const now = Date.now();
  const ago = (m: number) => new Date(now - m * 60_000).toISOString();
  // The card only reads a handful of dispatch / claim fields; these
  // helpers fill the remaining required DTO fields with inert defaults
  // so the literals stay readable while satisfying the domain types.
  const dispatch = (
    d: Pick<DispatchDTO, 'id' | 'issueKey' | 'mode' | 'status'> &
      Partial<DispatchDTO>,
  ): DispatchDTO => ({
    targetAgent: '',
    payload: '',
    createdBy: 'mock',
    createdAt: ago(60),
    needsRescue: false,
    ...d,
  });
  const claim = (
    c: Pick<ClaimDTO, 'issueKey' | 'state' | 'prompt'> & Partial<ClaimDTO>,
  ): ClaimDTO => ({ claimedAt: ago(60), ...c });
  return [
    {
      sessionId: 'mock-busy-todos',
      agentName: 'brave-otter@claude.shiny',
      actor: 'claude',
      model: 'claude-opus-4-7',
      branch: 'agents-grid-redesign',
      repoPrefix: 'BACI',
      status: 'active',
      busy: true,
      busyIssue: 'BACI-204',
      hasChannel: true,
      bacioVersion: 'v0.42.0',
      bacioVersionStale: false,
      lastSeenAt: ago(0),
      claims: [
        claim({
          issueKey: 'BACI-204',
          state: 'in_pipeline',
          prompt:
            'Plan a redesign of the Agents view to fit cards on a responsive grid.',
        }),
      ],
      dispatches: [
        dispatch({ id: 901, issueKey: 'BACI-204', mode: 'plan', status: 'delivered' }),
        dispatch({ id: 898, issueKey: 'BACI-201', mode: 'review', status: 'acked' }),
        dispatch({ id: 895, issueKey: 'BACI-199', mode: 'ship', status: 'acked' }),
      ],
      todos: [
        { content: 'Read AgentsView and surrounding CSS', status: 'completed' },
        { content: 'Sketch grid layout with self-contained cards', status: 'completed' },
        { content: 'Wire ?mock=1 demo data for review', status: 'in_progress' },
        { content: 'Update CSS for status accents and progress bar', status: 'pending' },
        { content: 'Verify against multiple states (busy / waiting / questions)', status: 'pending' },
      ],
      todosDone: 2,
      todosTotal: 5,
      openQuestions: [],
    },
    {
      sessionId: 'mock-busy-question',
      agentName: 'curious-quokka@claude.shiny',
      actor: 'claude',
      model: 'claude-sonnet-4-6',
      branch: 'worktree-agent-7c41ab',
      repoPrefix: 'BACI',
      status: 'active',
      busy: true,
      busyIssue: 'BACI-187',
      hasChannel: true,
      bacioVersion: 'v0.42.0',
      bacioVersionStale: false,
      lastSeenAt: ago(2),
      claims: [
        claim({ issueKey: 'BACI-187', state: 'in_pipeline', prompt: 'Pick the auth strategy.' }),
      ],
      dispatches: [
        dispatch({ id: 884, issueKey: 'BACI-187', mode: 'design', status: 'delivered' }),
      ],
      todos: [],
      todosDone: 0,
      todosTotal: 0,
      openQuestions: [
        { id: 9991, header: 'Auth library', issueKey: 'BACI-187', askedAt: ago(2) },
      ],
    },
    {
      sessionId: 'mock-idle-clean',
      agentName: 'mellow-marten@claude.shiny',
      actor: 'claude',
      model: 'claude-opus-4-7',
      branch: 'main',
      repoPrefix: 'BACI',
      status: 'idle',
      busy: false,
      busyIssue: '',
      hasChannel: true,
      bacioVersion: 'v0.42.0',
      bacioVersionStale: false,
      lastSeenAt: ago(8),
      claims: [],
      dispatches: [
        dispatch({ id: 870, issueKey: 'BACI-198', mode: 'fix_review', status: 'acked' }),
        dispatch({ id: 866, issueKey: 'BACI-197', mode: 'implement', status: 'acked' }),
      ],
      todos: [],
      todosDone: 0,
      todosTotal: 0,
      openQuestions: [],
    },
    {
      sessionId: 'mock-rescue-needed',
      agentName: 'gentle-fox@claude.shiny',
      actor: 'claude',
      model: 'claude-opus-4-7',
      branch: 'worktree-agent-19c2d4',
      repoPrefix: 'BACI',
      status: 'active',
      busy: true,
      busyIssue: 'BACI-156',
      hasChannel: false,
      bacioVersion: 'v0.40.1',
      bacioVersionStale: true,
      lastSeenAt: ago(14),
      claims: [
        claim({ issueKey: 'BACI-156', state: 'in_pipeline', prompt: 'Implement the sync diff renderer.' }),
      ],
      dispatches: [
        dispatch({
          id: 842,
          issueKey: 'BACI-156',
          mode: 'implement',
          status: 'delivered',
          needsRescue: true,
        }),
        dispatch({ id: 838, issueKey: 'BACI-155', mode: 'design', status: 'acked' }),
      ],
      todos: [
        { content: 'Wire the new diff API', status: 'completed' },
        { content: 'Render the deletion side', status: 'in_progress' },
        { content: 'Snapshot tests', status: 'pending' },
      ],
      todosDone: 1,
      todosTotal: 3,
      openQuestions: [],
    },
    {
      sessionId: 'mock-supervisor',
      agentName: 'wise-puffin@claude.supervisor',
      actor: 'claude',
      model: 'claude-opus-4-7',
      branch: 'main',
      repoPrefix: 'BACI',
      status: 'active',
      busy: false,
      busyIssue: '',
      hasChannel: true,
      bacioVersion: 'v0.42.0',
      bacioVersionStale: false,
      lastSeenAt: ago(1),
      claims: [],
      dispatches: [
        dispatch({ id: 904, issueKey: 'BACI-205', mode: 'scope', status: 'queued' }),
        dispatch({ id: 902, issueKey: 'BACI-203', mode: 'plan', status: 'pending' }),
        dispatch({ id: 900, issueKey: 'BACI-202', mode: 'ship', status: 'acked' }),
        dispatch({ id: 896, issueKey: 'BACI-200', mode: 'review', status: 'acked' }),
        dispatch({ id: 893, issueKey: 'BACI-199', mode: 'implement', status: 'acked' }),
      ],
      todos: [],
      todosDone: 0,
      todosTotal: 0,
      openQuestions: [],
    },
  ];
}

// AgentsView is the desktop Agents screen: a responsive grid where each
// live agent session is a self-contained card showing status, current
// work, todos and recent dispatches at a glance. Ended sessions are
// filtered out — once a session ends there's nothing actionable left on
// it, and the history is visible via the History view if needed. Read-
// only — agents are dispatched work from the issue drawer.
// BACI-361: AgentsView self-sources the agents resource + refresh from
// useAgents() rather than props.
export default function AgentsView() {
  const { agents, refreshAgents: onRefresh } = useAgents();
  // BACI-53 ask_user_question modal state. activeQuestionId is the
  // pending row's primary key (null when no modal is open); when set
  // the modal fetches the full payload + renders the answer form.
  const [activeQuestionId, setActiveQuestionId] = useState<number | null>(null);
  // BACI-190 rescue: per-dispatch in-flight set so a double-click on
  // the Rescue button doesn't fire twice. The rescued button stays
  // disabled until the next onRefresh poll clears the NeedsRescue flag
  // (a fresh rescue dispatch lands on a different session — the
  // original dead-session dispatch keeps its flag until acked).
  const [rescuing, setRescuing] = useState<Set<number>>(() => new Set());
  // BACI-190 rescue: most recent rescue error so a failure (no idle
  // supervisor / already-acked race) is visible inline without a toast
  // surface. Map: dispatch id → error message.
  const [rescueError, setRescueError] = useState<Record<number, string>>(() => ({}));

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

  const handleRescue = async (dispatchID: number) => {
    setRescuing((prev) => {
      const next = new Set(prev);
      next.add(dispatchID);
      return next;
    });
    setRescueError((prev) => {
      if (!(dispatchID in prev)) return prev;
      const next = { ...prev };
      delete next[dispatchID];
      return next;
    });
    try {
      await api.rescueDispatch(dispatchID);
      onRefresh?.();
    } catch (err) {
      setRescueError((prev) => ({
        ...prev,
        [dispatchID]: err instanceof Error ? err.message : String(err),
      }));
    } finally {
      setRescuing((prev) => {
        const next = new Set(prev);
        next.delete(dispatchID);
        return next;
      });
    }
  };

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
              onRescue={handleRescue}
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

type AgentCardProps = {
  agent: AgentCardDTO;
  rescuing: Set<number>;
  rescueError: Record<number, string>;
  onRescue: (dispatchID: number) => void;
  onOpenQuestion: (questionID: number) => void;
};

// AgentCard renders one session as a self-contained grid tile.
function AgentCard({ agent: a, rescuing, rescueError, onRescue, onOpenQuestion }: AgentCardProps) {
  const name = a.agentName || a.sessionId.slice(0, 12);
  const openQuestions = a.openQuestions || [];
  const dispatches = a.dispatches || [];
  const claims = a.claims || [];
  const todos = a.todos || [];

  // Surface the agent's most-recent few dispatches; the full audit log
  // lives in History — the card is a snapshot, not an archive.
  const recentDispatches = dispatches.slice(0, 4);

  // Inline progress for the todo list — only when there are any todos.
  const todoPct =
    a.todosTotal > 0 ? Math.min(100, Math.round((a.todosDone / a.todosTotal) * 100)) : 0;

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

      {openQuestions.length > 0 && (
        <section className="mk-agent-section mk-agent-section--question">
          <div className="mk-agent-section-label">User input needed</div>
          {openQuestions.map((q) => (
            <button
              key={q.id}
              type="button"
              className="mk-agent-question-btn"
              onClick={() => onOpenQuestion(q.id)}
            >
              <span className="mk-agent-question-mark">?</span>
              <span className="mk-agent-question-text">
                {q.header || `Question #${q.id}`}
              </span>
              {q.issueKey && <span className="mk-mono mk-meta">{q.issueKey}</span>}
              <span className="mk-meta">{relTime(q.askedAt)}</span>
            </button>
          ))}
        </section>
      )}

      {claims.length > 0 && (
        <section className="mk-agent-section">
          <div className="mk-agent-section-label">Open claims</div>
          {claims.map((c) => (
            <div key={c.issueKey} className="mk-agent-claim">
              <span className="mk-mono">{c.issueKey}</span>
              {c.prompt && (
                <span className="mk-agent-claim-prompt" title={c.prompt}>
                  {c.prompt}
                </span>
              )}
            </div>
          ))}
        </section>
      )}

      {a.todosTotal > 0 && (
        <section className="mk-agent-section">
          <div className="mk-agent-section-label">
            Todos {a.todosDone}/{a.todosTotal}
          </div>
          <div
            className="mk-agent-todo-bar"
            role="progressbar"
            aria-valuenow={a.todosDone}
            aria-valuemin={0}
            aria-valuemax={a.todosTotal}
          >
            <div className="mk-agent-todo-bar-fill" style={{ width: `${todoPct}%` }} />
          </div>
          {todos.length > 0 && (
            <div className="mk-agent-todo-list">
              {todos.slice(0, 6).map((t, i) => (
                <div key={i} className={`mk-agent-todo mk-agent-todo--${t.status}`}>
                  <span className="mk-agent-todo-glyph" aria-hidden>
                    {todoGlyph(t.status)}
                  </span>
                  <span className="mk-agent-todo-text">{t.content}</span>
                </div>
              ))}
              {todos.length > 6 && (
                <div className="mk-agent-todo-more">+{todos.length - 6} more</div>
              )}
            </div>
          )}
        </section>
      )}

      {recentDispatches.length > 0 && (
        <section className="mk-agent-section">
          <div className="mk-agent-section-label">
            Recent dispatches
            {dispatches.length > recentDispatches.length && (
              <span className="mk-agent-section-extra">
                {' '}
                · {dispatches.length - recentDispatches.length} older
              </span>
            )}
          </div>
          {recentDispatches.map((d) => (
            <div key={d.id} className="mk-agent-dispatch">
              <span className="mk-mono mk-agent-dispatch-id">#{d.id}</span>
              <span className={`mk-pill mk-status-${d.status}`}>{d.status}</span>
              {d.mode && <span className="mk-tag">{d.mode}</span>}
              <span className="mk-mono mk-agent-dispatch-issue">
                {d.issueKey || '—'}
              </span>
              {d.needsRescue && (
                <>
                  <button
                    type="button"
                    className="mk-btn-rescue"
                    onClick={() => onRescue(d.id)}
                    disabled={rescuing.has(d.id)}
                    title="Post a rescue dispatch to an idle supervisor so it can finalise this worker's worktree edits"
                  >
                    {rescuing.has(d.id) ? 'Rescuing…' : 'Rescue'}
                  </button>
                  {rescueError[d.id] && (
                    <span
                      className="mk-agent-dispatch-error"
                      title={rescueError[d.id]}
                    >
                      {rescueError[d.id]}
                    </span>
                  )}
                </>
              )}
            </div>
          ))}
        </section>
      )}
    </article>
  );
}
