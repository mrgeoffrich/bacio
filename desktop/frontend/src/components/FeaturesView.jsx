import React, { useState, useEffect } from 'react';
import * as api from '../api';

// Short date for the feature-list rows and detail metadata line.
function shortDate(iso) {
  return new Date(iso).toLocaleDateString();
}

// FeaturesView is the desktop feature browser: a read-only two-pane mirror of
// the TUI's Features tab. The left pane lists the repo's features; the right
// pane shows the selected feature's description and the issues grouped under
// it. Features are per-repo, so it needs a concrete repo selected in the
// topbar (not "All repositories"). Features are created/edited via the CLI —
// nothing here mutates.
export default function FeaturesView({ activeBoard }) {
  const [features, setFeatures] = useState([]);
  const [selected, setSelected] = useState(null); // slug
  const [detail, setDetail] = useState(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState(null);

  const repoSelected = !!activeBoard && activeBoard !== 'all';

  // Reload the feature list whenever the selected repo changes.
  useEffect(() => {
    setSelected(null);
    setDetail(null);
    setError(null);
    if (!repoSelected) {
      setFeatures([]);
      return;
    }
    api.listFeatures(activeBoard)
      .then(setFeatures)
      .catch(err => setError(err.message));
  }, [activeBoard, repoSelected]);

  // Load the chosen feature's detail (description + linked issues).
  useEffect(() => {
    if (!selected || !repoSelected) return;
    setLoading(true);
    setError(null);
    api.getFeature(activeBoard, selected)
      .then(d => {
        setDetail(d);
        setLoading(false);
      })
      .catch(err => {
        setError(err.message);
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
        {error && <div className="mk-features-error">{error}</div>}
        {!selected ? (
          <div className="mk-features-empty">Pick a feature to see its details.</div>
        ) : loading ? (
          <div className="mk-features-empty">Loading…</div>
        ) : detail ? (
          <div className="mk-features-detail">
            <h2 className="mk-features-title">{detail.title}</h2>
            <div className="mk-features-meta">
              <span className="mk-mono">{detail.slug}</span>
              {' · '}created {shortDate(detail.createdAt)}
              {' · '}updated {shortDate(detail.updatedAt)}
            </div>

            <section className="mk-features-section">
              <div className="mk-features-label">Description</div>
              {detail.description
                ? <p className="mk-features-text">{detail.description}</p>
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
          </div>
        ) : null}
      </div>
    </div>
  );
}
