import React, { useState } from 'react';
import * as api from '../api';
import { reportError } from '../errors';
import Icon from './Icon.jsx';
import Modal from './Modal.jsx';

// relTime renders a coarse "time since" for the last-seen line.
function relTime(iso) {
  const ms = Date.now() - new Date(iso).getTime();
  const m = Math.floor(ms / 60000);
  if (m < 1) return 'just now';
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  return `${Math.floor(h / 24)}d ago`;
}

// todoGlyph maps a TodoWrite status to the leading glyph rendered in
// the per-agent drill-down. Mirrors the TUI's vocabulary so an operator
// can switch between them without re-learning.
function todoGlyph(status) {
  switch (status) {
    case 'completed':
      return '●';
    case 'in_progress':
      return '◐';
    default:
      return '○';
  }
}

// AgentsView is the desktop Agents screen: a full-page list with a card per
// agent session connected to the current repo, click-to-expand into its open
// claims and dispatches. Read-only — agents are dispatched work from the issue
// drawer, not from here.
export default function AgentsView({ agents, onRefresh }) {
  const [expanded, setExpanded] = useState(null);
  // BACI-53 ask_user_question modal state. activeQuestionId is the
  // pending row's primary key (null when no modal is open); when set
  // the modal fetches the full payload + renders the answer form.
  const [activeQuestionId, setActiveQuestionId] = useState(null);

  return (
    <div className="mk-agents-view">
      <header className="mk-agents-bar">
        <h2 className="mk-agents-title">Agents</h2>
        <button className="mk-icbtn" aria-label="Refresh" onClick={onRefresh}>
          <Icon name="board" />
        </button>
      </header>

      <div className="mk-agents-list">
        {agents.length === 0 && (
          <p className="mk-drawer-text mk-meta-empty">No agent sessions for this repo.</p>
        )}

        {agents.map((a) => {
          const name = a.agentName || a.sessionId.slice(0, 12);
          const isOpen = expanded === a.sessionId;
          const openQuestions = a.openQuestions || [];
          return (
            <div key={a.sessionId} className={`mk-agent-card ${isOpen ? 'is-open' : ''}`}>
              <button
                className="mk-agent-head"
                onClick={() => setExpanded(isOpen ? null : a.sessionId)}
              >
                <span className="mk-agent-name">{name}</span>
                <span className={`mk-pill mk-status-${a.status}`}>{a.status}</span>
                {a.waiting ? (
                  <span className="mk-pill mk-status-waiting">
                    waiting · {a.waitingIssue}
                  </span>
                ) : (
                  a.busy && (
                    <span className="mk-pill mk-status-busy">busy · {a.busyIssue}</span>
                  )
                )}
                {a.todosTotal > 0 && (
                  <span className="mk-pill mk-todos-badge">
                    todos {a.todosDone}/{a.todosTotal}
                  </span>
                )}
                {openQuestions.length > 0 && (
                  <span
                    className="mk-pill mk-question-badge"
                    role="button"
                    title="User input needed — click to answer"
                    onClick={(ev) => {
                      ev.stopPropagation();
                      // Auto-pop the first open question. The modal's
                      // "Next" path (when there are multiple) walks
                      // through them in order.
                      setActiveQuestionId(openQuestions[0].id);
                    }}
                  >
                    ? {openQuestions.length}
                  </span>
                )}
                <span className="mk-agent-meta">
                  {a.model || '—'} · {a.branch || '—'} · seen {relTime(a.lastSeenAt)}
                </span>
                <span className="mk-agent-counts">
                  {a.claims.length} claim{a.claims.length === 1 ? '' : 's'} ·{' '}
                  {a.dispatches.length} dispatch{a.dispatches.length === 1 ? '' : 'es'}
                </span>
              </button>

              {isOpen && (
                <div className="mk-agent-detail">
                  <div className="mk-agent-detail-label">Open claims</div>
                  {a.claims.length === 0 ? (
                    <div className="mk-meta-empty">none</div>
                  ) : (
                    a.claims.map((c) => (
                      <div key={c.issueKey} className="mk-agent-claim">
                        <span className="mk-mono">{c.issueKey}</span>
                        {c.state === 'needs_action' && (
                          <span className="mk-tag">needs action</span>
                        )}
                        {c.prompt && <span className="mk-agent-claim-prompt">{c.prompt}</span>}
                      </div>
                    ))
                  )}

                  {openQuestions.length > 0 && (
                    <>
                      <div className="mk-agent-detail-label">User input needed</div>
                      {openQuestions.map((q) => (
                        <div key={q.id} className="mk-agent-question-row">
                          <button
                            className="mk-btn mk-btn-small"
                            onClick={() => setActiveQuestionId(q.id)}
                          >
                            Answer #{q.id} {q.header ? `(${q.header})` : ''}
                          </button>
                          {q.issueKey && (
                            <span className="mk-mono mk-meta">{q.issueKey}</span>
                          )}
                          <span className="mk-meta">{relTime(q.askedAt)}</span>
                        </div>
                      ))}
                    </>
                  )}

                  <div className="mk-agent-detail-label">
                    Todos {a.todosTotal > 0 ? `(${a.todosDone}/${a.todosTotal})` : ''}
                  </div>
                  {!a.todos || a.todos.length === 0 ? (
                    <div className="mk-meta-empty">none</div>
                  ) : (
                    a.todos.map((t, i) => (
                      <div
                        key={i}
                        className={`mk-agent-todo mk-agent-todo--${t.status}`}
                      >
                        <span className="mk-agent-todo-glyph" aria-hidden>
                          {todoGlyph(t.status)}
                        </span>
                        <span className="mk-agent-todo-text">{t.content}</span>
                      </div>
                    ))
                  )}

                  <div className="mk-agent-detail-label">Dispatches</div>
                  {a.dispatches.length === 0 ? (
                    <div className="mk-meta-empty">none</div>
                  ) : (
                    a.dispatches.map((d) => (
                      <div key={d.id} className="mk-agent-dispatch">
                        <span className="mk-mono">#{d.id}</span>
                        <span className="mk-pill">{d.status}</span>
                        {d.mode && <span className="mk-tag">{d.mode}</span>}
                        <span className="mk-mono">{d.issueKey || '—'}</span>
                      </div>
                    ))
                  )}
                </div>
              )}
            </div>
          );
        })}
      </div>

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

// QuestionModal renders the BACI-53 ask_user_question form. Fetches
// the full payload on open, renders each question as radios (single-
// select) or checkboxes (multi-select), and a free-text "Other"
// input per question. Submit calls answerSessionQuestion; the
// channel's next poll tick delivers the answer back to the agent.
// Cancel calls cancelSessionQuestion and the agent gets a tool error.
function QuestionModal({ questionId, onClose }) {
  const [row, setRow] = useState(null);
  const [loading, setLoading] = useState(false);
  const [answers, setAnswers] = useState({});

  // Track the row id we last loaded so a click on a different
  // question reloads cleanly without showing stale content.
  const lastLoadedId = React.useRef(null);
  React.useEffect(() => {
    if (questionId == null) {
      lastLoadedId.current = null;
      setRow(null);
      setAnswers({});
      return;
    }
    if (lastLoadedId.current === questionId) return;
    lastLoadedId.current = questionId;
    setLoading(true);
    api
      .getSessionQuestion(questionId)
      .then((r) => {
        setRow(r);
        setAnswers({});
      })
      .catch((err) =>
        reportError(err, { headline: 'Could not load question' }),
      )
      .finally(() => setLoading(false));
  }, [questionId]);

  if (questionId == null) {
    return null;
  }

  const items = row?.payload?.questions ?? [];

  function setAnswerFor(question, value) {
    setAnswers((prev) => ({ ...prev, [question]: value }));
  }

  function toggleMultiAnswer(question, label, checked) {
    setAnswers((prev) => {
      const current = Array.isArray(prev[question]) ? prev[question] : [];
      if (checked) {
        if (current.includes(label)) return prev;
        return { ...prev, [question]: [...current, label] };
      }
      return { ...prev, [question]: current.filter((x) => x !== label) };
    });
  }

  function buildFinalAnswers() {
    // Combine the chosen options with any non-empty "Other" free-text.
    const out = {};
    for (const item of items) {
      const v = answers[item.question];
      const otherKey = `${item.question}::other`;
      const other = (answers[otherKey] || '').trim();
      if (item.multiSelect) {
        const arr = Array.isArray(v) ? [...v] : [];
        if (other) arr.push(other);
        if (arr.length > 0) out[item.question] = arr;
      } else {
        if (v) out[item.question] = v;
        else if (other) out[item.question] = other;
      }
    }
    return out;
  }

  async function handleSubmit() {
    const final = buildFinalAnswers();
    try {
      setLoading(true);
      await api.answerSessionQuestion(questionId, final);
      onClose?.();
    } catch (err) {
      reportError(err, { headline: 'Could not submit answer' });
    } finally {
      setLoading(false);
    }
  }

  async function handleCancel() {
    if (!window.confirm('Dismiss this question? The agent will receive an error.')) {
      return;
    }
    try {
      setLoading(true);
      await api.cancelSessionQuestion(questionId);
      onClose?.();
    } catch (err) {
      reportError(err, { headline: 'Could not dismiss question' });
    } finally {
      setLoading(false);
    }
  }

  return (
    <Modal
      open={questionId != null}
      onClose={onClose}
      title="Agent question"
      preventClickOutsideClose
    >
      {loading && !row && <p className="mk-drawer-text">Loading…</p>}
      {row && row.state !== 'open' && (
        <p className="mk-drawer-text mk-meta-empty">
          This question is no longer open (state: {row.state}). Refresh to
          remove it from your list.
        </p>
      )}
      {row && row.state === 'open' && (
        <form
          onSubmit={(ev) => {
            ev.preventDefault();
            handleSubmit();
          }}
          className="mk-question-form"
        >
          {items.map((item) => (
            <fieldset key={item.question} className="mk-question-fieldset">
              <legend className="mk-question-legend">
                {item.header && (
                  <span className="mk-pill mk-question-header">{item.header}</span>
                )}{' '}
                {item.question}
              </legend>
              {item.options.map((opt) => {
                const inputId = `q-${item.question}-${opt.label}`;
                if (item.multiSelect) {
                  const arr = Array.isArray(answers[item.question])
                    ? answers[item.question]
                    : [];
                  return (
                    <label key={opt.label} className="mk-question-option" htmlFor={inputId}>
                      <input
                        id={inputId}
                        type="checkbox"
                        checked={arr.includes(opt.label)}
                        onChange={(ev) =>
                          toggleMultiAnswer(item.question, opt.label, ev.target.checked)
                        }
                      />
                      <span className="mk-question-label">{opt.label}</span>
                      {opt.description && (
                        <span className="mk-question-desc">{opt.description}</span>
                      )}
                    </label>
                  );
                }
                return (
                  <label key={opt.label} className="mk-question-option" htmlFor={inputId}>
                    <input
                      id={inputId}
                      type="radio"
                      name={`q-${item.question}`}
                      checked={answers[item.question] === opt.label}
                      onChange={() => setAnswerFor(item.question, opt.label)}
                    />
                    <span className="mk-question-label">{opt.label}</span>
                    {opt.description && (
                      <span className="mk-question-desc">{opt.description}</span>
                    )}
                  </label>
                );
              })}
              <label className="mk-question-other">
                <span className="mk-meta">Other:</span>
                <input
                  type="text"
                  value={answers[`${item.question}::other`] || ''}
                  onChange={(ev) =>
                    setAnswerFor(`${item.question}::other`, ev.target.value)
                  }
                  placeholder="(free-text response)"
                />
              </label>
            </fieldset>
          ))}
          <div className="mk-modal-actions">
            <Modal.Close asChild>
              <button type="button" className="mk-btn">
                Close
              </button>
            </Modal.Close>
            <button
              type="button"
              className="mk-btn mk-btn-danger"
              onClick={handleCancel}
              disabled={loading}
            >
              Dismiss (agent gets error)
            </button>
            <button type="submit" className="mk-btn mk-btn-primary" disabled={loading}>
              Submit answer
            </button>
          </div>
        </form>
      )}
    </Modal>
  );
}
