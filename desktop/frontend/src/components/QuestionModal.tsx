import { useEffect, useRef, useState } from 'react';
import * as api from '../api';
import { reportError } from '../errors';
import Modal from './Modal';
import type {
  SessionQuestion,
  QuestionOption,
} from '../../bindings/github.com/mrgeoffrich/bacio/internal/model';
import { QuestionState } from '../../bindings/github.com/mrgeoffrich/bacio/internal/model';

// QuestionModal renders the BACI-53 ask_user_question form. Fetches
// the full payload on open, renders each question as radios (single-
// select) or checkboxes (multi-select), plus an explicit "Other..."
// option that reveals a free-text input when picked. Submit calls
// answerSessionQuestion; the channel's next poll tick delivers the
// answer back to the agent. Cancel calls cancelSessionQuestion and
// the agent gets a tool error.
//
// Other answers route through the same QuestionAnswers payload as
// option labels — the store's validateAnswerLabel accepts any
// non-empty single-line string up to MaxOtherAnswerLen, so a typed
// Other value just slots into the answer (single-select) or onto the
// answer array (multi-select).
//
// Lifted out of AgentsView so KanbanCard (and any other surface that
// surfaces a "? N" pill) can reuse it without duplicating the form
// or the load/submit/cancel wiring.
type QuestionModalProps = {
  questionId: number | null;
  onClose?: () => void;
};

// answers maps question text -> the user's selection: a single option
// label (single-select) or an array of labels (multi-select).
type AnswerMap = Record<string, string | string[]>;

