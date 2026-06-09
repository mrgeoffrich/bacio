import { useEffect } from 'react';
import type { FeatureDetail } from '../../api';
import { useOptimisticToggle } from '../../lib/hooks/useOptimisticToggle';

// FeaturePropertyToggle is the reusable boolean Properties row on the feature
// detail pane (BACI-363) — one mk-segmented On/Off (or Show/Hide) pair plus an
// optional hint. It backs the Auto Close / Collect handoffs / Show on board
// rows that used to be three near-identical inline blocks in FeaturesView.
//
// BACI-357: the visible optimistic flip lives in useOptimisticToggle — the
// button flips on screen immediately, persists in the background, and reverts
// (reporting through the error bus) if the persist rejects. On success the
// server's returned FeatureDetail is handed to `onApplied` so the detail pane
// reconciles and the summary list refreshes. The tri-state State row can't use
// this (it isn't a boolean) and stays a bespoke segmented control in
// FeatureOverviewSections.
//
// `value` is the source of truth from the loaded detail; a fetch / reconcile
// re-seeds the toggle through the setValue effect below, bypassing the
// persist machinery the same way PipelineView's display toggles do.
type Option = { id: boolean; label: string };

type FeaturePropertyToggleProps = {
  label: string;
  ariaLabel: string;
  options: Option[];
  value: boolean;
  persist: (next: boolean) => Promise<FeatureDetail>;
  onApplied: (updated: FeatureDetail) => void;
  errorHeadline: string;
  // Visible hint paragraph under the control. Omit for a bare row.
  hint?: string;
  // The longer `title` tooltip on the hint; defaults to `hint` when omitted.
  hintTitle?: string;
};

export default function FeaturePropertyToggle({
  label,
  ariaLabel,
  options,
  value,
  persist,
  onApplied,
  errorHeadline,
  hint,
  hintTitle,
}: FeaturePropertyToggleProps) {
  const { value: current, setValue, set } = useOptimisticToggle(
    value,
    async (next: boolean) => {
      const updated = await persist(next);
      onApplied(updated);
    },
    { errorHeadline },
  );

  // Re-seed from the loaded detail when it changes (e.g. a reconcile lands or
  // another row's mutation returns a fresh FeatureDetail). During an in-flight
  // optimistic flip `value` is still the old prop, so this never clobbers it.
  useEffect(() => {
    setValue(value);
  }, [value, setValue]);

  return (
    <div className="mk-features-prop">
      <label className="mk-features-prop-label">{label}</label>
      <div className="mk-segmented" role="group" aria-label={ariaLabel}>
        {options.map((opt) => (
          <button
            key={String(opt.id)}
            className={`mk-segmented-btn ${current === opt.id ? 'is-active' : ''}`}
            aria-pressed={current === opt.id}
            onClick={() => set(opt.id)}
          >
            {opt.label}
          </button>
        ))}
      </div>
      {hint && (
        <p className="mk-features-prop-hint" title={hintTitle ?? hint}>
          {hint}
        </p>
      )}
    </div>
  );
}
