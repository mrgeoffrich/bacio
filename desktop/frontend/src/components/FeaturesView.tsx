import { useMemo, useState, useEffect, lazy, Suspense } from 'react';
import { Link, useNavigate, useParams } from 'react-router';
import { EyeOff, Search, X } from 'lucide-react';
import { reportError } from '../errors';
import { useOptimisticMutation } from '../lib/hooks/useOptimisticMutation';
import * as api from '../api';
import type {
  FeatureSummary,
  FeatureDetail,
  FeatureLinkedIssue,
  FeatureLinkedDoc,
  FeatureCommentDTO,
} from '../api';
import MarkdownView from '../lib/markdownView';
import CommentComposer from './issue/CommentComposer';
import InlineDescriptionEditor from './issue/InlineDescriptionEditor';
import FeatureEmojiPicker from './FeatureEmojiPicker';
import Icon from './Icon';
import { documentPath, featurePath, issuePath } from '../lib/routes';
import { isValidBranchName, shortBranchLabel } from '../lib/branchName';

// BACI-236: the dependency-graph view is lazy-loaded so the
// @xyflow/react chunk (~150 KB gzipped) only lands when the user
// actually opens the Graph tab. The Overview tab — the historical
// default — pays nothing for the new affordance.
const FeatureDependencyGraph = lazy(() => import('./FeatureDependencyGraph'));

// BACI-236: Overview vs Graph tab on the right pane. The id matches
// the `viewMode` useState so the segmented control mirrors the active
// tab. Overview is the historical default; switching tabs is a local
// state flip (no refetch / no URL change in v1).
const VIEW_MODE_OPTIONS = [
  { id: 'overview', label: 'Overview' },
  { id: 'graph', label: 'Graph' },
];

// BACI-177: per-feature "Show on board" toggle. The id is the value
// the toggle should set on the feature — true = show on board (the
// default), false = hide. Matches the two-button mk-segmented shape
// used elsewhere (theme, show-archived, hide-empty-columns).
const SHOW_ON_BOARD_OPTIONS = [
  { id: true, label: 'Show' },
  { id: false, label: 'Hide' },
];

// BACI-250: per-feature auto-close toggle. The id is the `enabled`
// value the API expects — true = auto-close ON (default; the sweep
// may promote the feature once every child is terminal), false =
// auto-close OFF (long-lived catch-all stays `active`). Same shape as
// SHOW_ON_BOARD_OPTIONS.
const AUTO_CLOSE_OPTIONS = [
  { id: true, label: 'On' },
  { id: false, label: 'Off' },
];

// BACI-333: per-feature collect-handoffs options. The id is the boolean
// the API expects — true = collect worker close-out handoff comments
// (the default), false = silence a standing bucket like `bugs` /
// `maintenance`. Same shape as AUTO_CLOSE_OPTIONS.
const COLLECT_HANDOFFS_OPTIONS = [
  { id: true, label: 'On' },
  { id: false, label: 'Off' },
];

// BACI-199: per-feature state options. The id is the canonical
// state string ParseFeatureState accepts; label is the visible button
// caption. Stays in lockstep with model.FeatureState — adding a
// fourth value would mean adding a row here.
const FEATURE_STATE_OPTIONS = [
  { id: 'active', label: 'Active' },
  { id: 'done', label: 'Done' },
  { id: 'cancelled', label: 'Cancelled' },
];

// Filter strip choices — `all` is the default; the canonical states
// match the segmented control's ids so the same `state` value works
// against either surface.
const FILTERS = [
  { id: 'all', label: 'All' },
  { id: 'active', label: 'Active' },
  { id: 'done', label: 'Done' },
  { id: 'cancelled', label: 'Cancelled' },
];

// stateLabelFor maps a feature state string to the visible label,
// falling back to the raw value so a forward-compat new state still
// renders something. Mirrors stateLabel() for issue states.
function stateLabelFor(state: string): string {
  const opt = FEATURE_STATE_OPTIONS.find((o) => o.id === state);
  return opt ? opt.label : state || 'Active';
}

