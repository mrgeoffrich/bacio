// TodoTasksTool renders TodoWrite / TaskCreate / TaskUpdate calls
// (BACI-125) as a task list rather than raw JSON. Completed items are
// struck through; in_progress is highlighted; pending is muted. Far
// more useful than a JSON dump for scanning what the agent was
// tracking.

import React from 'react';
import CollapsibleBody from '../CollapsibleBody';
import { flattenResultContent, prettyJSON } from '../format';
import type { ToolResult } from '../types';

type TodoInput = {
  todos?: TodoLike[];
  subject?: string;
  description?: string;
  status?: string;
  taskId?: string | number;
  activeForm?: string;
};
type TodoLike = {
  content?: string;
  status?: string;
  activeForm?: string;
};

function statusIcon(status?: string): string {
  switch (status) {
    case 'completed':
      return '✓';
    case 'in_progress':
      return '▶';
    case 'cancelled':
    case 'deleted':
      return '✗';
    default:
      return '○';
  }
}

function statusClass(status?: string): string {
  switch (status) {
    case 'completed':
      return 'is-completed';
    case 'in_progress':
      return 'is-active';
    case 'cancelled':
    case 'deleted':
      return 'is-cancelled';
    default:
      return 'is-pending';
  }
}

function TodoList({ todos }: { todos: TodoLike[] }): React.ReactElement {
  return (
    <ul className="mk-transcript-todos">
      {todos.map((t, i) => {
        const status = t.status ?? '';
        return (
          <li key={i} className={`mk-transcript-todo ${statusClass(status)}`}>
            <span className="mk-transcript-todo-icon" aria-hidden="true">
              {statusIcon(status)}
            </span>
            <span className="mk-transcript-todo-text">
              {t.content ?? t.activeForm ?? '(no content)'}
            </span>
          </li>
        );
      })}
    </ul>
  );
}

type TodoTasksToolProps = {
  toolName: 'TodoWrite' | 'TaskCreate' | 'TaskUpdate' | string;
  input: unknown;
  result?: ToolResult;
};

export default function TodoTasksTool({
  toolName,
  input,
  result,
}: TodoTasksToolProps): React.ReactElement {
  const i = (input ?? {}) as TodoInput;
  const resultText = result ? flattenResultContent(result.content) : '';

  let body: React.ReactNode;
  if (toolName === 'TodoWrite' && Array.isArray(i.todos)) {
    body = <TodoList todos={i.todos} />;
  } else if (toolName === 'TaskCreate') {
    body = (
      <ul className="mk-transcript-todos">
        <li className={`mk-transcript-todo ${statusClass('pending')}`}>
          <span className="mk-transcript-todo-icon" aria-hidden="true">+</span>
          <span className="mk-transcript-todo-text">
            <strong>{i.subject ?? '(no subject)'}</strong>
            {i.description && (
              <>
                {' — '}
                <span className="mk-transcript-todo-desc">{i.description}</span>
              </>
            )}
          </span>
        </li>
      </ul>
    );
  } else if (toolName === 'TaskUpdate') {
    body = (
      <ul className="mk-transcript-todos">
        <li className={`mk-transcript-todo ${statusClass(i.status)}`}>
          <span className="mk-transcript-todo-icon" aria-hidden="true">
            {statusIcon(i.status)}
          </span>
          <span className="mk-transcript-todo-text">
            Task #{String(i.taskId ?? '?')}
            {i.status && (
              <>
                {' '}→ <code className="mk-transcript-inline-code">{i.status}</code>
              </>
            )}
            {i.subject && <> — {i.subject}</>}
          </span>
        </li>
      </ul>
    );
  } else {
    body = <pre className="mk-transcript-pre">{prettyJSON(input)}</pre>;
  }

  return (
    <div className="mk-transcript-tool-body">
      <div className="mk-transcript-section">{body}</div>
      {result && resultText && (
        <div className={`mk-transcript-section ${result.isError ? 'is-error' : ''}`}>
          <CollapsibleBody text={resultText} label="result" linesThreshold={6}>
            <pre className="mk-transcript-pre">{resultText}</pre>
          </CollapsibleBody>
        </div>
      )}
    </div>
  );
}
