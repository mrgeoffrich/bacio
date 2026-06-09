import type { DispatchDTO } from '../../api';

// AgentDispatchesSection — the session's most-recent few dispatches (the full
// audit log lives in History; the card is a snapshot, not an archive). A
// NeedsRescue dispatch (BACI-190) grows an inline Rescue button + error. The
// section is absent when the agent has no dispatches.
type AgentDispatchesSectionProps = {
  dispatches: DispatchDTO[];
  rescuing: Set<number>;
  rescueError: Record<number, string>;
  onRescue: (dispatchID: number) => void;
};

export default function AgentDispatchesSection({
  dispatches,
  rescuing,
  rescueError,
  onRescue,
}: AgentDispatchesSectionProps) {
  // Surface the agent's most-recent few dispatches; the full audit log
  // lives in History — the card is a snapshot, not an archive.
  const recentDispatches = dispatches.slice(0, 4);
  if (recentDispatches.length === 0) return null;
  return (
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
  );
}
