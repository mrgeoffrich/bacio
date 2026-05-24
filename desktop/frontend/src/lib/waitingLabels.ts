// BACI-145: user-facing label for the kanban / IssueLockBanner spinner.
//
// The wording is a single source of truth on the TS side, mirrored by
// the Go-side `boardcards.WaitingStateLabel` (see internal/boardcards/
// cards.go). Both must agree — the kanban card and TUI render Go-side
// strings, the React tree's spinner + lock banner render this one.
// `TestWaitingStateLabel` in internal/boardcards/cards_test.go pins
// the Go side; pin a follow-up snapshot test here if the wording
// starts drifting between surfaces.
//
// WaitingState can arrive from two transports:
//   * Wails bindings (api.ts) — class instance with `kind` populated.
//   * HTTP wire (api.http.ts) — plain object with the same `kind` /
//     `actionLabel` shape.
// Both reach the React layer behind the same interface; the function
// below works with either by reading the duck-typed fields.
import type { WaitingState } from '../api';

export function waitingStateLabel(ws: WaitingState | null | undefined): string {
  if (!ws) return '';
  switch (ws.kind) {
    case 'delivered':
      return ws.actionLabel
        ? `Worker has the ${ws.actionLabel} job`
        : 'Worker is on this job';
    case 'queued_blocked':
      return ws.actionLabel
        ? `Waiting on ${ws.actionLabel} job to finish`
        : 'Waiting on prior job to finish';
    case 'queued_no_agent':
      return 'Waiting for an available agent';
    default:
      return '';
  }
}
