import type { QuestionDTO } from '../../api';
import { relTime } from './relTime';

// AgentQuestionsSection — the open ask_user_question rows (BACI-53). Each is
// a button that opens the shared QuestionModal; the section is absent when
// the agent isn't waiting on the user.
type AgentQuestionsSectionProps = {
  questions: QuestionDTO[];
  onOpenQuestion: (questionID: number) => void;
};

export default function AgentQuestionsSection({
  questions,
  onOpenQuestion,
}: AgentQuestionsSectionProps) {
  if (questions.length === 0) return null;
  return (
    <section className="mk-agent-section mk-agent-section--question">
      <div className="mk-agent-section-label">User input needed</div>
      {questions.map((q) => (
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
  );
}
