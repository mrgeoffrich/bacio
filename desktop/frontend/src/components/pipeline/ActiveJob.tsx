import { stageLabel } from '../../lib/pipelineProcesses';
import type { BoardCard, BoardCardJob } from '../../api';

// ActiveJob — the running job's mode + live meta + the worker's todo
// list (from the card's TodoWrite projection).
type ActiveJobProps = { card: BoardCard; job: BoardCardJob };

export default function ActiveJob({ card, job }: ActiveJobProps) {
  const todos = card.todos || [];
  const verb = card.activeVerb || '';
  // A queued/delivered dispatch reads as "running" on the job long before a
  // worker actually starts. card.taken flips true only once the worker opens
  // its claim, so gate the live word on it: "waiting for an agent" until then.
  const taken = !!card.taken;
  return (
    <div className="mk-pl-job">
      <div className="mk-pl-job-head">
        <span className="mk-pl-jmode">{stageLabel(job.mode)}</span>
        <span className="mk-pl-jmeta">
          {taken
            ? <span className="mk-pl-live">running</span>
            : <span className="mk-pl-live is-waiting">waiting for an agent</span>}
          {verb ? ` · ${verb}` : ''}
          {card.todosTotal ? ` · ${card.todosDone}/${card.todosTotal}` : ''}
        </span>
      </div>
      {todos.length > 0 && (
        <>
          <div className="mk-pl-proclabel">Job todos</div>
          <ul className="mk-pl-todos">
            {todos.map((t, i) => (
              <li key={i} className={`mk-pl-todo is-${t.status}`}>
                <span className="mk-pl-todo-box">
                  {t.status === 'completed' ? '✓' : t.status === 'in_progress' ? '◔' : ''}
                </span>
                {t.content}
              </li>
            ))}
          </ul>
        </>
      )}
    </div>
  );
}
