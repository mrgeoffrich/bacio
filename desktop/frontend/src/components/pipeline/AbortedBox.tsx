import { stageLabel } from '../../lib/pipelineProcesses';
import type { BoardCardJob } from '../../api';

// AbortedBox — the chain is wedged on one or more Stopped (cancelled)
// jobs. Each aborted job gets a Re-run control that resets it to pending
// and re-dispatches it. Ship stays disabled until every job is complete.
type AbortedBoxProps = {
  jobs: BoardCardJob[];
  onRerunJob?: (seq: number) => void;
};

export default function AbortedBox({ jobs, onRerunJob }: AbortedBoxProps) {
  const cancelled = jobs.filter(j => j.status === 'cancelled');
  return (
    <div className="mk-pl-job is-aborted">
      <div className="mk-pl-job-head">
        <span className="mk-pl-jmode is-aborted">Aborted</span>
        <span className="mk-pl-jmeta">
          {cancelled.length === 1 ? 'Stopped before it finished' : `${cancelled.length} stopped jobs`}
          {' · re-run to continue'}
        </span>
      </div>
      <ul className="mk-pl-aborted-list">
        {cancelled.map(j => (
          <li key={j.sequence} className="mk-pl-aborted-item">
            <span className="mk-pl-aborted-lbl">{stageLabel(j.mode)}</span>
            <button
              type="button"
              className="mk-pl-btn is-sm mk-pl-rerun"
              onClick={() => onRerunJob?.(j.sequence)}
            >
              ↻ Re-run
            </button>
          </li>
        ))}
      </ul>
    </div>
  );
}
