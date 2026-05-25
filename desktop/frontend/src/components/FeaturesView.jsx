import React, { useState, useEffect } from 'react';
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

// Short date for the feature-list rows and detail metadata line.
function shortDate(iso) {
  return new Date(iso).toLocaleDateString();
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

  const repoSelected = !!activeBoard;

  // Reload the feature list whenever the selected repo changes.
  useEffect(() => {
    setSelected(null);
    setDetail(null);
    if (!repoSelected) {
      setFeatures([]);
      return;
    }
    api.listFeatures(activeBoard)
      .then(setFeatures)
      .catch(err => reportError(err, { headline: "Couldn't load features" }));
  }, [activeBoard, repoSelected]);

  // Load the chosen feature's detail (description + linked issues).
  useEffect(() => {
    if (!selected || !repoSelected) return;
    setLoading(true);
    api.getFeature(activeBoard, selected)
      .then(d => {
        setDetail(d);
        setLoading(false);
      })
      .catch(err => {
        reportError(err, { headline: "Couldn't load feature" });
        setLoading(false);
      });
  }, [selected, activeBoard, repoSelected]);

  if (!repoSelected) {
    return (
      <div className="mk-features">
        <div className="mk-features-empty">Select a repository to view its features.</div>
      </div>
    );
  }

  return (
    <div className="mk-features">
      <aside className="mk-features-list">
        {features.length === 0 ? (
          <div className="mk-features-list-empty">No features in this repository.</div>
        ) : (
          features.map(f => (
            <button
              key={f.slug}
              className={`mk-features-item ${selected === f.slug ? 'is-active' : ''}`}
              onClick={() => setSelected(f.slug)}
            >
              <span className="mk-features-item-slug">{f.slug}</span>
              <span className="mk-features-item-title">{f.title}</span>
            </button>
          ))
        )}
      </aside>

      <div className="mk-features-main">
        {!selected ? (
          <div className="mk-features-empty">Pick a feature to see its details.</div>
        ) : loading ? (
          <div className="mk-features-empty">Loading…</div>
        ) : detail ? (
          <div className="mk-features-detail">
            <div className="mk-features-title-row">
              {/* BACI-172: emoji picker sits beside the title.
                  Clicking opens a Radix DropdownMenu with the curated
                  grid + a free-form input + a Clear button. */}
              <FeatureEmojiPicker
                value={detail.emoji || ''}
                onSelect={async (next) => {
                  try {
                    const updated = await api.setFeatureEmoji(activeBoard, detail.slug, next);
                    setDetail(updated);
                  } catch (err) {
                    reportError(err, { headline: "Couldn't update emoji" });
                  }
                }}
              />
              <h2 className="mk-features-title">{detail.title}</h2>
            </div>
            <div className="mk-features-meta">
              <span className="mk-mono">{detail.slug}</span>
              {' · '}created {shortDate(detail.createdAt)}
              {' · '}updated {shortDate(detail.updatedAt)}
            </div>

            <section className="mk-features-section">
              <div className="mk-features-toggle-row">
                <div className="mk-features-label">Show on board</div>
                <div
                  className="mk-segmented"
                  role="group"
                  aria-label="Show this feature's cards on the board"
                >
                  {SHOW_ON_BOARD_OPTIONS.map(opt => {
                    // The toggle reads "are cards visible?" — i.e. the
                    // inverse of detail.hiddenOnBoard. The id we set
                    // is the same shape: true = show. The store
                    // writes hidden=!shown.
                    const shown = !detail.hiddenOnBoard;
                    return (
                      <button
                        key={String(opt.id)}
                        className={`mk-segmented-btn ${shown === opt.id ? 'is-active' : ''}`}
                        aria-pressed={shown === opt.id}
                        onClick={async () => {
                          if (shown === opt.id) return; // already at this value
                          try {
                            const updated = await api.setFeatureHiddenOnBoard(
                              activeBoard,
                              detail.slug,
                              !opt.id, // hidden = !shown
                            );
                            setDetail(updated);
                            // Refresh the cached board cards in the
                            // parent so the change is visible without
                            // waiting for the 10s poll.
                            if (typeof onChangeHidden === 'function') {
                              onChangeHidden();
                            }
                            // Also refresh the list so the row's flag
                            // stays in lockstep with the detail pane.
                            try {
                              const feats = await api.listFeatures(activeBoard);
                              setFeatures(feats);
                            } catch {
                              // non-fatal — the next selection refresh
                              // will pick up the new state.
                            }
                          } catch (err) {
                            reportError(err, { headline: "Couldn't update visibility" });
                          }
                        }}
                      >
                        {opt.label}
                      </button>
                    );
                  })}
                </div>
              </div>
              <div className="mk-features-hint">
                When hidden, every kanban card belonging to this
                feature is dropped from the board on this machine.
                Reverse it any time. Lives alongside the TUI feature
                picker — flipping it here is visible on the TUI on the
                next reload.
              </div>
            </section>

            <section className="mk-features-section">
              <div className="mk-features-label">Description</div>
              {detail.description
                ? <MarkdownView className="mk-features-text mk-markdown">{detail.description}</MarkdownView>
                : <p className="mk-features-text mk-meta-empty">No description.</p>}
            </section>

            <section className="mk-features-section">
              <div className="mk-features-label">Issues · {detail.issues.length}</div>
              {detail.issues.length === 0 ? (
                <p className="mk-features-text mk-meta-empty">No issues linked yet.</p>
              ) : (
                <ul className="mk-features-issues">
                  {detail.issues.map(iss => (
                    <li key={iss.key} className="mk-features-issue">
                      <span className="mk-card-id">{iss.key}</span>
                      <span className={`mk-pill mk-status-${iss.state}`}>{iss.stateLabel}</span>
                      <span className="mk-features-issue-title">{iss.title}</span>
                    </li>
                  ))}
                </ul>
              )}
            </section>

            <FeatureCommentsSection
              repoPrefix={activeBoard}
              detail={detail}
              onChange={setDetail}
            />
          </div>
        ) : null}
      </div>
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
      const updated = await api.addFeatureComment(repoPrefix, detail.slug, author, body);
      onChange(updated);
    } catch (err) {
      reportError(err, { headline: "Couldn't add comment" });
    }
  };
  const onDelete = async (uuid) => {
    try {
      const updated = await api.deleteFeatureComment(repoPrefix, detail.slug, uuid);
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
          {comments.map(c => (
            <li key={c.uuid} className="mk-comment-row">
              <div className="mk-comment-meta">
                <span className="mk-comment-author">{c.author}</span>
                <span className="mk-comment-time">{commentTimestamp(c.createdAt)}</span>
                <button
                  type="button"
                  className="mk-link-btn"
                  onClick={() => onDelete(c.uuid)}
                  title="Delete this comment"
                >
                  delete
                </button>
              </div>
              <MarkdownView className="mk-comment-body mk-markdown">{c.body}</MarkdownView>
            </li>
          ))}
        </ul>
      )}
      <CommentComposer onSubmit={onSubmit} />
    </section>
  );
}
