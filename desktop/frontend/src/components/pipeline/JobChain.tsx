import React from 'react';
import { stageLabel, isShipStage, isShelveStage, isMarkDoneStage } from '../../lib/pipelineProcesses';
import type { BoardCardJob } from '../../api';

// JobChain — the stepper across the top of the processing area. Each job
// renders as a step with a status-driven dot; the Ship sentinel renders
// as the hand-off step, the Shelve sentinel (BACI-332) as a distinct
// "returns to Backlog" step, and the Mark-done sentinel (BACI-352) as a
// "closes out as done" step.
type JobChainProps = { jobs: BoardCardJob[] };

export default function JobChain({ jobs }: JobChainProps) {
  return (
    <div className="mk-pl-chain">
      {jobs.map((j, i) => {
        const ship = isShipStage(j.mode);
        const shelve = isShelveStage(j.mode);
        const markDone = isMarkDoneStage(j.mode);
        // All three sentinels render as the handoff step shape; .is-shelve
        // tints it as a "park" (return to Backlog) and .is-done as a
        // "close out" (straight to done) rather than Ship's "promote".
        const cls = ship ? 'handoff' : shelve ? 'handoff is-shelve' : markDone ? 'handoff is-done' : j.status;
        let dot = `${i + 1}`;
        if (ship) dot = '⏏';
        else if (shelve) dot = '⇤';
        else if (markDone) dot = '✓';
        else if (j.status === 'complete') dot = '✓';
        else if (j.status === 'running') dot = '●';
        else if (j.status === 'cancelled') dot = '✕';
        return (
          <React.Fragment key={j.sequence}>
            {i > 0 && <span className="mk-pl-connector" />}
            <span
              className={`mk-pl-step is-${cls}`}
              title={shelve ? 'Shelve · returns to Backlog' : markDone ? 'Mark done · closes out as done' : undefined}
            >
              <span className="mk-pl-dot">{dot}</span>
              <span className="mk-pl-step-lbl">{stageLabel(j.mode)}</span>
            </span>
          </React.Fragment>
        );
      })}
    </div>
  );
}
