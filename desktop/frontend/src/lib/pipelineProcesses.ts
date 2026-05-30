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
  { slug: 'plan_large', name: 'Large Plan', stages: ['plan_large'] },
  { slug: 'design', name: 'Design', stages: ['design'] },
  { slug: 'implement', name: 'Implement', stages: ['implement'] },
];

// PRESET_BY_SLUG indexes PIPELINE_PROCESSES by slug so any caller that has
// a preset slug (e.g. the CLI `bacio issue process set <KEY> <preset>`
// mirror) can resolve its name + stages. The in-pipeline picker no longer
// consumes it — BACI-299 replaced the skip-Plan preset buttons with free
// stage toggles that always send an explicit `{ stages }` list.
export const PRESET_BY_SLUG: Record<string, PipelineProcess> =
  Object.fromEntries(PIPELINE_PROCESSES.map(p => [p.slug, p]));

// ProcessSelection is what the picker hands back to setCardProcess: an
// explicit ordered stage list from the toggles, or a preset slug. The
// BACI-299 picker only ever emits the `{ stages }` arm; the `{ process }`
// arm stays for other call sites threading a preset slug. Mutually
// exclusive — the api seam sends exactly one, mirroring the server's
// ResolveProcess guard.
export type ProcessSelection = { stages: string[] } | { process: string };

// stageLabel renders a job/stage mode as a short title-cased label for
// the chain stepper and process menu (e.g. "plan" → "Plan"). Falls back
// to the raw mode so a user-added template mode still renders something.
const STAGE_LABELS: Record<string, string> = {
  plan: 'Plan',
  plan_large: 'Large Plan',
  implement: 'Implement',
  ship: 'Ship',
  design: 'Design',
  review: 'Review',
  fix_review: 'Fix review',
  scope: 'Scope',
  research: 'Research',
};

export function stageLabel(mode: string): string {
  if (STAGE_LABELS[mode]) return STAGE_LABELS[mode];
  if (!mode) return '';
  return mode.charAt(0).toUpperCase() + mode.slice(1).replace(/_/g, ' ');
}

// stageGlyph returns a stable single-character glyph for a stage/job
// mode, used by the chain chips and the picker stepper. First-letter
// glyphs collide (Plan vs Large Plan both "P"), so the common modes get
// an explicit glyph; anything else falls back to its first letter.
const STAGE_GLYPHS: Record<string, string> = {
  plan: 'P',
  plan_large: 'L',
  design: 'D',
  implement: 'I',
  ship: '⏏',
  review: 'R',
  fix_review: 'F',
  scope: 'S',
  research: 'Ⓡ',
};

export function stageGlyph(mode: string): string {
  if (STAGE_GLYPHS[mode]) return STAGE_GLYPHS[mode];
  if (!mode) return '';
  return mode.charAt(0).toUpperCase();
}

// isShipStage reports whether a stage/job mode is the Ship hand-off
// sentinel (model.ShipJobMode) rather than an agent dispatch.
export function isShipStage(mode: string): boolean {
  return mode === 'ship';
}

// EDITABLE_JOB_MODES is the palette the BACI-294 Edit Process screen offers
// under "+ Add job" — every dispatchable builtin template mode in canonical
// order plus the Ship hand-off sentinel. The reserved `_dispatch_preamble`
// (the wrapper prepended to every dispatch) is deliberately excluded; it is
// never a chain stage, mirroring the backend's stageModeAllowed gate. Keep
// in lockstep with model.BuiltinTemplate* — same dispatchable slugs.
export const EDITABLE_JOB_MODES: string[] = [
  'scope',
  'research',
  'plan',
  'plan_large',
  'design',
  'implement',
  'review',
  'fix_review',
  'ship',
];