// Short date for the feature-list rows and detail metadata line.
function shortDate(iso: string): string {
  return new Date(iso).toLocaleDateString();
}

// relTime renders a coarse "time since" for the list-row updated stamp.
function relTime(iso: string): string {
  const ms = Date.now() - new Date(iso).getTime();
  if (ms < 0) return 'in the future';
  const m = Math.floor(ms / 60_000);
  if (m < 1) return 'just now';
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  const d = Math.floor(h / 24);
  if (d < 30) return `${d}d ago`;
  return shortDate(iso);
}

// commentTimestamp formats a feature comment's createdAt for display
// next to its author — matches the issue drawer's comment metadata.
function commentTimestamp(iso: string): string {
  try {
    return new Date(iso).toLocaleString();
  } catch {
    return iso;
  }
}

// TEMP demo data — appended to the real feature list when ?mock=1 is
// present in the URL. Used to cover states (notably `cancelled`) the
// live data doesn't have so the filter strip can be exercised. Remove
// together with the showMock branch before merging.
function mockFeatures(): FeatureSummary[] {
  const ago = (d: number) => new Date(Date.now() - d * 86_400_000).toISOString();
  return [
    {
      slug: 'mock-cancelled-spike',
      title: 'Spike on alternate sync transport (parked)',
      emoji: '🧪',
      state: 'cancelled',
      branchName: '',
      updatedAt: ago(12),
      hiddenOnBoard: false,
    },
    {
      slug: 'mock-active-redesign',
      title: 'Features view redesign + state filter',
      emoji: '🎨',
      state: 'active',
      branchName: '',
      updatedAt: ago(0),
      hiddenOnBoard: false,
    },
    {
      slug: 'mock-hidden-feature',
      title: 'Internal-only debug dashboard',
      emoji: '🛠',
      state: 'active',
      branchName: '',
      updatedAt: ago(3),
      hiddenOnBoard: true,
    },
  ];
}

// FeaturesView is the desktop feature browser: a two-pane mirror of
// the TUI's Features tab. The left pane lists the repo's features; the
// right pane shows the selected feature's description and the issues
// grouped under it. BACI-172 widened the right pane with an emoji
// picker — the per-feature glyph rendered on every kanban card under
// this feature. BACI-177 added a "Show on board" toggle that hides
// every kanban card belonging to the feature when flipped off.
// Everything else still flows through the CLI.
//
// onChangeHidden (BACI-177) is fired by the toggle so the parent
// (App.tsx) can refresh the cached board cards — flipping the toggle
// changes which cards ship over the wire, and the App-owned `cards`
// state would otherwise show stale entries until the 10s poll.
type FeaturesViewProps = {
  activeBoard: string;
  onChangeHidden: () => void;
};

