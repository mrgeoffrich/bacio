import type { SessionTodoDTO } from '../../api';
import { todoGlyph } from '../../lib/todoGlyph';

// AgentTodosSection — the worker's task-list progress: a count, a progress
// bar, and the first few todos. Driven by todosTotal (not todos.length) so
// the bar shows even when the individual rows aren't carried; absent when
// the agent has no task list.
type AgentTodosSectionProps = {
  todos: SessionTodoDTO[];
  done: number;
  total: number;
};

export default function AgentTodosSection({ todos, done, total }: AgentTodosSectionProps) {
  if (total <= 0) return null;
  // Inline progress for the todo list — guarded by total > 0 above.
  const todoPct = Math.min(100, Math.round((done / total) * 100));
  return (
    <section className="mk-agent-section">
      <div className="mk-agent-section-label">
        Todos {done}/{total}
      </div>
      <div
        className="mk-agent-todo-bar"
        role="progressbar"
        aria-valuenow={done}
        aria-valuemin={0}
        aria-valuemax={total}
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
  );
}
