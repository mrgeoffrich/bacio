import type { BoardCardJob } from '../../api';

// DoneBox — every non-ship job complete; ready to hand off to Shipping.
type DoneBoxProps = { jobs: BoardCardJob[] };

export default function DoneBox({ jobs }: DoneBoxProps) {
  const total = jobs.length;
  return (
    <div className="mk-pl-job is-done">
      <div className="mk-pl-job-head">
        <span className="mk-pl-jmode is-done">Done</span>
        <span className="mk-pl-jmeta">{total} of {total} jobs complete · ready to hand off</span>
      </div>
    </div>
  );
}
