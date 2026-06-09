import type { BoardCardQuestion } from '../../api';

// QuestionPanel — the open question on the current job. Auto halts until
// it's answered; clicking opens the shared QuestionModal.
type QuestionPanelProps = {
  question: BoardCardQuestion;
  onOpenQuestion?: (id: number) => void;
};

export default function QuestionPanel({ question, onOpenQuestion }: QuestionPanelProps) {
  return (
    <button
      type="button"
      className="mk-pl-qpanel"
      onClick={(e) => { e.stopPropagation(); onOpenQuestion?.(question.id); }}
    >
      <span className="mk-pl-qhead">❓ Waiting on you</span>
      <span className="mk-pl-qq">
        {question.firstQuestion || question.header || 'The worker needs your input — click to answer.'}
      </span>
    </button>
  );
}
