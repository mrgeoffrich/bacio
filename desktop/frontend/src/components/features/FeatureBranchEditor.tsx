import { useEffect, useState } from 'react';
import { X } from 'lucide-react';
import * as api from '../../api';
import type { FeatureDetail, FeatureSummary } from '../../api';
import { reportError } from '../../errors';
import Icon from '../Icon';
import { isValidBranchName } from '../../lib/branchName';

// FeatureBranchEditor (BACI-231) is the integration-branch Properties row on
// the feature detail pane — the editable branch the feature ships against.
// Empty input clears the branch (the feature ships straight to main again).
// Save fires on blur AND on Enter; client-side validation via
// lib/branchName.isValidBranchName blocks the round-trip when the input is
// malformed, so the user sees an inline error before the server's identical
// rejection. Parallel to the Show-on-board and State rows above it.
type FeatureBranchEditorProps = {
  activeBoard: string;
  detail: FeatureDetail;
  onDetailChange: (detail: FeatureDetail) => void;
  onFeaturesChange: (features: FeatureSummary[]) => void;
};

export default function FeatureBranchEditor({
  activeBoard,
  detail,
  onDetailChange,
  onFeaturesChange,
}: FeatureBranchEditorProps) {
  // Local edit buffer — the user can type without firing a save on
  // every keystroke. Resets when the upstream detail.branchName
  // changes (e.g. another tab switched away and back).
  const [value, setValue] = useState(detail.branchName || '');
  const [error, setError] = useState('');
  useEffect(() => {
    setValue(detail.branchName || '');
    setError('');
  }, [detail.branchName]);

  const persisted = detail.branchName || '';
  const trimmed = value;

  const save = async () => {
    // No-op if unchanged — avoid a round trip when the user just
    // tabbed out without editing.
    if (trimmed === persisted) {
      setError('');
      return;
    }
    const check = isValidBranchName(trimmed);
    if (!check.ok) {
      setError(check.reason);
      return;
    }
    try {
      const updated = await api.setFeatureBranchName(
        activeBoard,
        detail.slug,
        trimmed,
      );
      setError('');
      onDetailChange(updated);
      try {
        const feats = await api.listFeatures(activeBoard);
        onFeaturesChange(feats);
      } catch {
        // non-fatal — next selection refresh picks it up.
      }
    } catch (err) {
      const rawMessage =
        err instanceof Error
          ? err.message
          : typeof err === 'object' && err !== null && 'message' in err
            ? String((err as { message: unknown }).message)
            : undefined;
      const message = rawMessage || String(err);
      // Surface store-side validation messages inline; route the rest
      // through the global error toast (network blip, etc).
      if (/^branch_name/.test(message)) {
        setError(message);
      } else {
        reportError(err, { headline: "Couldn't update branch" });
      }
    }
  };

  return (
    <div className="mk-features-prop">
      <label className="mk-features-prop-label" htmlFor="mk-features-branch-input">
        Integration branch
      </label>
      <div className="mk-features-prop-input-wrap">
        <Icon name="branch" />
        <input
          id="mk-features-branch-input"
          type="text"
          className="mk-features-prop-input"
          placeholder="main (default)"
          value={value}
          spellCheck={false}
          autoComplete="off"
          onChange={(e) => {
            setValue(e.target.value);
            // Clear the inline error eagerly as the user types — the
            // next blur / Enter re-runs validation.
            if (error) setError('');
          }}
          onBlur={save}
          onKeyDown={(e) => {
            if (e.key === 'Enter') {
              e.preventDefault();
              save();
            } else if (e.key === 'Escape') {
              e.preventDefault();
              setValue(persisted);
              setError('');
            }
          }}
        />
        {value && (
          <button
            type="button"
            className="mk-features-prop-clear"
            aria-label="Clear branch"
            onClick={() => {
              setValue('');
              setError('');
              // Immediately persist the clear — matches the chip's
              // expectation that clicking × routes back to main.
              (async () => {
                try {
                  const updated = await api.setFeatureBranchName(
                    activeBoard,
                    detail.slug,
                    '',
                  );
                  onDetailChange(updated);
                  try {
                    const feats = await api.listFeatures(activeBoard);
                    onFeaturesChange(feats);
                  } catch {
                    // non-fatal.
                  }
                } catch (err) {
                  reportError(err, { headline: "Couldn't clear branch" });
                }
              })();
            }}
          >
            <X strokeWidth={2} />
          </button>
        )}
      </div>
      {error && <p className="mk-features-prop-error">{error}</p>}
      <p
        className="mk-features-prop-hint"
        title="Sets the per-feature integration branch. New dispatches under this feature target the named branch; in-flight work keeps its current base."
      >
        Issues under this feature ship to this branch instead of <code>main</code>.
      </p>
    </div>
  );
}
