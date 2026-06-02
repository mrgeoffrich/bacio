import React, { useState, useEffect, useCallback } from 'react';
import SyncRepoCard from '../SyncRepoCard';
import SyncSetupModal from '../SyncSetupModal';
import PhantomLinkModal from '../PhantomLinkModal';
import { reportError } from '../../errors';
import * as api from '../../api';

// BACI-248: Sync Settings section — absorbed body of the old
// standalone SyncView. Now mounts as one entry in the sectioned
// SettingsView instead of being a sibling overlay reached from the
// topbar sync pill. The pill still routes to this section; SettingsView
// preselects `initialSection='sync'` so the muscle memory of "click
// sync pill → see sync state" is preserved.
//
// Same registry + prefs effects, same 10-second poll cadence, same
// SyncRepoCard / SyncSetupModal / PhantomLinkModal children — only the
// outer wrapper changes: the section pane mounts under the Settings
// shell instead of stamping its own .mk-settings-view chrome.

// Matches App.jsx's POLL_INTERVAL_MS — the registry refreshes at the
// same cadence the Board and Agents views poll (10 s). The poll runs
// only while this section is mounted; the SettingsView only mounts
// this when its activeSection === 'sync'.
const POLL_INTERVAL_MS = 10_000;

const BACKGROUND_OPTIONS = [
  { id: false, label: 'Off' },
  { id: true, label: 'On' },
];

// ScopeChip — same shape as the System pane chip. Sync settings are
// uniformly server-side (the sync remote registry is per-machine in
// shape but stored in app_settings / sync_remotes on the shared store),
// so every row here is "Server".
function ScopeChip() {
  return <span className="mk-settings-scope-chip mk-settings-scope-chip--server">Server</span>;
}

export default function SyncSettingsSection() {
  const [registry, setRegistry] = useState(null);
  const [prefs, setPrefs] = useState(null);
  const [loaded, setLoaded] = useState(false);
  const [savingPrefs, setSavingPrefs] = useState(false);
  // BACI-111: prefix of the unsynced project the setup modal is open
  // for, or null when the modal is closed. The lookup against
  // registry.unsyncedProjects happens at render time so the row stays
  // resolvable even as the registry refreshes underneath the modal.
  const [openSetupForPrefix, setOpenSetupForPrefix] = useState(null);
  // BACI-112: phantom row whose Link local… button was clicked, or
  // null when the modal is closed. We carry the row object verbatim
  // (not just the prefix) because the row is a member of one sync
  // repo's projects array; resolving "which sync repo it belonged to"
  // after a registry refresh would be one extra lookup the modal
  // doesn't need to do.
  const [phantomToLink, setPhantomToLink] = useState(null);

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
  // need a poll. The cleanup clears the interval on unmount (i.e. when
  // the user switches away from the Sync section).
  useEffect(() => {
    const id = setInterval(() => refreshRegistry({ silent: true }), POLL_INTERVAL_MS);
    return () => clearInterval(id);
  }, [refreshRegistry]);

  // Bubble submodal-open up to the SettingsView shell so its Escape
  // listener doesn't fight Radix Dialog for the keypress.
  const subModalOpen = openSetupForPrefix !== null || phantomToLink !== null;
  useEffect(() => {
    window.dispatchEvent(new CustomEvent('mk-settings-submodal', { detail: { open: subModalOpen } }));
    return () => {
      if (subModalOpen) {
        window.dispatchEvent(new CustomEvent('mk-settings-submodal', { detail: { open: false } }));
      }
    };
  }, [subModalOpen]);

  // changeBackgroundEnabled flips the toggle optimistically, then
  // confirms with the server response — same shape as App.jsx's
  // changeShowArchived. On failure the modal surfaces and the UI
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
  // Resolve the target project at render time so the setup modal
  // remains anchored to the row even after a poll-driven registry
  // refresh under it.
  const setupTarget = openSetupForPrefix
    ? unsynced.find(u => u.prefix === openSetupForPrefix) ?? null
    : null;

  return (
    <div className="mk-settings-section-pane">
      <header className="mk-settings-section-head">
        <h3 className="mk-settings-section-title">Sync</h3>
        <p className="mk-settings-section-subtitle">
          All-or-nothing background sync across every linked project repo.
          The per-machine registry lives below — set up sync from the CLI
          (<code>bacio sync init</code> / <code>bacio sync clone</code>) or
          per-project from the unsynced list.
        </p>
      </header>

      <section className="mk-settings-row">
        <div className="mk-settings-row-text">
          <div className="mk-settings-label">
            Background sync
            <ScopeChip />
          </div>
          <div className="mk-settings-hint">
            When on, the leader-elected controller mirrors each configured
            project&apos;s issues, features, and documents to its sync repo
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
          <div className="mk-settings-label">
            Sync repositories
            <ScopeChip />
          </div>
          <div className="mk-settings-hint">
            The git repos this machine clones to mirror project data
            across machines. Each card shows the projects it carries —
            linked rows are the projects you work in locally; phantom
            rows are projects this sync repo has but you haven&apos;t linked
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
          <div className="mk-settings-label">
            Unsynced projects
            <ScopeChip />
          </div>
          <div className="mk-settings-hint">
            Project repos this machine tracks that don&apos;t yet have a sync
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
                <button
                  type="button"
                  className="mk-segmented-btn"
                  onClick={() => setOpenSetupForPrefix(p.prefix)}
                >
                  Set up sync…
                </button>
              </li>
            ))}
          </ul>
        )}
      </section>

      <SyncSetupModal
        open={!!setupTarget}
        project={setupTarget}
        syncRepos={syncRepos}
        onClose={() => setOpenSetupForPrefix(null)}
        onDone={() => {
          setOpenSetupForPrefix(null);
          refreshRegistry();
        }}
      />

      {/* BACI-112: phantom-repo link modal. Mounts when a phantom row's
          "Link local…" button has been clicked; on success the row is
          no longer a phantom and we refresh the registry so the card
          re-renders with the linked status. */}
      <PhantomLinkModal
        phantom={phantomToLink}
        onClose={() => setPhantomToLink(null)}
        onSubmitted={() => {
          setPhantomToLink(null);
          refreshRegistry();
        }}
      />
    </div>
  );
}
