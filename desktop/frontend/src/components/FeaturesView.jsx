import React, { useMemo, useState, useEffect } from 'react';
import { EyeOff, Search, X } from 'lucide-react';
import { reportError } from '../errors';
import * as api from '../api';
import MarkdownView from '../lib/markdownView';
import CommentComposer from './issue/CommentComposer';
import FeatureEmojiPicker from './FeatureEmojiPicker.jsx';

// BACI-177: per-feature "Show on board" toggle. The id is the value
// the toggle should set on the feature — true = show on board (the
// default), false = hide. Matches the two-button mk-segmented shape
// used elsewhere (theme, show-archived, hide-empty-columns).
const SHOW_ON_BOARD_OPTIONS = [
  { id: true, label: 'Show' },
  { id: false, label: 'Hide' },
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
function stateLabelFor(state) {
  const opt = FEATURE_STATE_OPTIONS.find((o) => o.id === state);
  return opt ? opt.label : state || 'Active';
}

// Short date for the feature-list rows and detail metadata line.
function shortDate(iso) {
  return new Date(iso).toLocaleDateString();
}

// relTime renders a coarse "time since" for the list-row updated stamp.
function relTime(iso) {
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
function commentTimestamp(iso) {
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
function mockFeatures() {
  const ago = (d) => new Date(Date.now() - d * 86_400_000).toISOString();
  return [
    {
      slug: 'mock-cancelled-spike',
      title: 'Spike on alternate sync transport (parked)',
      emoji: '🧪',
      state: 'cancelled',
      updatedAt: ago(12),
      hiddenOnBoard: false,
    },
    {
      slug: 'mock-active-redesign',
      title: 'Features view redesign + state filter',
      emoji: '🎨',
      state: 'active',
      updatedAt: ago(0),
      hiddenOnBoard: false,
    },
    {
      slug: 'mock-hidden-feature',
      title: 'Internal-only debug dashboard',
      emoji: '🛠',
      state: 'active',
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
// (App.jsx) can refresh the cached board cards — flipping the toggle
// changes which cards ship over the wire, and the App-owned `cards`
// state would otherwise show stale entries until the 10s poll.
export default function FeaturesView({ activeBoard, onChangeHidden }) {
  const [features, setFeatures] = useState([]);
  const [selected, setSelected] = useState(null); // slug
  const [detail, setDetail] = useState(null);
  const [loading, setLoading] = useState(false);
  // BACI-199 filter strip: which state bucket the list is restricted
  // to. `all` is the default so the count matches the raw feature row
  // count; counts on each chip show the per-state population.
  const [filter, setFilter] = useState('all');
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

  // Reload the feature list whenever the selected repo changes.
  useEffect(() => {
    setSelected(null);
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
          state: m.state,
          stateManual: false,
          createdAt: m.updatedAt,
          updatedAt: m.updatedAt,
          issues: [],
          comments: [],
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
    const acc = { all: allFeatures.length, active: 0, done: 0, cancelled: 0 };
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
  useEffect(() => {
    if (!repoSelected || visible.length === 0) return;
    const currentInView = visible.some((f) => f.slug === selected);
    if (!currentInView) setSelected(visible[0].slug);
  }, [visible, selected, repoSelected]);

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
                onSelect={() => setSelected(f.slug)}
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
function FeatureRow({ feature, isActive, onSelect }) {
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
function FeatureDetailPane({
  activeBoard,
  detail,
  onChangeHidden,
  onDetailChange,
  onFeaturesChange,
}) {
  return (
    <div className="mk-features-detail">
      <div className="mk-features-title-row">
        <FeatureEmojiPicker
          value={detail.emoji || ''}
          onSelect={async (next) => {
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
                  onClick={async () => {
                    if (current === opt.id) return;
                    try {
                      const updated = await api.setFeatureState(
                        activeBoard,
                        detail.slug,
                        opt.id,
                      );
                      onDetailChange(updated);
                      try {
                        const feats = await api.listFeatures(activeBoard);
                        onFeaturesChange(feats);
                      } catch {
                        // non-fatal — next selection refresh picks it up.
                      }
                    } catch (err) {
                      reportError(err, { headline: "Couldn't update state" });
                    }
                  }}
                >
                  {opt.label}
                </button>
              );
            })}
          </div>
          <p
            className="mk-features-prop-hint"
            title="Pinning a value keeps the auto-completion sweep from changing it. Archive is independent."
          >
            Pinned values aren't touched by the auto-completion sweep.
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
                  onClick={async () => {
                    if (shown === opt.id) return;
                    try {
                      const updated = await api.setFeatureHiddenOnBoard(
                        activeBoard,
                        detail.slug,
                        !opt.id,
                      );
                      onDetailChange(updated);
                      if (typeof onChangeHidden === 'function') {
                        onChangeHidden();
                      }
                      try {
                        const feats = await api.listFeatures(activeBoard);
                        onFeaturesChange(feats);
                      } catch {
                        // non-fatal — next selection refresh picks it up.
                      }
                    } catch (err) {
                      reportError(err, {
                        headline: "Couldn't update visibility",
                      });
                    }
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
      </section>

      <section className="mk-features-section">
        <div className="mk-features-label">Description</div>
        {detail.description ? (
          <MarkdownView className="mk-features-text mk-markdown">
            {detail.description}
          </MarkdownView>
        ) : (
          <p className="mk-features-text mk-meta-empty">No description.</p>
        )}
      </section>

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
            {detail.issues.map((iss) => (
              <li key={iss.key} className="mk-features-issue">
                <span className="mk-card-id">{iss.key}</span>
                <span className="mk-features-issue-title">{iss.title}</span>
                <span className={`mk-pill mk-status-${iss.state}`}>
                  {iss.stateLabel}
                </span>
              </li>
            ))}
          </ul>
        )}
      </section>

      <FeatureCommentsSection
        repoPrefix={activeBoard}
        detail={detail}
        onChange={onDetailChange}
      />
    </div>
  );
}

// FeatureCommentsSection renders the BACI-124 handoff timeline plus an
// inline composer. Reuses the same MarkdownView + CommentComposer used
// by the issue drawer so the markdown rendering rule (`<MarkdownView>`
// is the canonical reader, never `react-markdown` directly) holds.
function FeatureCommentsSection({ repoPrefix, detail, onChange }) {
  const comments = detail.comments ?? [];
  const onSubmit = async (author, body) => {
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
  const onDelete = async (uuid) => {
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
        <ul className="mk-features-comments">
          {comments.map((c) => (
            <li key={c.uuid} className="mk-comment-row">
              <div className="mk-comment-meta">
                <span className="mk-comment-author">{c.author}</span>
                <span className="mk-comment-time">
                  {commentTimestamp(c.createdAt)}
                </span>
                <button
                  type="button"
                  className="mk-link-btn"
                  onClick={() => onDelete(c.uuid)}
                  title="Delete this comment"
                >
                  delete
                </button>
              </div>
              <MarkdownView className="mk-comment-body mk-markdown">
                {c.body}
              </MarkdownView>
            </li>
          ))}
        </ul>
      )}
      <CommentComposer onSubmit={onSubmit} />
    </section>
  );
}