export default function QuestionModal({ questionId, onClose }: QuestionModalProps) {
  const [row, setRow] = useState<SessionQuestion | null>(null);
  const [loading, setLoading] = useState(false);
  const [answers, setAnswers] = useState<AnswerMap>({});
  // Per-question Other state, keyed by question text. Kept separate
  // from `answers` so a single-select that toggles between a label
  // and Other doesn't lose the typed text on toggle.
  const [otherSelected, setOtherSelected] = useState<Record<string, boolean>>({});
  const [otherText, setOtherText] = useState<Record<string, string>>({});

  // Track the row id we last loaded so a click on a different
  // question reloads cleanly without showing stale content.
  const lastLoadedId = useRef<number | null>(null);
  useEffect(() => {
    if (questionId == null) {
      lastLoadedId.current = null;
      setRow(null);
      setAnswers({});
      setOtherSelected({});
      setOtherText({});
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
        setOtherSelected({});
        setOtherText({});
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

  function pickSingleAnswer(question: string, value: string) {
    // Single-select radio click on a labeled option: record the
    // value AND clear Other so the radio group reads cleanly (the
    // Other radio shares the same `name`, but the explicit clear
    // keeps the reveal closed once a label option wins).
    setAnswers((prev) => ({ ...prev, [question]: value }));
    setOtherSelected((prev) => ({ ...prev, [question]: false }));
  }

  function toggleMultiAnswer(question: string, label: string, checked: boolean) {
    setAnswers((prev) => {
      const existing = prev[question];
      const current = Array.isArray(existing) ? existing : [];
      if (checked) {
        if (current.includes(label)) return prev;
        return { ...prev, [question]: [...current, label] };
      }
      return { ...prev, [question]: current.filter((x) => x !== label) };
    });
  }

  function pickOther(question: string, multiSelect: boolean) {
    if (!multiSelect) {
      // Picking Other on a single-select clears the label answer —
      // they're mutually exclusive in the radio group.
      setAnswers((prev) => ({ ...prev, [question]: '' }));
    }
    setOtherSelected((prev) => ({ ...prev, [question]: true }));
  }

  function unpickOther(question: string) {
    setOtherSelected((prev) => ({ ...prev, [question]: false }));
  }

  function setOtherTextFor(question: string, value: string) {
    setOtherText((prev) => ({ ...prev, [question]: value }));
  }

  function buildFinalAnswers() {
    // One entry per question: a string (single-select) or a string
    // array (multi-select). Empty selections are dropped — the
    // store-side ValidateQuestionAnswers tolerates missing keys
    // and surfaces "at least one question must be answered" if the
    // whole map ends up empty.
    const out: Record<string, string | string[]> = {};
    for (const item of items) {
      const v = answers[item.question];
      const otherOn = !!otherSelected[item.question];
      const otherT = (otherText[item.question] || '').trim();
      const hasOther = otherOn && otherT.length > 0;
      if (item.multiSelect) {
        const arr = Array.isArray(v) ? [...v] : [];
        if (hasOther) arr.push(otherT);
        if (arr.length > 0) out[item.question] = arr;
      } else {
        if (hasOther) {
          out[item.question] = otherT;
        } else if (v) {
          out[item.question] = v;
        }
      }
    }
    return out;
  }

  async function handleSubmit() {
    if (questionId == null) return;
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
    if (questionId == null) return;
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
      {row && row.state !== QuestionState.QuestionOpen && (
        <p className="mk-drawer-text mk-meta-empty">
          This question is no longer open (state: {row.state}). Refresh to
          remove it from your list.
        </p>
      )}
      {row && row.state === QuestionState.QuestionOpen && (
        <form
          onSubmit={(ev) => {
            ev.preventDefault();
            handleSubmit();
          }}
          className="mk-question-form"
        >
          {items.map((item, idx) => {
            // BACI-53: previews live on single-select questions only.
            // When at least one option carries one, switch the
            // fieldset's option-list to a two-column layout — option
            // list on the left, focused option's preview on the
            // right. Mirrors native AskUserQuestion's behavior.
            const hasPreview = !item.multiSelect &&
              item.options.some((o) => o.preview && o.preview.length > 0);
            const legendId = `q-legend-${idx}`;
            const focused = item.options.find(
              (o) => o.label === answers[item.question],
            ) || item.options[0];
            const focusedPreview = !otherSelected[item.question] && focused && focused.preview
              ? focused.preview
              : '';
            const renderOption = (opt: QuestionOption) => {
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
                    onChange={() => pickSingleAnswer(item.question, opt.label)}
                  />
                  <span className="mk-question-label">{opt.label}</span>
                  {opt.description && (
                    <span className="mk-question-desc">{opt.description}</span>
                  )}
                </label>
              );
            };
            return (
            <fieldset
              key={item.question}
              className={`mk-question-fieldset ${hasPreview ? 'mk-question-fieldset-preview' : ''}`}
              aria-labelledby={legendId}
            >
              {/* Not <legend>: Chrome renders <legend> overlapping the
                  fieldset's top border, so a long wrapped header bleeds
                  visually into both the modal background above and the
                  fieldset interior below. A normal block + aria-labelledby
                  keeps the group semantics without the rendering quirk. */}
              <div id={legendId} className="mk-question-legend">
                {item.header && (
                  <span className="mk-pill mk-question-header">{item.header}</span>
                )}{' '}
                {item.question}
              </div>
              {(() => {
                // "Other..." affordance + textbox. Single-select shares
                // the radio group; multi-select is an independent
                // checkbox. The textbox is revealed when picked.
                const otherBlock = (
                  <>
                    <label className="mk-question-option" htmlFor={`q-${item.question}-other`}>
                      <input
                        id={`q-${item.question}-other`}
                        type={item.multiSelect ? 'checkbox' : 'radio'}
                        name={`q-${item.question}`}
                        checked={!!otherSelected[item.question]}
                        onChange={(ev) => {
                          if (ev.target.checked) {
                            pickOther(item.question, !!item.multiSelect);
                          } else {
                            unpickOther(item.question);
                          }
                        }}
                      />
                      <span className="mk-question-label">Other…</span>
                      <span className="mk-question-desc">Type a custom response.</span>
                    </label>
                    {otherSelected[item.question] && (
                      <input
                        type="text"
                        className="mk-question-other-input"
                        value={otherText[item.question] || ''}
                        onChange={(ev) => setOtherTextFor(item.question, ev.target.value)}
                        placeholder={`Your answer to "${item.header || item.question}"`}
                        autoFocus
                      />
                    )}
                  </>
                );
                if (hasPreview) {
                  return (
                    <div className="mk-question-with-preview">
                      <div className="mk-question-option-list">
                        {item.options.map(renderOption)}
                        {otherBlock}
                      </div>
                      <pre className="mk-question-preview" aria-label="Option preview">
                        {focusedPreview || '(no preview for this option)'}
                      </pre>
                    </div>
                  );
                }
                return (
                  <>
                    {item.options.map(renderOption)}
                    {otherBlock}
                  </>
                );
              })()}
            </fieldset>
            );
          })}
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
