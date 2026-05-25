import React from 'react';
import { waitingStateLabel } from '../../lib/waitingLabels';

// IssueLockBanner names the agent holding (or about to hold) the issue
// and surfaces the BACI-51 "queued, no agent yet" affordance — the
// spinner doubles as a cancel button for the waiting dispatch. Returns
// null when the issue is neither taken nor waiting; render unconditionally
// from the workspace head and let it noop on free issues.
//
// `taken` takes render precedence: once an agent claims, waitingForClaim
// is cleared, so the two shouldn't overlap; render defensively in case
// they do.
//
// BACI-145: when waitingState is passed, the banner shows the same
// explanatory copy ("Waiting for an available agent" / "Waiting on
// Ship it job to finish" / "Worker has the Ship it job") the kanban
// card renders. Falls back to the legacy "queued · waiting for an
// agent" string when the brief is from a pre-145 server (waitingState
// undefined but waiting true).
export default function IssueLockBanner({ taken, waiting, waitingState, claimant, onCancelWaiting }) {
  if (taken && claimant) {
    return (
      <div className="mk-workspace-lock-banner mk-pill mk-status-busy">
        <span>taken · {claimant.agentName || claimant.sessionId.slice(0, 12)}</span>
        {claimant.prompt && <span className="mk-workspace-lock-prompt">— {claimant.prompt}</span>}
      </div>
    );
  }
  if (waiting) {
    const delivered = waitingState?.kind === 'delivered';
    const label = waitingState
      ? waitingStateLabel(waitingState)
      : 'queued · waiting for an agent';
    return (
      <div className="mk-workspace-lock-banner mk-pill mk-status-waiting">
        {delivered ? (
          <span
            className="mk-card-spinner"
            role="status"
            aria-label={label}
          />
        ) : (
          <button
            type="button"
            className="mk-card-spinner mk-card-spinner-btn"
            aria-label="Cancel queued dispatch"
            title="Cancel queued dispatch"
            onClick={onCancelWaiting}
          />
        )}
        <span>{label}{!delivered && ' · click ↑ to cancel'}</span>
      </div>
    );
  }
  return null;
}
