import { EyeOff } from 'lucide-react';
import type { FeatureSummary } from '../../api';
import Icon from '../Icon';
import { shortBranchLabel } from '../../lib/branchName';
import { relTime, stateLabelFor } from './format';

// FeatureRow renders a single feature in the left list — single-height,
// with the emoji + title on the top line and slug + state + relative
// updated time on the meta line. Replaces the two-line slug-then-title
// layout which wrapped awkwardly at narrow widths.
type FeatureRowProps = {
  feature: FeatureSummary;
  isActive: boolean;
  onSelect: () => void;
};

export default function FeatureRow({ feature, isActive, onSelect }: FeatureRowProps) {
  return (
    <button
      type="button"
      className={`mk-features-item mk-features-item--${
        feature.state || 'active'
      } ${isActive ? 'is-active' : ''}`}
      onClick={onSelect}
    >
      <span className="mk-features-item-top">
        {feature.emoji ? (
          <span className="mk-features-item-emoji" aria-hidden="true">
            {feature.emoji}
          </span>
        ) : (
          <span className="mk-features-item-emoji-empty" aria-hidden="true" />
        )}
        <span className="mk-features-item-title" title={feature.title}>
          {feature.title}
        </span>
        <span
          className={`mk-pill mk-status-${feature.state || 'active'}`}
        >
          {stateLabelFor(feature.state)}
        </span>
      </span>
      <span className="mk-features-item-meta">
        <span className="mk-features-item-slug">{feature.slug}</span>
        {feature.branchName && (
          <>
            <span className="mk-features-item-meta-sep">·</span>
            <span
              className="mk-features-item-branch"
              title={`Ships to ${feature.branchName}`}
            >
              <Icon name="branch" />
              {shortBranchLabel(feature.branchName)}
            </span>
          </>
        )}
        <span className="mk-features-item-meta-sep">·</span>
        <span>{relTime(feature.updatedAt)}</span>
        {feature.hiddenOnBoard && (
          <span
            className="mk-features-item-hidden"
            title="Hidden from the kanban board"
          >
            <EyeOff strokeWidth={2} />
          </span>
        )}
      </span>
    </button>
  );
}
