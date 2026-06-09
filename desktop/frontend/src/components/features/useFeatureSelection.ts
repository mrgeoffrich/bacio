import { useEffect, useState } from 'react';
import { useNavigate, useParams } from 'react-router';
import type { FeatureSummary } from '../../api';
import { featurePath } from '../../lib/routes';

// FeatureSelection is what useFeatureSelection hands the FeaturesView shell:
// the currently-selected slug (mirrored to/from the URL) and a `selectFeature`
// helper the list rows call to navigate.
export type FeatureSelection = {
  selected: string | null;
  selectFeature: (slug: string) => void;
};

// useFeatureSelection owns the URL<->selection sync carved out of FeaturesView
// (BACI-203 / BACI-363). The selected slug is mirrored to/from the URL via
// useParams + navigate: row clicks call selectFeature(slug) (an outbound
// navigate); the URL is the source of truth so refresh / deep-link /
// back-forward all work, but the component still reads `selected` so the
// layout / data effects stay unchanged. It also owns the auto-select effect
// that lands the detail pane on the first visible feature when the current
// selection drops out of view (a filter change, a repo switch).
export function useFeatureSelection(
  activeBoard: string,
  repoSelected: boolean,
  visible: FeatureSummary[],
): FeatureSelection {
  const navigate = useNavigate();
  const { slug: slugParam } = useParams();
  const [selected, setSelected] = useState<string | null>(slugParam ?? null);

  // BACI-203: sync `selected` from useParams when the URL changes
  // (back/forward, deep-link, manual edit). Outbound writes happen in
  // selectFeature via navigate(featurePath(...)). The mount-time read in
  // useState's initialiser already covers the first paint; this covers
  // subsequent URL changes.
  useEffect(() => {
    if (slugParam && slugParam !== selected) {
      setSelected(slugParam);
    } else if (!slugParam && selected) {
      // The user navigated to /features (no slug) — clear the
      // selection so the auto-select effect below can land on the
      // first visible feature.
      setSelected(null);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [slugParam]);

  // Auto-select the first visible feature so the detail pane isn't
  // empty on first load. Re-runs when the filter changes if the
  // currently-selected slug drops out of the visible set — keeps the
  // detail pane in lockstep with what the user is actually looking at.
  //
  // BACI-203: auto-selection writes through navigate so the URL
  // stays in sync. Replaces the bare setSelected from the BACI-54
  // era — the URL is the source of truth now.
  useEffect(() => {
    if (!repoSelected || visible.length === 0) return;
    const currentInView = visible.some((f) => f.slug === selected);
    if (!currentInView) navigate(featurePath(activeBoard, visible[0].slug), { replace: true });
  }, [visible, selected, repoSelected, navigate, activeBoard]);

  const selectFeature = (slug: string) => navigate(featurePath(activeBoard, slug));

  return { selected, selectFeature };
}
