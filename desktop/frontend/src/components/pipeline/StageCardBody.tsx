import JobChain from './JobChain';
import QuestionPanel from './QuestionPanel';
import DoneBox from './DoneBox';
import AbortedBox from './AbortedBox';
import ActiveJob from './ActiveJob';
import type { StageCardState } from './useStageCardState';
import type { BoardCard } from '../../api';

// StageCardBody — the processing area of an in-pipeline card: the job-chain
// stepper plus the active-detail panel that swaps between the open question,
// the done/aborted summaries, the running job's live todos, or the
// "press Start" empty hint. Split out of StageCard (BACI-362) so the body
// and the controls footer live in their own files.
type StageCardBodyProps = {
  card: BoardCard;
  state: StageCardState;
  onRerunJob?: (key: string, seq: number) => void;
  onOpenQuestion?: (id: number) => void;
};

export default function StageCardBody({ card, state, onRerunJob, onOpenQuestion }: StageCardBodyProps) {
  const { jobs, agentJobs, question, allDone, aborted, running } = state;
  return (
    <>
      <div className="mk-pl-stage-proc">
        <div className="mk-pl-proclabel">Process</div>
        <JobChain jobs={jobs} />
      </div>
      <div className="mk-pl-stage-detail">
        {question ? (
          <QuestionPanel question={question} onOpenQuestion={onOpenQuestion} />
        ) : allDone ? (
          <DoneBox jobs={agentJobs} />
        ) : aborted ? (
          <AbortedBox
            jobs={agentJobs}
            onRerunJob={(seq) => onRerunJob?.(card.key, seq)}
          />
        ) : running ? (
          <ActiveJob card={card} job={running} />
        ) : (
          <div className="mk-pl-proc-empty">
            Process selected. Press <b>Start</b> to run the next job, or flip
            {' '}<b>Auto</b> to run them through.
          </div>
        )}
      </div>
    </>
  );
}
