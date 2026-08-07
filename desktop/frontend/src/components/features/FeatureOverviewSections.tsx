import { Link } from 'react-router';
import * as api from '../../api';
import type { FeatureDetail, FeatureSummary, FeatureLinkedIssue } from '../../api';
import { reportError } from '../../errors';
import InlineDescriptionEditor from '../issue/InlineDescriptionEditor';
import { issuePath } from '../../lib/routes';
import {
  AUTO_CLOSE_OPTIONS,
  COLLECT_HANDOFFS_OPTIONS,
  FEATURE_STATE_OPTIONS,
  SHOW_ON_BOARD_OPTIONS,
} from './constants';
import { useFeaturePropertyUpdate } from './useFeaturePropertyUpdate';
import FeaturePropertyToggle from './FeaturePropertyToggle';
import FeatureBranchEditor from './FeatureBranchEditor';
import FeatureLinkedDocsSection from './FeatureLinkedDocsSection';
import FeatureCommentsSection from './FeatureCommentsSection';

// FeatureOverviewSections wraps the historical right-pane body —
// properties / description / issues / docs / comments — so the Graph tab
// (BACI-236) can swap it out without dragging this block around.
//
// BACI-363: the four property toggles route through useFeaturePropertyUpdate,
// which owns the set->api->reconcile->refresh->error dance. The three boolean
// rows render through the reusable FeaturePropertyToggle (optimistic flip via
// useOptimisticToggle); the tri-state State row stays a bespoke segmented
// control driven by the hook's await-then-apply `update`.
type FeatureOverviewSectionsProps = {
  activeBoard: string;
  detail: FeatureDetail;
  onChangeHidden: () => void;
  onDetailChange: (detail: FeatureDetail) => void;
  onFeaturesChange: (features: FeatureSummary[]) => void;
};

export default function FeatureOverviewSections({
  activeBoard,
  detail,
  onChangeHidden,
  onDetailChange,
  onFeaturesChange,
}: FeatureOverviewSectionsProps) {
  const { update, reconcile } = useFeaturePropertyUpdate(
    activeBoard,
    onDetailChange,
    onFeaturesChange,
  );
  return (
    <>
      <section className="mk-features-properties">
        <div className="mk-features-prop">
          <label className="mk-features-prop-label">State</label>
          <div
            className="mk-segmented"
            role="group"
            aria-label="Epic state"
          >
            {FEATURE_STATE_OPTIONS.map((opt) => {
              const current = detail.state || 'active';
              return (
                <button
                  key={opt.id}
                  className={`mk-segmented-btn ${
                    current === opt.id ? 'is-active' : ''
                  }`}
                  aria-pressed={current === opt.id}
                  onClick={() => {
                    if (current === opt.id) return;
                    update({
                      persist: () => api.setFeatureState(activeBoard, detail.slug, opt.id),
                      errorHeadline: "Couldn't update state",
                    });
                  }}
                >
                  {opt.label}
                </button>
              );
            })}
          </div>
        </div>

        <FeaturePropertyToggle
          label="Auto Close"
          ariaLabel="Auto-close this epic when every child is terminal"
          options={AUTO_CLOSE_OPTIONS}
          value={!detail.stateManual}
          persist={(next) => api.setFeatureAutoClose(activeBoard, detail.slug, next)}
          onApplied={(updated) => reconcile(updated)}
          errorHeadline="Couldn't update auto-close"
          hint="When on, the auto-completion sweep can promote this epic to done/cancelled once every child issue is terminal. Turn off for long-lived catch-all epics."
        />

        <FeaturePropertyToggle
          label="Collect handoffs"
          ariaLabel="Collect worker handoff comments on this epic"
          options={COLLECT_HANDOFFS_OPTIONS}
          value={detail.collectHandoffs}
          persist={(next) => api.setFeatureCollectHandoffs(activeBoard, detail.slug, next)}
          onApplied={(updated) => reconcile(updated)}
          errorHeadline="Couldn't update collect-handoffs"
          hint="When on, worker close-outs append a handoff comment so siblings inherit context. Turn off for standing buckets like bugs / maintenance."
          hintTitle="When on, implement-worker close-outs append a handoff comment to this epic so sibling workers inherit context. Turn off for standing bucket epics whose unrelated children make handoffs noise."
        />

        <FeaturePropertyToggle
          label="Show on board"
          ariaLabel="Show this epic's cards on the board"
          options={SHOW_ON_BOARD_OPTIONS}
          value={!detail.hiddenOnBoard}
          persist={(next) => api.setFeatureHiddenOnBoard(activeBoard, detail.slug, !next)}
          onApplied={(updated) => reconcile(updated, onChangeHidden)}
          errorHeadline="Couldn't update visibility"
          hint="Hide this epic's cards from the kanban board."
          hintTitle="When hidden, every kanban card belonging to this epic is dropped from the board on this machine. Lives alongside the TUI epic picker."
        />

        <FeatureBranchEditor
          activeBoard={activeBoard}
          detail={detail}
          onDetailChange={onDetailChange}
          onFeaturesChange={onFeaturesChange}
        />
      </section>

      <InlineDescriptionEditor
        description={detail.description}
        readOnly={false}
        onSave={async (body: string) => {
          // Mirror App.saveDescription: report failures and re-throw so the
          // editor stays in edit mode (and keeps the buffer) on error.
          try {
            const updated = await api.setFeatureDescription(
              activeBoard,
              detail.slug,
              body,
            );
            onDetailChange(updated);
          } catch (err) {
            reportError(err, { headline: "Couldn't update description" });
            throw err;
          }
        }}
      />

      <section className="mk-features-section">
        <div className="mk-features-label">
          Issues · {detail.issues.length}
        </div>
        {detail.issues.length === 0 ? (
          <p className="mk-features-text mk-meta-empty">
            No issues linked yet.
          </p>
        ) : (
          <ul className="mk-features-issues">
            {detail.issues.map((iss: FeatureLinkedIssue) => (
              <li key={iss.key}>
                <Link
                  to={issuePath(activeBoard, iss.key)}
                  className="mk-features-issue"
                >
                  <span className="mk-card-id">{iss.key}</span>
                  <span className="mk-features-issue-title">{iss.title}</span>
                  <span className={`mk-pill mk-status-${iss.state}`}>
                    {iss.stateLabel}
                  </span>
                </Link>
              </li>
            ))}
          </ul>
        )}
      </section>

      <FeatureLinkedDocsSection activeBoard={activeBoard} documents={detail.documents ?? []} />

      <FeatureCommentsSection
        repoPrefix={activeBoard}
        detail={detail}
        onChange={onDetailChange}
      />
    </>
  );
}
