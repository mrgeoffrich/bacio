import React from 'react';

// IssueLockBanner names the agent holding (or about to hold) the issue
// and surfaces the BACI-51 "queued, no agent yet" affordance — the
// spinner doubles as a cancel button for the waiting dispatch. Returns
// null when the issue is neither taken nor waiting; render unconditionally
// from the workspace head and let it noop on free issues.
//
// `taken` takes render precedence: once an agent claims, waitingForClaim
// is cleared, so the two shouldn't overlap; render defensively in case
// they do.
export default function IssueLockBanner({ taken, waiting, claimant, onCancelWaiting }) {
  if (taken && claimant) {
    return (
      <div className="mk-workspace-lock-banner mk-pill mk-status-busy">
        <span>taken · {claimant.agentName || claimant.sessionId.slice(0, 12)}</span>
        {claimant.prompt && <span className="mk-workspace-lock-prompt">— {claimant.prompt}</span>}
      </div>
    );
  }
  if (waiting) {
    return (
      <div className="mk-workspace-lock-banner mk-pill mk-status-waiting">
        <button
          type="button"
          className="mk-card-spinner mk-card-spinner-btn"
          aria-label="Cancel queued dispatch"
          onClick={onCancelWaiting}
        />
        <span>queued · waiting for an agent · click ↑ to cancel</span>
      </div>
    );
  }
  return null;
}
