// waitingLabels — BACI-145. Single source of truth for the wording
// that surfaces next to the queued / delivered dispatch spinner on a
// kanban card (and the IssueLockBanner copy in the issue workspace).
// Keeps the kanban card label and the workspace banner aligned: edit
// the strings here once and both surfaces pick up the new wording on
// the next render.
//
// The Go side derives the matching ActionLabel from the prompt
// template (template.ActionLabel override, else
// BuiltinTemplateActionLabel(slug), else DeriveActionLabel(Name)). We
// trust the value the server sends and just compose it into the
// label here — no client-side derivation, so a user-renamed template
// shows the right wording without a TS deploy.

import type { WaitingState } from '../api.http';

// waitingStateLabel returns the human-readable explanation that
// renders next to the spinner. Falls back to a sensible default when
// the action label is missing (e.g. an unknown / renamed mode).
export function waitingStateLabel(ws: WaitingState | null | undefined): string {
  if (!ws) return '';
  const verb = (ws.actionLabel ?? '').trim();
  switch (ws.kind) {
    case 'queued_no_agent':
      return 'Waiting for an available agent';
    case 'queued_blocked':
      // "Waiting on Ship it job to finish" — the imperative
      // ActionLabel is grammatically the "Ship it" / "Plan" / "Fix
      // review" form from the dispatch dropdown the user clicked.
      return verb ? `Waiting on ${verb} job to finish` : 'Waiting on prior job to finish';
    case 'delivered':
      // BACI-130 sub-state: the worker has accepted the Task. The
      // spinner stays but the cancel affordance drops; the label
      // makes the state visible at a glance.
      return verb ? `Worker has the ${verb} job` : 'Worker is on this job';
    default:
      // Defensive: a kind the renderer doesn't know about (e.g. a
      // newer server we haven't shipped TypeScript for yet) reads
      // through as the bare wait-state — better than an empty
      // explanation.
      return 'Waiting';
  }
}
