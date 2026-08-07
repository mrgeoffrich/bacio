import { useEffect, useState } from 'react';
import { Search, X } from 'lucide-react';
import { reportError } from '../errors';
import * as api from '../api';
import type { FeatureSummary } from '../api';
import { useActiveRepo } from '../state/RepoProvider';
import { useCards } from '../state/CardsProvider';
import { FILTERS } from './features/constants';
import { useFeatureFiltering } from './features/useFeatureFiltering';
import { useFeatureSelection } from './features/useFeatureSelection';
import { useFeatureDetail } from './features/useFeatureDetail';
import FeatureRow from './features/FeatureRow';
import FeatureDetailPane from './features/FeatureDetailPane';

// FeaturesView is the desktop feature browser: a two-pane mirror of
// the TUI's Features tab. The left pane lists the repo's features; the
// right pane shows the selected feature's description and the issues
// grouped under it.
//
// BACI-361: the active repo comes from useActiveRepo() and the board-cards
// refresh from useCards() — the route element takes no props. Flipping the
// per-feature "Show on board" toggle (BACI-177) fires refreshCards so the
// cached board cards reflect the change without waiting for the 10s poll
// (the toggle changes which cards ship over the wire).
//
// BACI-363: this file is the shell. The list derivations, URL<->selection
// sync, and detail fetch live in components/features/ hooks; the left-rail
// row and the right-pane detail are components/features/ components.
export default function FeaturesView() {
  const { activeBoard } = useActiveRepo();
  const { refreshCards: onChangeHidden } = useCards();
  const [features, setFeatures] = useState<FeatureSummary[]>([]);
  // BACI-199 / BACI-242 filter strip: which state bucket the list is
  // restricted to, defaulting to `active` so a fresh visit shows the
  // features the user is currently working on. Free-text search is applied
  // after the state filter so the chip counts reflect the unfiltered state
  // population even when search is narrowing the visible list.
  const [filter, setFilter] = useState('active');
  const [search, setSearch] = useState('');

  const repoSelected = !!activeBoard;

  // TEMP demo toggle — ?mock=1 appends a varied set of fake features
  // (including a cancelled row) so the filter strip can be reviewed
  // against the redesign. Remove with mockFeatures() before merging.
  const showMock =
    typeof window !== 'undefined' &&
    new URLSearchParams(window.location.search).has('mock');

  const { allFeatures, counts, visible } = useFeatureFiltering(
    features,
    showMock,
    filter,
    search,
  );
  const { selected, selectFeature } = useFeatureSelection(
    activeBoard,
    repoSelected,
    visible,
  );
  const { detail, setDetail, loading } = useFeatureDetail(
    activeBoard,
    selected,
    repoSelected,
  );

  // Reload the feature list whenever the selected repo changes, blanking the
  // detail pane so it doesn't show a stale feature during the swap.
  useEffect(() => {
    setDetail(null);
    if (!repoSelected) {
      setFeatures([]);
      return;
    }
    api
      .listFeatures(activeBoard)
      .then(setFeatures)
      .catch((err) => reportError(err, { headline: "Couldn't load epics" }));
  }, [activeBoard, repoSelected, setDetail]);

  if (!repoSelected) {
    return (
      <div className="mk-features">
        <div className="mk-features-empty">
          Select a repository to view its epics.
        </div>
      </div>
    );
  }

  return (
    <div className="mk-features">
      <aside className="mk-features-list-pane">
        <div
          className="mk-features-filter"
          role="tablist"
          aria-label="Filter epics by state"
        >
          {FILTERS.map((f) => (
            <button
              key={f.id}
              type="button"
              role="tab"
              aria-selected={filter === f.id}
              className={`mk-features-filter-chip ${
                filter === f.id ? 'is-active' : ''
              }`}
              onClick={() => setFilter(f.id)}
            >
              <span>{f.label}</span>
              <span className="mk-features-filter-count">{counts[f.id]}</span>
            </button>
          ))}
        </div>
        <label className="mk-features-search">
          <Search className="mk-features-search-icon" strokeWidth={2} aria-hidden="true" />
          <input
            type="search"
            className="mk-features-search-input"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="Filter by title or slug"
            aria-label="Filter epics"
          />
          {search && (
            <button
              type="button"
              className="mk-features-search-clear"
              aria-label="Clear filter"
              onClick={() => setSearch('')}
            >
              <X strokeWidth={2} aria-hidden="true" />
            </button>
          )}
        </label>

        <div className="mk-features-list">
          {allFeatures.length === 0 ? (
            <div className="mk-features-list-empty">
              No epics in this repository.
            </div>
          ) : visible.length === 0 ? (
            <div className="mk-features-list-empty">
              No {filter} epics.
            </div>
          ) : (
            visible.map((f) => (
              <FeatureRow
                key={f.slug}
                feature={f}
                isActive={selected === f.slug}
                onSelect={() => selectFeature(f.slug)}
              />
            ))
          )}
        </div>
      </aside>

      <div className="mk-features-main">
        {!selected ? (
          <div className="mk-features-empty">
            Pick an epic to see its details.
          </div>
        ) : loading ? (
          <div className="mk-features-empty">Loading…</div>
        ) : detail ? (
          <FeatureDetailPane
            activeBoard={activeBoard}
            detail={detail}
            onChangeHidden={onChangeHidden}
            onDetailChange={setDetail}
            onFeaturesChange={setFeatures}
          />
        ) : null}
      </div>
    </div>
  );
}
