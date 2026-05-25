import React, { useState, useEffect, useCallback } from 'react';
import Icon from './Icon.jsx';
import Tooltip from './Tooltip.jsx';
import SyncRepoCard from './SyncRepoCard.jsx';
import PhantomLinkModal from './PhantomLinkModal.jsx';
import { reportError } from '../errors';
import * as api from '../api';

// Matches App.jsx's POLL_INTERVAL_MS — the registry refreshes at the
// same cadence the Board and Agents views poll (10 s). Inlined per the
// BACI-108 plan: SyncView owns its poll because it's only mounted when
// the operator opens it; lifting the constant to a shared module would
// be over-abstraction for one extra reuse.
const POLL_INTERVAL_MS = 10_000;

const BACKGROUND_OPTIONS = [
  { id: false, label: 'Off' },
  { id: true, label: 'On' },
];

// SyncView is the standalone sync screen (BACI-108) — a full-screen
// view, sibling to SettingsView, opened from the topbar pill. Three
// sections: a background-sync toggle row, a sync-repo registry list
// with one SyncRepoCard per entry, and an unsynced-projects section
// with disabled "Set up sync…" placeholders (BACI-111 lands the
// wizard).
export default function SyncView({ onClose }) {
  const [registry, setRegistry] = useState(null);
  const [prefs, setPrefs] = useState(null);
  const [loaded, setLoaded] = useState(false);
  const [savingPrefs, setSavingPrefs] = useState(false);
  // BACI-112: the phantom row whose Link-local button the operator
  // clicked, or null when no modal is open. SyncRepoCard hoists the
  // click target up here so the modal can mount once at the SyncView
  // level (instead of per-card) and the page-Escape handler can be
  // suspended while it's open.
  const [phantomToLink, setPhantomToLink] = useState(null);

  // Page-level Escape closes the view, matching SettingsView's handler.
  // While a sub-modal is open (PhantomLinkModal — BACI-112) the page
  // handler steps aside so the modal owns its own Escape semantics.
  useEffect(() => {
    const onKey = (e) => {
      if (e.key !== 'Escape') return;
      if (phantomToLink) return; // modal handles its own Escape
      onClose();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose, phantomToLink]);

  const refreshRegistry = useCallback((opts = {}) => {
    api.getSyncRegistry()
      .then(setRegistry)
      .catch(err => {
        if (opts.silent) console.warn('sync registry refresh failed:', err);
        else reportError(err, { headline: "Couldn't load sync registry" });
      });
  }, []);

  // Mount: parallel-load the registry and the background-sync toggle.
  // A failure on either is surfaced once via reportError — the poll
  // loop below logs silently so a flapping fetch doesn't spam the user.
  useEffect(() => {
    let cancelled = false;
    Promise.all([api.getSyncRegistry(), api.getSyncPreferences()])
      .then(([reg, p]) => {
        if (cancelled) return;
        setRegistry(reg);
        setPrefs(p);
        setLoaded(true);
      })
      .catch(err => {
        if (cancelled) return;
        reportError(err, { headline: "Couldn't load sync registry" });
        setLoaded(true);
      });
    return () => { cancelled = true; };
  }, []);

  // Poll the registry on the same cadence the Board uses. Only the
  // registry refreshes — the toggle is operator-driven and doesn't
  // need a poll. The cleanup clears the interval on close / unmount.
  useEffect(() => {
    const id = setInterval(() => refreshRegistry({ silent: true }), POLL_INTERVAL_MS);
    return () => clearInterval(id);
  }, [refreshRegistry]);

  // changeBackgroundEnabled flips the toggle optimistically, then
  // confirms with the server response — same shape as App.jsx's
  // changeHideEmptyColumns. On failure the modal surfaces and the UI
  // reverts to the persisted value.
  const changeBackgroundEnabled = useCallback((next) => {
    if (savingPrefs) return;
    setSavingPrefs(true);
    setPrefs({ backgroundEnabled: next });
    api.setSyncPreferences(next)
      .then(setPrefs)
      .catch(err => {
        reportError(err, { headline: "Couldn't save background sync preference" });
        // Re-pull authoritative state on failure so the toggle snaps back.
        api.getSyncPreferences().then(setPrefs).catch(() => {});
      })
      .finally(() => setSavingPrefs(false));
  }, [savingPrefs]);

  const syncRepos = registry?.syncRepos ?? [];
  const unsynced = registry?.unsyncedProjects ?? [];

  return (
    <div className="mk-settings-view">
      <header className="mk-settings-bar">
        <h2 className="mk-settings-title">Sync</h2>
        <button className="mk-icbtn" aria-label="Close" onClick={onClose}>
          <Icon name="x" />
        </button>
      </header>

      <div className="mk-settings-body">
        <section className="mk-settings-row">
          <div className="mk-settings-row-text">
            <div className="mk-settings-label">Background sync</div>
            <div className="mk-settings-hint">
              When on, the leader-elected controller mirrors each configured
              project's issues, features, and documents to its sync repo
              every few minutes. Turn off to stop the background ticker
              without touching the per-project sync configuration.
            </div>
          </div>
          <div className="mk-segmented" role="group" aria-label="Background sync">
            {BACKGROUND_OPTIONS.map(opt => (
              <button
                key={String(opt.id)}
                className={`mk-segmented-btn ${prefs?.backgroundEnabled === opt.id ? 'is-active' : ''}`}
                aria-pressed={prefs?.backgroundEnabled === opt.id}
                disabled={!loaded || savingPrefs}
                onClick={() => changeBackgroundEnabled(opt.id)}
              >
                {opt.label}
              </button>
            ))}
          </div>
        </section>

        <section className="mk-settings-section">
          <div className="mk-settings-row-text">
            <div className="mk-settings-label">Sync repositories</div>
            <div className="mk-settings-hint">
              The git repos this machine clones to mirror project data
              across machines. Each card shows the projects it carries —
              linked rows are the projects you work in locally; phantom
              rows are projects this sync repo has but you haven't linked
              to a local working tree yet.
            </div>
          </div>
          {!loaded ? (
            <div className="mk-sync-section-empty">Loading…</div>
          ) : syncRepos.length === 0 ? (
            <div className="mk-sync-section-empty">
              No sync repositories configured on this machine. Use{' '}
              <code>bacio sync init</code> or <code>bacio sync clone</code>{' '}
              from the CLI to set one up.
            </div>
          ) : (
            syncRepos.map(entry => (
              <SyncRepoCard
                key={entry.remoteUrl}
                entry={entry}
                onLinkPhantom={setPhantomToLink}
              />
            ))
          )}
        </section>

        <section className="mk-settings-section">
          <div className="mk-settings-row-text">
            <div className="mk-settings-label">Unsynced projects</div>
            <div className="mk-settings-hint">
              Project repos this machine tracks that don't yet have a sync
              configuration. Set up sync to attach one to an existing sync
              repo (or initialise a new one).
            </div>
          </div>
          {!loaded ? (
            <div className="mk-sync-section-empty">Loading…</div>
          ) : unsynced.length === 0 ? (
            <div className="mk-sync-section-empty">
              Every tracked project is already attached to a sync repo.
            </div>
          ) : (
            <ul className="mk-sync-project-list">
              {unsynced.map(p => (
                <li key={p.prefix} className="mk-sync-project-row">
                  <span className="mk-sync-project-prefix">{p.prefix}</span>
                  <span className="mk-sync-project-name">{p.name}</span>
                  <code className="mk-sync-project-path">{p.path}</code>
                  <Tooltip label="Coming in BACI-111">
                    <span>
                      <button
                        type="button"
                        className="mk-segmented-btn"
                        disabled
                      >
                        Set up sync…
                      </button>
                    </span>
                  </Tooltip>
                </li>
              ))}
            </ul>
          )}
        </section>
      </div>

      {phantomToLink && (
        <PhantomLinkModal
          phantom={phantomToLink}
          onClose={() => setPhantomToLink(null)}
          onSubmitted={() => {
            setPhantomToLink(null);
            refreshRegistry();
          }}
        />
      )}
    </div>
  );
}
