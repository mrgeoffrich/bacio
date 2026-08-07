import { lazy, Suspense, useState } from 'react';
import { Link } from 'react-router';
import { Pencil } from 'lucide-react';
import * as api from '../../api';
import type { FeatureDetail, FeatureSummary } from '../../api';
import { reportError } from '../../errors';
import { editEpicPath } from '../../lib/routes';
import FeatureEmojiPicker from '../FeatureEmojiPicker';
import { VIEW_MODE_OPTIONS } from './constants';
import { shortDate, stateLabelFor } from './format';
import FeatureOverviewSections from './FeatureOverviewSections';

// BACI-236: the dependency-graph view is lazy-loaded so the
// @xyflow/react chunk (~150 KB gzipped) only lands when the user
// actually opens the Graph tab. The Overview tab — the historical
// default — pays nothing for the new affordance.
const FeatureDependencyGraph = lazy(() => import('../FeatureDependencyGraph'));

// FeatureDetailPane is the right-side drawer body. Hosts the emoji picker /
// title / metadata header plus the two view modes — Overview (the historical
// layout: properties / description / issues / docs / comments) and Graph (a
// dependency-graph render of the same issues with directed `blocks` edges).
// The segmented control above the title row flips between them; the toggle is
// a local useState so switching back to Overview is instant and never
// refetches.
type FeatureDetailPaneProps = {
  activeBoard: string;
  detail: FeatureDetail;
  onChangeHidden: () => void;
  onDetailChange: (detail: FeatureDetail) => void;
  onFeaturesChange: (features: FeatureSummary[]) => void;
};

export default function FeatureDetailPane({
  activeBoard,
  detail,
  onChangeHidden,
  onDetailChange,
  onFeaturesChange,
}: FeatureDetailPaneProps) {
  const [viewMode, setViewMode] = useState<string>('overview');
  return (
    <div className="mk-features-detail">
      <div
        className="mk-features-viewmode mk-segmented"
        role="tablist"
        aria-label="Epic view mode"
      >
        {VIEW_MODE_OPTIONS.map((opt) => (
          <button
            key={opt.id}
            type="button"
            role="tab"
            aria-selected={viewMode === opt.id}
            className={`mk-segmented-btn ${
              viewMode === opt.id ? 'is-active' : ''
            }`}
            onClick={() => setViewMode(opt.id)}
          >
            {opt.label}
          </button>
        ))}
      </div>
      <div className="mk-features-title-row">
        <FeatureEmojiPicker
          value={detail.emoji || ''}
          onSelect={async (next: string) => {
            try {
              const updated = await api.setFeatureEmoji(
                activeBoard,
                detail.slug,
                next,
              );
              onDetailChange(updated);
            } catch (err) {
              reportError(err, { headline: "Couldn't update emoji" });
            }
          }}
        />
        <h2 className="mk-features-title">{detail.title}</h2>
        <span className={`mk-pill mk-status-${detail.state || 'active'}`}>
          {stateLabelFor(detail.state)}
        </span>
        {/* The Edit page is ADDITIVE — nothing on this pane was removed
            for it. What it adds that has no other home anywhere is the
            title editor; the rest is a bulk-edit convenience. Same shape
            as the docs folder page's Rename button. */}
        <Link
          to={editEpicPath(activeBoard, detail.slug)}
          className="mk-btn-secondary mk-features-edit-btn"
          title="Edit this epic"
        >
          <Pencil size={14} strokeWidth={2} aria-hidden="true" />
          <span>Edit</span>
        </Link>
      </div>
      <div className="mk-features-meta">
        <span className="mk-mono">{detail.slug}</span>
        {' · '}created {shortDate(detail.createdAt)}
        {' · '}updated {shortDate(detail.updatedAt)}
      </div>

      {viewMode === 'graph' ? (
        <Suspense
          fallback={<div className="mk-features-empty">Loading graph…</div>}
        >
          <FeatureDependencyGraph
            repoPrefix={activeBoard}
            slug={detail.slug}
          />
        </Suspense>
      ) : (
        <FeatureOverviewSections
          activeBoard={activeBoard}
          detail={detail}
          onChangeHidden={onChangeHidden}
          onDetailChange={onDetailChange}
          onFeaturesChange={onFeaturesChange}
        />
      )}
    </div>
  );
}
