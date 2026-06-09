import { useEffect, useState } from 'react';
import type { Dispatch, SetStateAction } from 'react';
import * as api from '../../api';
import type { FeatureDetail } from '../../api';
import { reportError } from '../../errors';
import { mockFeatures } from './mockFeatures';

// FeatureDetailState is what useFeatureDetail hands the FeaturesView shell:
// the loaded detail (or null), the loading flag, and the raw setter so a
// property mutation can apply the server's returned FeatureDetail in place,
// and the shell can blank the pane on a repo switch.
export type FeatureDetailState = {
  detail: FeatureDetail | null;
  setDetail: Dispatch<SetStateAction<FeatureDetail | null>>;
  loading: boolean;
};

// useFeatureDetail owns the selected feature's detail fetch carved out of
// FeaturesView (BACI-363). It loads the chosen feature's description + linked
// issues whenever `selected` (or the repo) changes, short-circuiting the
// `mock-` slugs the `?mock=1` demo injects with a hand-rolled FeatureDetail so
// the filter strip can be exercised against rows the live DB doesn't carry.
export function useFeatureDetail(
  activeBoard: string,
  selected: string | null,
  repoSelected: boolean,
): FeatureDetailState {
  const [detail, setDetail] = useState<FeatureDetail | null>(null);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (!selected || !repoSelected) return;
    // TEMP demo branch — mock slugs short-circuit the API call and
    // render a hand-rolled FeatureDetail so the filter strip can be
    // exercised against a cancelled row that doesn't exist server-side.
    if (selected.startsWith('mock-')) {
      const m = mockFeatures().find((f) => f.slug === selected);
      if (m) {
        setDetail({
          slug: m.slug,
          title: m.title,
          description:
            'This is a mock feature injected by `?mock=1` so the filter strip can be tested against rows the live DB doesn\'t carry. The detail pane is hand-rolled — clicking the State or Show-on-board toggles will fail silently against the backend.',
          emoji: m.emoji,
          branchName: '',
          state: m.state,
          stateManual: false,
          collectHandoffs: true,
          createdAt: m.updatedAt,
          updatedAt: m.updatedAt,
          issues: [],
          comments: [],
          documents: [],
          hiddenOnBoard: m.hiddenOnBoard,
        });
        setLoading(false);
        return;
      }
    }
    setLoading(true);
    api
      .getFeature(activeBoard, selected)
      .then((d) => {
        setDetail(d);
        setLoading(false);
      })
      .catch((err) => {
        reportError(err, { headline: "Couldn't load feature" });
        setLoading(false);
      });
  }, [selected, activeBoard, repoSelected]);

  return { detail, setDetail, loading };
}
