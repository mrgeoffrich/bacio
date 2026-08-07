import type { ReactNode } from 'react';
import * as api from '../../api';
import type { FeatureDetail } from '../../api';
import { FEATURE_STATE_OPTIONS } from './constants';
import type { FeaturePropertyUpdate } from './useFeaturePropertyUpdate';

// FeatureStateControl is the tri-state `State` Properties row — the one
// property control that can't route through FeaturePropertyToggle,
// because that hook is built on a boolean optimistic flip and this has
// three values. It stays what it always was: an await-then-apply write
// through useFeaturePropertyUpdate.update.
//
// Extracted from FeatureOverviewSections so the Edit Epic page renders
// the EXACT SAME control the detail pane does rather than a lookalike.
// That is load-bearing for the two-speed design: the four properties are
// signposted as "applied immediately, same as the detail pane", and a
// second implementation would be the first thing to drift out of that
// promise.
type FeatureStateControlProps = {
  activeBoard: string;
  detail: FeatureDetail;
  update: FeaturePropertyUpdate['update'];
  // Optional trailing badge beside the label — the Edit page's `SAVED`
  // flash. The detail pane passes nothing.
  badge?: ReactNode;
};

export default function FeatureStateControl({
  activeBoard,
  detail,
  update,
  badge,
}: FeatureStateControlProps) {
  const current = detail.state || 'active';
  return (
    <div className="mk-features-prop">
      <label className="mk-features-prop-label">
        State
        {badge}
      </label>
      <div className="mk-segmented" role="group" aria-label="Epic state">
        {FEATURE_STATE_OPTIONS.map((opt) => (
          <button
            key={opt.id}
            // Explicit type: the Edit Epic page renders this inside a page
            // that also carries a <form>, where a bare <button> would
            // default to submit and fire the batched Details save.
            type="button"
            className={`mk-segmented-btn ${current === opt.id ? 'is-active' : ''}`}
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
        ))}
      </div>
    </div>
  );
}