export default function FeaturesView({ activeBoard, onChangeHidden }: FeaturesViewProps) {
  // BACI-203: the selected slug is mirrored to/from the URL via
  // useParams + navigate. Click handlers in the master pane call
  // navigate(featurePath(slug)); the URL is the source of truth so
  // refresh / deep-link / back-forward all work, but the rest of the
  // component still reads from `selected` so the layout / data
  // effects stay unchanged.
  const navigate = useNavigate();
  const { slug: slugParam } = useParams();
  const [features, setFeatures] = useState<FeatureSummary[]>([]);
  const [selected, setSelected] = useState<string | null>(slugParam ?? null); // slug
  const [detail, setDetail] = useState<FeatureDetail | null>(null);
  const [loading, setLoading] = useState(false);
  // BACI-199 filter strip: which state bucket the list is restricted
  // to. BACI-242 made `active` the default so a fresh visit shows the
  // features the user is currently working on rather than every shipped
  // / cancelled row; the per-chip counts still expose the full
  // populations (e.g. `Active 2 · All 9`) so flipping back is one
  // click. If the active bucket is empty on first load we leave it
  // empty rather than falling back — consistent with how other
  // state-based filters in bacio behave, and the chip counts make it
  // obvious why.
  const [filter, setFilter] = useState('active');
  // Free-text search across title + slug. Applied after the state
  // filter so the chip counts reflect the unfiltered state population
  // even when search is narrowing the visible list.
  const [search, setSearch] = useState('');

  const repoSelected = !!activeBoard;

  // TEMP demo toggle — ?mock=1 appends a varied set of fake features
  // (including a cancelled row) so the filter strip can be reviewed
  // against the redesign. Remove with mockFeatures() before merging.
  const showMock =
    typeof window !== 'undefined' &&
    new URLSearchParams(window.location.search).has('mock');

  // BACI-203: sync `selected` from useParams when the URL changes
  // (back/forward, deep-link, manual edit). Outbound writes happen in
  // the FeatureRow click handler via navigate(featurePath(...)). The
  // mount-time read in useState's initialiser already covers the
  // first paint; this covers subsequent URL changes.
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

  // Reload the feature list whenever the selected repo changes.
  useEffect(() => {
    setDetail(null);
    if (!repoSelected) {
      setFeatures([]);
      return;
    }
    api
      .listFeatures(activeBoard)
      .then(setFeatures)
      .catch((err) => reportError(err, { headline: "Couldn't load features" }));
  }, [activeBoard, repoSelected]);

  // Load the chosen feature's detail (description + linked issues).
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

  const allFeatures = useMemo(
    () => (showMock ? [...features, ...mockFeatures()] : features),
    [features, showMock],
  );

  // Per-state counts power the filter-chip badges so a quick glance
  // shows how the population breaks down.
  const counts = useMemo(() => {
    const acc: Record<string, number> = {
      all: allFeatures.length,
      active: 0,
      done: 0,
      cancelled: 0,
    };
    for (const f of allFeatures) {
      const s = f.state || 'active';
      if (acc[s] !== undefined) acc[s] += 1;
    }
    return acc;
  }, [allFeatures]);

  const visible = useMemo(() => {
    const byState =
      filter === 'all'
        ? allFeatures
        : allFeatures.filter((f) => (f.state || 'active') === filter);
    const q = search.trim().toLowerCase();
    if (!q) return byState;
    return byState.filter(
      (f) =>
        (f.title || '').toLowerCase().includes(q) ||
        (f.slug || '').toLowerCase().includes(q),
    );
  }, [allFeatures, filter, search]);

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

  if (!repoSelected) {
    return (
      <div className="mk-features">
        <div className="mk-features-empty">
          Select a repository to view its features.
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
          aria-label="Filter features by state"
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
            aria-label="Filter features"
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
              No features in this repository.
            </div>
          ) : visible.length === 0 ? (
            <div className="mk-features-list-empty">
              No {filter} features.
            </div>
          ) : (
            visible.map((f) => (
              <FeatureRow
                key={f.slug}
                feature={f}
                isActive={selected === f.slug}
                onSelect={() => navigate(featurePath(activeBoard, f.slug))}
              />
            ))
          )}
        </div>
      </aside>

      <div className="mk-features-main">
        {!selected ? (
          <div className="mk-features-empty">
            Pick a feature to see its details.
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

// FeatureRow renders a single feature in the left list — single-height,
// with the emoji + title on the top line and slug + state + relative
// updated time on the meta line. Replaces the two-line slug-then-title
// layout which wrapped awkwardly at narrow widths.
type FeatureRowProps = {
  feature: FeatureSummary;
  isActive: boolean;
  onSelect: () => void;
};

function FeatureRow({ feature, isActive, onSelect }: FeatureRowProps) {
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

// FeatureDetailPane is the right-side drawer body. Extracted so the
// state-toggle / hidden-toggle handlers don't clutter the parent and
// the layout below stays readable.
//
// BACI-236: the pane now hosts two view modes — Overview (the
// historical layout: properties / description / issues / docs /
// comments) and Graph (a dependency-graph render of the same issues
// with directed `blocks` edges). The segmented control above the
// title row flips between them; the toggle is a local useState so
// switching back to Overview is instant and never refetches.
type FeatureDetailPaneProps = {
  activeBoard: string;
  detail: FeatureDetail;
  onChangeHidden: () => void;
  onDetailChange: (detail: FeatureDetail) => void;
  onFeaturesChange: (features: FeatureSummary[]) => void;
};

function FeatureDetailPane({
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
        aria-label="Feature view mode"
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

// FeatureOverviewSections wraps the historical right-pane body —
// properties / description / issues / docs / comments — so the new
// Graph tab can swap it out without dragging this block around.
// Extracted with no behaviour change; every onClick / onSubmit
// handler is identical to the pre-BACI-236 inline form.
type FeatureOverviewSectionsProps = {
  activeBoard: string;
  detail: FeatureDetail;
  onChangeHidden: () => void;
  onDetailChange: (detail: FeatureDetail) => void;
  onFeaturesChange: (features: FeatureSummary[]) => void;
};

function FeatureOverviewSections({
  activeBoard,
  detail,
  onChangeHidden,
  onDetailChange,
  onFeaturesChange,
}: FeatureOverviewSectionsProps) {
  // BACI-357: the property toggles below route their api round-trip through
  // useOptimisticMutation — it owns the reportError-on-failure half of the
  // dance. These toggles stay await-then-apply (no optimistic flip): they
  // apply the server's returned FeatureDetail, then refresh the summary list.
  // The visible optimistic flip lands in Phase 4b's FeaturePropertyToggle.
  const mutate = useOptimisticMutation();
  // Best-effort summary-list refresh after a property change so the left-rail
  // pills re-render; a failure is non-fatal — the next selection refresh picks
  // it up (matches the pre-BACI-357 inner try/catch).
  const refreshFeatures = () => {
    api.listFeatures(activeBoard).then(onFeaturesChange).catch(() => {});
  };
  return (
    <>
      <section className="mk-features-properties">
        <div className="mk-features-prop">
          <label className="mk-features-prop-label">State</label>
          <div
            className="mk-segmented"
            role="group"
            aria-label="Feature state"
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
                    mutate({
                      persist: () =>
                        api.setFeatureState(activeBoard, detail.slug, opt.id),
                      onSuccess: (updated) => {
                        onDetailChange(updated);
                        refreshFeatures();
                      },
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

        <div className="mk-features-prop">
          <label className="mk-features-prop-label">Auto Close</label>
          <div
            className="mk-segmented"
            role="group"
            aria-label="Auto-close this feature when every child is terminal"
          >
            {AUTO_CLOSE_OPTIONS.map((opt) => {
              const autoClose = !detail.stateManual;
              return (
                <button
                  key={String(opt.id)}
                  className={`mk-segmented-btn ${
                    autoClose === opt.id ? 'is-active' : ''
                  }`}
                  aria-pressed={autoClose === opt.id}
                  onClick={() => {
                    if (autoClose === opt.id) return;
                    mutate({
                      persist: () =>
                        api.setFeatureAutoClose(activeBoard, detail.slug, opt.id),
                      onSuccess: (updated) => {
                        onDetailChange(updated);
                        refreshFeatures();
                      },
                      errorHeadline: "Couldn't update auto-close",
                    });
                  }}
                >
                  {opt.label}
                </button>
              );
            })}
          </div>
          <p
            className="mk-features-prop-hint"
            title="When on, the auto-completion sweep can promote this feature to done/cancelled once every child issue is terminal. Turn off for long-lived catch-all features."
          >
            When on, the auto-completion sweep can promote this feature to
            done/cancelled once every child issue is terminal. Turn off for
            long-lived catch-all features.
          </p>
        </div>

        <div className="mk-features-prop">
          <label className="mk-features-prop-label">Collect handoffs</label>
          <div
            className="mk-segmented"
            role="group"
            aria-label="Collect worker handoff comments on this feature"
          >
            {COLLECT_HANDOFFS_OPTIONS.map((opt) => {
              const collect = detail.collectHandoffs;
              return (
                <button
                  key={String(opt.id)}
                  className={`mk-segmented-btn ${
                    collect === opt.id ? 'is-active' : ''
                  }`}
                  aria-pressed={collect === opt.id}
                  onClick={() => {
                    if (collect === opt.id) return;
                    mutate({
                      persist: () =>
                        api.setFeatureCollectHandoffs(activeBoard, detail.slug, opt.id),
                      onSuccess: (updated) => {
                        onDetailChange(updated);
                        refreshFeatures();
                      },
                      errorHeadline: "Couldn't update collect-handoffs",
                    });
                  }}
                >
                  {opt.label}
                </button>
              );
            })}
          </div>
          <p
            className="mk-features-prop-hint"
            title="When on, implement-worker close-outs append a handoff comment to this feature so sibling workers inherit context. Turn off for standing bucket features whose unrelated children make handoffs noise."
          >
            When on, worker close-outs append a handoff comment so siblings
            inherit context. Turn off for standing buckets like bugs /
            maintenance.
          </p>
        </div>

        <div className="mk-features-prop">
          <label className="mk-features-prop-label">Show on board</label>
          <div
            className="mk-segmented"
            role="group"
            aria-label="Show this feature's cards on the board"
          >
            {SHOW_ON_BOARD_OPTIONS.map((opt) => {
              const shown = !detail.hiddenOnBoard;
              return (
                <button
                  key={String(opt.id)}
                  className={`mk-segmented-btn ${
                    shown === opt.id ? 'is-active' : ''
                  }`}
                  aria-pressed={shown === opt.id}
                  onClick={() => {
                    if (shown === opt.id) return;
                    mutate({
                      persist: () =>
                        api.setFeatureHiddenOnBoard(activeBoard, detail.slug, !opt.id),
                      onSuccess: (updated) => {
                        onDetailChange(updated);
                        if (typeof onChangeHidden === 'function') {
                          onChangeHidden();
                        }
                        refreshFeatures();
                      },
                      errorHeadline: "Couldn't update visibility",
                    });
                  }}
                >
                  {opt.label}
                </button>
              );
            })}
          </div>
          <p
            className="mk-features-prop-hint"
            title="When hidden, every kanban card belonging to this feature is dropped from the board on this machine. Lives alongside the TUI feature picker."
          >
            Hide this feature's cards from the kanban board.
          </p>
        </div>

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

// FeatureLinkedDocsSection (BACI-214) lists documents linked to the
// feature via `bacio doc link <file> <feature-slug>`. Each row carries a
// FeatureBranchEditor (BACI-231) is the third Properties row on the
// feature detail pane — the editable integration branch the
// feature ships against. Empty input clears the branch (the
// feature ships straight to main again). Save fires on blur AND on
// Enter; client-side validation via lib/branchName.isValidBranchName
// blocks the round-trip when the input is malformed, so the user
// sees an inline error before the server's identical rejection.
// Parallel to the Show-on-board and State rows above it.
type FeatureBranchEditorProps = {
  activeBoard: string;
  detail: FeatureDetail;
  onDetailChange: (detail: FeatureDetail) => void;
  onFeaturesChange: (features: FeatureSummary[]) => void;
};

function FeatureBranchEditor({
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

// type badge, a `<Link>` into the canonical `/documents/<filename>`
// viewer (the same route the Documents screen uses), and an inline
// `— description` when the link was attached with `--why`. Always-render
// shape mirrors the Issues section's "No issues linked yet." idiom so
// the section is present-as-empty and doesn't pop in when the first doc
// lands. Uses .mk-linked-doc + .mk-attachment-* from app.css /
// desktop.css so no new CSS is needed.
type FeatureLinkedDocsSectionProps = {
  activeBoard: string;
  documents: FeatureLinkedDoc[];
};

function FeatureLinkedDocsSection({ activeBoard, documents }: FeatureLinkedDocsSectionProps) {
  return (
    <section className="mk-features-section">
      <div className="mk-features-label">Documents · {documents.length}</div>
      {documents.length === 0 ? (
        <p className="mk-features-text mk-meta-empty">No documents linked.</p>
      ) : (
        <div className="mk-linked-doc-list">
          {documents.map((d: FeatureLinkedDoc) => (
            <div key={d.filename} className="mk-linked-doc">
              <div className="mk-linked-doc-head">
                <span className="mk-attachment-badge">{d.type || 'doc'}</span>
                <Link
                  to={documentPath(activeBoard, d.filename)}
                  className="mk-attachment-name mk-attachment-link"
                >
                  {d.filename}
                </Link>
                {d.description && (
                  <span className="mk-attachment-why">— {d.description}</span>
                )}
              </div>
            </div>
          ))}
        </div>
      )}
    </section>
  );
}

// FeatureCommentsSection renders the BACI-124 handoff timeline plus an
// inline composer. Reuses the same MarkdownView + CommentComposer used
// by the issue drawer so the markdown rendering rule (`<MarkdownView>`
// is the canonical reader, never `react-markdown` directly) holds.
type FeatureCommentsSectionProps = {
  repoPrefix: string;
  detail: FeatureDetail;
  onChange: (detail: FeatureDetail) => void;
};

function FeatureCommentsSection({ repoPrefix, detail, onChange }: FeatureCommentsSectionProps) {
  const comments = detail.comments ?? [];
  const onSubmit = async (author: string, body: string) => {
    try {
      const updated = await api.addFeatureComment(
        repoPrefix,
        detail.slug,
        author,
        body,
      );
      onChange(updated);
    } catch (err) {
      reportError(err, { headline: "Couldn't add comment" });
    }
  };
  const onDelete = async (uuid: string) => {
    try {
      const updated = await api.deleteFeatureComment(
        repoPrefix,
        detail.slug,
        uuid,
      );
      onChange(updated);
    } catch (err) {
      reportError(err, { headline: "Couldn't delete comment" });
    }
  };
  return (
    <section className="mk-features-section">
      <div className="mk-features-label">Comments · {comments.length}</div>
      {comments.length === 0 ? (
        <p className="mk-features-text mk-meta-empty">No comments yet.</p>
      ) : (
        // BACI-213: reuse the issue drawer's Activity timeline shape so
        // the per-comment delete affordance (hover-revealed mk-tl-delete
        // + window.confirm) matches across surfaces. Feature comments
        // don't carry eval / mode / agent metadata, so no eval pill or
        // footer here — just author + timestamp + body + delete.
        <ul className="mk-timeline">
          {comments.map((c: FeatureCommentDTO) => (
            <li key={c.uuid} className="mk-tl-item">
              <span className="mk-tl-dot" />
              <div className="mk-tl-text">
                <b className="mk-tl-author">{c.author}</b>
                <span>{commentTimestamp(c.createdAt)}</span>
                <MarkdownView className="mk-markdown mk-tl-body">
                  {c.body}
                </MarkdownView>
              </div>
              {c.uuid && (
                <button
                  type="button"
                  className="mk-tl-delete"
                  title="Delete comment"
                  aria-label="Delete comment"
                  onClick={() => {
                    if (window.confirm('Delete this comment? This cannot be undone.')) {
                      onDelete(c.uuid);
                    }
                  }}
                >
                  ✕
                </button>
              )}
            </li>
          ))}
        </ul>
      )}
      <CommentComposer onSubmit={onSubmit} />
    </section>
  );
}
