// Pipeline preset processes (Phase 4) — the starter job chains offered
// in the in-pipeline "pick a process" menu when a card is dragged in or
// zapped (requirements §5.1). The backend enumerates these in-code in
// internal/model/pipeline.go (model.PipelineProcesses); there is no
// endpoint, so this constant is the client-side mirror. KEEP IN LOCKSTEP
// with pipeline.go — same slugs, same stage modes, same menu order.
//
// `stages` is the ordered list of job modes; a trailing 'ship' stage is
// the hand-off (move to to_be_shipped), not an agent dispatch.
export interface PipelineProcess {
  slug: string;
  name: string;
  stages: string[];
}

export const PIPELINE_PROCESSES: PipelineProcess[] = [
  { slug: 'plan-implement-ship', name: 'Plan → Implement → Ship', stages: ['plan', 'implement', 'ship'] },
  { slug: 'implement-ship', name: 'Implement → Ship', stages: ['implement', 'ship'] },
  { slug: 'plan-implement', name: 'Plan → Implement', stages: ['plan', 'implement'] },
  { slug: 'plan', name: 'Plan', stages: ['plan'] },
  { slug: 'implement', name: 'Implement', stages: ['implement'] },
];

// stageLabel renders a job/stage mode as a short title-cased label for
// the chain stepper and process menu (e.g. "plan" → "Plan"). Falls back
// to the raw mode so a user-added template mode still renders something.
const STAGE_LABELS: Record<string, string> = {
  plan: 'Plan',
  implement: 'Implement',
  ship: 'Ship',
  design: 'Design',
  review: 'Review',
  fix_review: 'Fix review',
  scope: 'Scope',
};

export function stageLabel(mode: string): string {
  if (STAGE_LABELS[mode]) return STAGE_LABELS[mode];
  if (!mode) return '';
  return mode.charAt(0).toUpperCase() + mode.slice(1).replace(/_/g, ' ');
}

// isShipStage reports whether a stage/job mode is the Ship hand-off
// sentinel (model.ShipJobMode) rather than an agent dispatch.
export function isShipStage(mode: string): boolean {
  return mode === 'ship';
}
