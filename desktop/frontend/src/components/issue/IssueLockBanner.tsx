import { waitingStateLabel } from '../../lib/waitingLabels.ts';
import type { WaitingState } from '../../api';
import type { ClaimantDTO } from '../../../bindings/github.com/mrgeoffrich/bacio/internal/agentcards';

// IssueLockBanner names the agent holding (or about to hold) the issue
// and surfaces the BACI-51 "queued, no agent yet" affordance — the
// spinner doubles as a cancel button for the waiting dispatch (unless
// the BACI-130 delivered branch fires, in which case the cancel
// disappears — cancel-after-delivery is rejected by the store).
// Returns null when the issue is neither taken nor waiting; render
// unconditionally from the workspace head and let it noop on free
// issues.
//
// `taken` takes render precedence: once an agent claims, the claim
// row pre-empts the waiting derivation in the caller, so the two
// shouldn't overlap; render defensively in case they do.
//
// BACI-145: the inline waiting label uses the same wording as the
// kanban card via the shared `waitingStateLabel` helper, so the two
// surfaces stay in lockstep.
type IssueLockBannerProps = {
  taken: boolean;
  waiting: boolean;
  waitingState: WaitingState | null;
  claimant: ClaimantDTO | null;
  onCancelWaiting: () => void;
};

export default function IssueLockBanner({
  taken,
  waiting,
  waitingState,
  claimant,
  onCancelWaiting,
}: IssueLockBannerProps) {
  if (taken && claimant) {
    return (
      <div className="mk-workspace-lock-banner mk-pill mk-status-busy">
        <span>taken · {claimant.agentName || claimant.sessionId.slice(0, 12)}</span>
        {claimant.prompt && <span className="mk-workspace-lock-prompt">— {claimant.prompt}</span>}
      </div>
    );
  }
  if (waiting) {
    const delivered = waitingState && waitingState.kind === 'delivered';
    const label = waitingStateLabel(waitingState) || 'queued · waiting for an agent';
    return (
      <div className="mk-workspace-lock-banner mk-pill mk-status-waiting">
        {delivered ? (
          <span
            className="mk-card-spinner"
            role="status"
            aria-label="Dispatch delivered to worker"
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
        <span>{label}</span>
      </div>
    );
  }
  return null;
}
