import React, { useState, useEffect, useCallback } from 'react';
import Icon from './Icon.jsx';
import { reportError } from '../errors';
import { WEB_MODE } from '../env';
import * as api from '../api';

const THEME_OPTIONS = [
  { id: 'system', label: 'System' },
  { id: 'light', label: 'Light' },
  { id: 'dark', label: 'Dark' },
];

const HIDE_EMPTY_OPTIONS = [
  { id: false, label: 'Off' },
  { id: true, label: 'On' },
];

// EMPTY_NEW_TEMPLATE is the seed state for the "Add template" inline form.
const EMPTY_NEW_TEMPLATE = { slug: '', name: '', body: '', states: ['todo'] };

// SettingsView is the desktop Settings screen — a full-screen view (not
// a modal) covering the content area below the topbar. It owns theme
// selection plus the dispatch prompt templates: arbitrary in count
// since BACI-31, with add / rename / delete / restore-defaults
// affordances on top of the per-template body + state-gate editors.
//
// `columns` is the bacio state vocabulary, used to render the
// state-gate toggles. `onTemplatesChanged` is fired after any
// mutation that affects which templates the per-card action menu
// offers, so App.jsx can refresh its promptConfig live without
// waiting for the Settings screen to close.
export default function SettingsView({
  theme,
  onChangeTheme,
  hideEmptyColumns,
  onChangeHideEmptyColumns,
  columns,
  onClose,
  onTemplatesChanged,
}) {
  const [templates, setTemplates] = useState([]);
  const [placeholders, setPlaceholders] = useState([]);
  const [drafts, setDrafts] = useState({});
  const [savingSlug, setSavingSlug] = useState(null);
  const [bacioVer, setBacioVer] = useState('');

  // Add-template inline form. `null` = collapsed; an object = open.
  const [adding, setAdding] = useState(null);
  // Rename overlay state: { slug, newSlug, newName } when open.
  const [renaming, setRenaming] = useState(null);
  // Confirm overlays.
  const [pendingDelete, setPendingDelete] = useState(null);
  const [pendingRestore, setPendingRestore] = useState(false);

  const refreshTemplates = useCallback(async () => {
    const tpls = await api.listPromptTemplates();
    setTemplates(tpls);
    setDrafts(Object.fromEntries(tpls.map(t => [t.slug, t.body])));
    return tpls;
  }, []);

  useEffect(() => {
    let cancelled = false;
    // BACI-47/B+C: the per-template body + state-gate editors are wired
    // in web mode against the BACI-36 REST routes; only the typed
    // CRUD affordances (add / rename / delete / restore-defaults) stay
    // hidden until that REST surface lands.
    Promise.all([refreshTemplates(), api.promptPlaceholders(), api.bacioVersion()])
      .then(([, ph, ver]) => {
        if (cancelled) return;
        setPlaceholders(ph);
        setBacioVer(ver);
      })
      .catch(err => { if (!cancelled) reportError(err, { headline: "Couldn't load templates" }); });
    return () => { cancelled = true; };
  }, [refreshTemplates]);

  // Each mutating path threads through these helpers so the promptConfig
  // up in App.jsx stays in sync without waiting for the Settings screen
  // to close.
  const notifyTemplatesChanged = useCallback(() => {
    if (typeof onTemplatesChanged === 'function') onTemplatesChanged();
  }, [onTemplatesChanged]);

  const saveTemplate = useCallback(async (slug, body) => {
    setSavingSlug(slug);
    try {
      const updated = await api.savePromptTemplate(slug, body);
      setTemplates(prev => prev.map(t => (t.slug === slug ? updated : t)));
      setDrafts(prev => ({ ...prev, [slug]: updated.body }));
      notifyTemplatesChanged();
    } catch (err) {
      reportError(err, { headline: "Couldn't save template body" });
    } finally {
      setSavingSlug(null);
    }
  }, [notifyTemplatesChanged]);

  const saveStates = useCallback(async (slug, states) => {
    setSavingSlug(slug);
    try {
      const updated = await api.savePromptStates(slug, states);
      setTemplates(prev => prev.map(t => (t.slug === slug ? updated : t)));
      notifyTemplatesChanged();
    } catch (err) {
      reportError(err, { headline: "Couldn't save template states" });
    } finally {
      setSavingSlug(null);
    }
  }, [notifyTemplatesChanged]);

  const toggleState = useCallback((t, state) => {
    const on = new Set(t.allowedStates || []);
    if (on.has(state)) on.delete(state);
    else on.add(state);
    const next = columns.map(c => c.state).filter(s => on.has(s));
    saveStates(t.slug, next);
  }, [columns, saveStates]);

  const commitAdd = useCallback(async () => {
    if (!adding) return;
    setSavingSlug(adding.slug || '__new__');
    try {
      await api.addPromptTemplate(adding.slug, adding.name, adding.body, adding.states);
      setAdding(null);
      await refreshTemplates();
      notifyTemplatesChanged();
    } catch (err) {
      reportError(err, { headline: "Couldn't add template" });
    } finally {
      setSavingSlug(null);
    }
  }, [adding, notifyTemplatesChanged, refreshTemplates]);

  const commitRename = useCallback(async () => {
    if (!renaming) return;
    setSavingSlug(renaming.slug);
    try {
      await api.renamePromptTemplate(renaming.slug, renaming.newSlug, renaming.newName);
      setRenaming(null);
      await refreshTemplates();
      notifyTemplatesChanged();
    } catch (err) {
      reportError(err, { headline: "Couldn't rename template" });
    } finally {
      setSavingSlug(null);
    }
  }, [renaming, notifyTemplatesChanged, refreshTemplates]);

  const commitDelete = useCallback(async () => {
    if (!pendingDelete) return;
    setSavingSlug(pendingDelete);
    try {
      await api.deletePromptTemplate(pendingDelete);
      setPendingDelete(null);
      await refreshTemplates();
      notifyTemplatesChanged();
    } catch (err) {
      reportError(err, { headline: "Couldn't delete template" });
    } finally {
      setSavingSlug(null);
    }
  }, [pendingDelete, notifyTemplatesChanged, refreshTemplates]);

  const commitRestore = useCallback(async () => {
    setPendingRestore(false);
    setSavingSlug('__restore__');
    try {
      const refreshed = await api.restoreBuiltinPromptTemplates();
      setTemplates(refreshed);
      setDrafts(Object.fromEntries(refreshed.map(t => [t.slug, t.body])));
      notifyTemplatesChanged();
    } catch (err) {
      reportError(err, { headline: "Couldn't restore built-in templates" });
    } finally {
      setSavingSlug(null);
    }
  }, [notifyTemplatesChanged]);

  // Track which built-in slugs are missing (so the "Restore built-ins"
  // button can be disabled when they're all present).
  const presentSlugs = new Set(templates.map(t => t.slug));
  const BUILTIN_SLUGS = ['plan', 'implement', 'review', 'ship', 'fix_review'];
  const missingBuiltins = BUILTIN_SLUGS.filter(s => !presentSlugs.has(s));

  return (
    <div className="mk-settings-view">
      <header className="mk-settings-bar">
        <h2 className="mk-settings-title">Settings</h2>
        <button className="mk-icbtn" aria-label="Close" onClick={onClose}>
          <Icon name="x" />
        </button>
      </header>

      <div className="mk-settings-body">
        <section className="mk-settings-row">
          <div className="mk-settings-row-text">
            <div className="mk-settings-label">Theme</div>
            <div className="mk-settings-hint">System follows your OS appearance.</div>
          </div>
          <div className="mk-segmented" role="group" aria-label="Theme">
            {THEME_OPTIONS.map(opt => (
              <button
                key={opt.id}
                className={`mk-segmented-btn ${theme === opt.id ? 'is-active' : ''}`}
                aria-pressed={theme === opt.id}
                onClick={() => onChangeTheme(opt.id)}
              >
                {opt.label}
              </button>
            ))}
          </div>
        </section>

        <section className="mk-settings-row">
          <div className="mk-settings-row-text">
            <div className="mk-settings-label">Hide empty board columns</div>
            <div className="mk-settings-hint">Columns with no cards are hidden from the board.</div>
          </div>
          <div className="mk-segmented" role="group" aria-label="Hide empty board columns">
            {HIDE_EMPTY_OPTIONS.map(opt => (
              <button
                key={String(opt.id)}
                className={`mk-segmented-btn ${hideEmptyColumns === opt.id ? 'is-active' : ''}`}
                aria-pressed={hideEmptyColumns === opt.id}
                onClick={() => onChangeHideEmptyColumns(opt.id)}
              >
                {opt.label}
              </button>
            ))}
          </div>
        </section>

        <section className="mk-settings-section">
          <div className="mk-settings-row-text">
            <div className="mk-settings-label">Prompt templates</div>
            <div className="mk-settings-hint">
              {WEB_MODE
                ? "The instruction sent to an agent when you dispatch a job at each template, and the issue states each template's prompt can be launched from."
                : "The instruction sent to an agent when you dispatch a job at each template, and the issue states each template's prompt can be launched from. You can add, rename, and delete templates here — built-ins can be deleted too, and \"Restore built-ins\" re-seeds any that are missing."}
            </div>
          </div>
          {placeholders.length > 0 && (
            <div className="mk-tmpl-tokens">
              Placeholders:{' '}
              {placeholders.map((p, i) => (
                <React.Fragment key={p}>
                  {i > 0 && ' '}
                  <code>{`{{${p}}}`}</code>
                </React.Fragment>
              ))}
            </div>
          )}

          {!WEB_MODE && (
            <div className="mk-tmpl-toolbar">
              <button
                className="mk-segmented-btn"
                onClick={() => setAdding(adding ? null : { ...EMPTY_NEW_TEMPLATE })}
                disabled={savingSlug !== null}
              >
                {adding ? 'Cancel add' : '+ Add template'}
              </button>
              <button
                className="mk-segmented-btn"
                onClick={() => setPendingRestore(true)}
                disabled={savingSlug !== null || missingBuiltins.length === 0}
                title={missingBuiltins.length === 0
                  ? 'Every built-in template is already present'
                  : `Will re-seed: ${missingBuiltins.join(', ')}`}
              >
                Restore built-ins{missingBuiltins.length > 0 ? ` (${missingBuiltins.length})` : ''}
              </button>
            </div>
          )}

          {adding && !WEB_MODE && (
            <div className="mk-tmpl mk-tmpl-adding">
              <div className="mk-tmpl-head">
                <span className="mk-tmpl-label">New template</span>
              </div>
              <div className="mk-tmpl-add-grid">
                <label className="mk-tmpl-add-field">
                  <span>Slug</span>
                  <input
                    className="mk-tmpl-input"
                    value={adding.slug}
                    placeholder="e.g. spike"
                    onChange={e => setAdding({ ...adding, slug: e.target.value })}
                  />
                </label>
                <label className="mk-tmpl-add-field">
                  <span>Name</span>
                  <input
                    className="mk-tmpl-input"
                    value={adding.name}
                    placeholder="e.g. Spike"
                    onChange={e => setAdding({ ...adding, name: e.target.value })}
                  />
                </label>
              </div>
              <textarea
                className="mk-tmpl-input"
                value={adding.body}
                rows={3}
                placeholder="Body — supports {{issue_id}}, {{issue_title}}, {{repo_prefix}}"
                onChange={e => setAdding({ ...adding, body: e.target.value })}
              />
              <div className="mk-tmpl-states">
                <div className="mk-tmpl-states-head">
                  <span className="mk-tmpl-states-label">Valid from</span>
                </div>
                <div className="mk-tmpl-states-chips">
                  {columns.map(col => {
                    const on = new Set(adding.states).has(col.state);
                    return (
                      <button
                        key={col.state}
                        className={`mk-state-chip ${on ? 'is-on' : ''}`}
                        aria-pressed={on}
                        onClick={() => {
                          const next = new Set(adding.states);
                          if (next.has(col.state)) next.delete(col.state);
                          else next.add(col.state);
                          setAdding({
                            ...adding,
                            states: columns.map(c => c.state).filter(s => next.has(s)),
                          });
                        }}
                      >
                        {col.label}
                      </button>
                    );
                  })}
                </div>
              </div>
              <div className="mk-tmpl-toolbar">
                <button
                  className="mk-segmented-btn is-active"
                  onClick={commitAdd}
                  disabled={!adding.slug || !adding.name}
                >
                  Create
                </button>
              </div>
            </div>
          )}

          {templates.map(t => {
            const draft = drafts[t.slug] ?? t.body;
            const dirty = draft !== t.body;
            const busy = savingSlug === t.slug;
            const allowed = new Set(t.allowedStates || []);
            return (
              <div className="mk-tmpl" key={t.slug}>
                <div className="mk-tmpl-head">
                  <span className="mk-tmpl-label">
                    {t.label}
                    {t.isBuiltin && <span className="mk-tmpl-builtin"> · built-in</span>}
                    <span className="mk-tmpl-slug"> · <code>{t.slug}</code></span>
                  </span>
                  <div className="mk-tmpl-actions">
                    {t.isBuiltin && (
                      <button
                        className="mk-tmpl-reset"
                        disabled={busy || (t.isDefault && !dirty)}
                        onClick={() => saveTemplate(t.slug, '')}
                        title="Restore the built-in default body"
                      >
                        Reset body
                      </button>
                    )}
                    {!WEB_MODE && (
                      <button
                        className="mk-tmpl-reset"
                        disabled={busy}
                        onClick={() => setRenaming({ slug: t.slug, newSlug: t.slug, newName: t.label })}
                      >
                        Rename
                      </button>
                    )}
                    {!WEB_MODE && (
                      <button
                        className="mk-tmpl-reset"
                        disabled={busy}
                        onClick={() => setPendingDelete(t.slug)}
                      >
                        Delete
                      </button>
                    )}
                  </div>
                </div>
                <textarea
                  className="mk-tmpl-input"
                  value={draft}
                  rows={3}
                  disabled={busy}
                  spellCheck={false}
                  onChange={e => setDrafts(prev => ({ ...prev, [t.slug]: e.target.value }))}
                  onBlur={() => { if (dirty) saveTemplate(t.slug, draft); }}
                />
                <div className="mk-tmpl-states">
                  <div className="mk-tmpl-states-head">
                    <span className="mk-tmpl-states-label">Valid from</span>
                    {t.isBuiltin && (
                      <button
                        className="mk-tmpl-reset"
                        disabled={busy || t.statesAreDefault}
                        onClick={() => saveStates(t.slug, [])}
                      >
                        Reset gate
                      </button>
                    )}
                  </div>
                  <div className="mk-tmpl-states-chips">
                    {columns.map(col => (
                      <button
                        key={col.state}
                        className={`mk-state-chip ${allowed.has(col.state) ? 'is-on' : ''}`}
                        aria-pressed={allowed.has(col.state)}
                        disabled={busy}
                        onClick={() => toggleState(t, col.state)}
                      >
                        {col.label}
                      </button>
                    ))}
                  </div>
                </div>
              </div>
            );
          })}
          {WEB_MODE && (
            <p className="mk-settings-hint">
              Add, rename, delete, and restore-defaults for templates from the desktop app.
            </p>
          )}
        </section>

        <section className="mk-settings-row">
          <div className="mk-settings-row-text">
            <div className="mk-settings-label">Bacio version</div>
            <div className="mk-settings-hint">
              The version of the bacio binary this desktop app is running.
              Cross-check against the "Bacio version" on each agent in the
              Agents panel to spot agents talking to outdated channels.
            </div>
          </div>
          <div className="mk-settings-value"><code>{bacioVer || '—'}</code></div>
        </section>
      </div>

      {renaming && (
        <div className="mk-modal-backdrop" onClick={() => setRenaming(null)}>
          <div className="mk-modal" onClick={e => e.stopPropagation()}>
            <h3 className="mk-modal-title">Rename template</h3>
            <label className="mk-tmpl-add-field">
              <span>Slug</span>
              <input
                className="mk-tmpl-input"
                value={renaming.newSlug}
                onChange={e => setRenaming({ ...renaming, newSlug: e.target.value })}
              />
            </label>
            <label className="mk-tmpl-add-field">
              <span>Name</span>
              <input
                className="mk-tmpl-input"
                value={renaming.newName}
                onChange={e => setRenaming({ ...renaming, newName: e.target.value })}
              />
            </label>
            <div className="mk-modal-actions">
              <button className="mk-segmented-btn" onClick={() => setRenaming(null)}>Cancel</button>
              <button className="mk-segmented-btn is-active" onClick={commitRename}>Save</button>
            </div>
          </div>
        </div>
      )}

      {pendingDelete && (
        <div className="mk-modal-backdrop" onClick={() => setPendingDelete(null)}>
          <div className="mk-modal" onClick={e => e.stopPropagation()}>
            <h3 className="mk-modal-title">Delete template</h3>
            <p>
              Delete the template <code>{pendingDelete}</code>? Historical dispatches
              that referenced this slug will keep it verbatim but won't have a body to
              render anymore.
            </p>
            <div className="mk-modal-actions">
              <button className="mk-segmented-btn" onClick={() => setPendingDelete(null)}>Cancel</button>
              <button className="mk-segmented-btn is-active" onClick={commitDelete}>Delete</button>
            </div>
          </div>
        </div>
      )}

      {pendingRestore && (
        <div className="mk-modal-backdrop" onClick={() => setPendingRestore(false)}>
          <div className="mk-modal" onClick={e => e.stopPropagation()}>
            <h3 className="mk-modal-title">Restore built-in templates</h3>
            <p>
              Re-seed any missing built-in templates ({missingBuiltins.join(', ') || 'none missing'})
              from the embedded defaults. Existing templates won't be touched.
            </p>
            <div className="mk-modal-actions">
              <button className="mk-segmented-btn" onClick={() => setPendingRestore(false)}>Cancel</button>
              <button className="mk-segmented-btn is-active" onClick={commitRestore}>Restore</button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
