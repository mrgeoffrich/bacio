import React, { useState, useEffect, useCallback } from 'react';
import Modal from './Modal.jsx';
import Icon from './Icon.jsx';
import Tooltip from './Tooltip.jsx';
import { reportError } from '../errors';
import * as api from '../api';

const THEME_OPTIONS = [
  { id: 'system', label: 'System' },
  { id: 'light', label: 'Light' },
  { id: 'dark', label: 'Dark' },
];

// BACI-68: the display.show_archived toggle uses the same two-button
// shape as the other boolean preferences. "Off" hides archived rows
// from default lists / boards (the default); "On" surfaces them
// rendered visibly muted.
const SHOW_ARCHIVED_OPTIONS = [
  { id: false, label: 'Off' },
  { id: true, label: 'On' },
];

// EMPTY_NEW_TEMPLATE is the seed state for the "Add template" inline form.
// actionLabel (BACI-67) is optional — an empty string is the "no override,
// derive from the gerund name" sentinel the backend honours.
const EMPTY_NEW_TEMPLATE = { slug: '', name: '', body: '', states: ['todo'], actionLabel: '' };

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
  showArchived,
  onChangeShowArchived,
  archiveAutoEnabled,
  archiveRetentionDays,
  onChangeArchivePreferences,
  columns,
  onClose,
  onTemplatesChanged,
  repoPrefix,
}) {
  // BACI-162: local draft for the retention-days input. We commit to
  // the App-owned state via onChangeArchivePreferences on blur (rather
  // than every keystroke) so a half-typed value doesn't round-trip
  // through the API on every digit. Kept in lockstep with the App
  // state by syncing on prop change.
  const [retentionDraft, setRetentionDraft] = useState(String(archiveRetentionDays));
  useEffect(() => {
    setRetentionDraft(String(archiveRetentionDays));
  }, [archiveRetentionDays]);
  const [templates, setTemplates] = useState([]);
  const [placeholders, setPlaceholders] = useState([]);
  const [drafts, setDrafts] = useState({});
  const [savingSlug, setSavingSlug] = useState(null);
  const [bacioVer, setBacioVer] = useState('');

  // BACI-235: per-repo default-feature setting. defaultFeatureSlug is
  // the empty-string sentinel when unset; featureChoices is the list
  // backing the dropdown (refreshed once on view mount + on repo
  // change). Local-only — the App owns repoPrefix, this view owns the
  // setting state because it's a per-view affordance.
  const [defaultFeatureSlug, setDefaultFeatureSlug] = useState('');
  const [featureChoices, setFeatureChoices] = useState([]);
  const [savingDefaultFeature, setSavingDefaultFeature] = useState(false);

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

  // Page-level Escape closes the Settings view. Skipped when any
  // sub-modal is open — Radix Dialog catches Escape first via a
  // capture-phase listener and dismisses just the sub-modal, leaving
  // Settings open. (When no sub-modal is open, no Radix listener is
  // mounted, so this window-level listener fires and closes the page.)
  useEffect(() => {
    const onKey = (e) => {
      if (e.key !== 'Escape') return;
      if (adding || renaming || pendingDelete || pendingRestore) return;
      onClose();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [adding, renaming, pendingDelete, pendingRestore, onClose]);

  useEffect(() => {
    let cancelled = false;
    // BACI-50 closed the web-mode CRUD gap — every affordance below is
    // available in both desktop and web.
    Promise.all([refreshTemplates(), api.promptPlaceholders(), api.bacioVersion()])
      .then(([, ph, ver]) => {
        if (cancelled) return;
        setPlaceholders(ph);
        setBacioVer(ver);
      })
      .catch(err => { if (!cancelled) reportError(err, { headline: "Couldn't load templates" }); });
    return () => { cancelled = true; };
  }, [refreshTemplates]);

  // BACI-235: load the current per-repo default-feature setting + the
  // list of features for the dropdown. Re-runs when the active repo
  // changes (the App-owned activeBoard).
  useEffect(() => {
    if (!repoPrefix) {
      setDefaultFeatureSlug('');
      setFeatureChoices([]);
      return;
    }
    let cancelled = false;
    Promise.all([api.getDefaultFeature(repoPrefix), api.listFeatures(repoPrefix)])
      .then(([def, feats]) => {
        if (cancelled) return;
        setDefaultFeatureSlug(def?.slug ?? '');
        setFeatureChoices(feats || []);
      })
      .catch(err => { if (!cancelled) reportError(err, { headline: "Couldn't load default feature" }); });
    return () => { cancelled = true; };
  }, [repoPrefix]);

  // BACI-235: persist the per-repo default feature. Empty string is the
  // "clear" sentinel — routed through the explicit clearDefaultFeature
  // path so the audit log records it as a clear, not as a "set to no
  // feature".
  const changeDefaultFeature = useCallback(async (nextSlug) => {
    if (!repoPrefix) return;
    setSavingDefaultFeature(true);
    try {
      if (nextSlug === '') {
        await api.clearDefaultFeature(repoPrefix);
        setDefaultFeatureSlug('');
      } else {
        const out = await api.setDefaultFeature(repoPrefix, nextSlug);
        setDefaultFeatureSlug(out?.slug ?? '');
      }
    } catch (err) {
      reportError(err, { headline: "Couldn't save default feature" });
    } finally {
      setSavingDefaultFeature(false);
    }
  }, [repoPrefix]);

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

  // BACI-51: persist a template's per-(repo, slug) in-flight cap. The
  // matcher reads this column on every tick to decide whether to bind
  // another queued dispatch. 0 = unlimited; built-in ship seeds to 1
  // so merging serialises. Validation (>=0) lives in the store.
  const saveConcurrency = useCallback(async (slug, limit) => {
    setSavingSlug(slug);
    try {
      const updated = await api.savePromptConcurrency(slug, limit);
      setTemplates(prev => prev.map(t => (t.slug === slug ? updated : t)));
      notifyTemplatesChanged();
    } catch (err) {
      reportError(err, { headline: "Couldn't save concurrency limit" });
    } finally {
      setSavingSlug(null);
    }
  }, [notifyTemplatesChanged]);

  // BACI-67: persist the imperative action_label override — the verb the
  // dispatch action menus render on the kanban-card + issue-workspace
  // dropdowns. An empty string clears the override; the UI then derives
  // a default from the gerund display name (the validator on the Go side
  // accepts the empty value as the "no override" sentinel).
  const saveActionLabel = useCallback(async (slug, actionLabel) => {
    setSavingSlug(slug);
    try {
      const updated = await api.savePromptActionLabel(slug, actionLabel);
      setTemplates(prev => prev.map(t => (t.slug === slug ? updated : t)));
      notifyTemplatesChanged();
    } catch (err) {
      reportError(err, { headline: "Couldn't save action label" });
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
      await api.addPromptTemplate(adding.slug, adding.name, adding.body, adding.states, adding.actionLabel || '');
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

  // The Restore button is always available — the backend's
  // RestoreBuiltinPromptTemplates is idempotent (only inserts slugs that
  // aren't already present). Computing "missing" client-side meant
  // hardcoding the canonical built-in slug list, which silently went
  // stale every time a new built-in was added.

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

        {repoPrefix && (
          <section className="mk-settings-row">
            <div className="mk-settings-row-text">
              <div className="mk-settings-label">Default feature ({repoPrefix})</div>
              <div className="mk-settings-hint">
                BACI-235: when set, issues created without an explicit feature auto-apply to this feature. Per-repo (every other setting on this page is global). An explicit feature on the new-issue form always wins; deleting the referenced feature clears the setting automatically.
              </div>
            </div>
            <select
              className="mk-tmpl-input"
              value={defaultFeatureSlug}
              disabled={savingDefaultFeature}
              aria-label="Default feature for new issues"
              onChange={e => changeDefaultFeature(e.target.value)}
            >
              <option value="">(none)</option>
              {featureChoices.map(f => (
                <option key={f.slug} value={f.slug}>
                  {f.emoji ? `${f.emoji} ` : ''}{f.title}
                </option>
              ))}
            </select>
          </section>
        )}

        <section className="mk-settings-row">
          <div className="mk-settings-row-text">
            <div className="mk-settings-label">Show archived items</div>
            <div className="mk-settings-hint">
              When on, archived issues, documents and features surface in the board, doc list and feature list (rendered visibly muted). When off (the default), they&apos;re hidden. The hourly auto-sweep archives completed work older than the retention window below; you can also archive any item manually.
            </div>
          </div>
          <div className="mk-segmented" role="group" aria-label="Show archived items">
            {SHOW_ARCHIVED_OPTIONS.map(opt => (
              <button
                key={String(opt.id)}
                className={`mk-segmented-btn ${showArchived === opt.id ? 'is-active' : ''}`}
                aria-pressed={showArchived === opt.id}
                onClick={() => onChangeShowArchived(opt.id)}
              >
                {opt.label}
              </button>
            ))}
          </div>
        </section>

        <section className="mk-settings-row">
          <div className="mk-settings-row-text">
            <div className="mk-settings-label">Auto-archive completed issues</div>
            <div className="mk-settings-hint">
              When on (the default), the hourly sweep hides done / cancelled issues whose terminal-state timestamp is older than the retention window. You can always unarchive items from the board. The feature + linked-doc cascade still runs when this is off, so manually archiving an issue still tidies up its parents.
            </div>
          </div>
          <div className="mk-segmented" role="group" aria-label="Auto-archive completed issues">
            {SHOW_ARCHIVED_OPTIONS.map(opt => (
              <button
                key={String(opt.id)}
                className={`mk-segmented-btn ${archiveAutoEnabled === opt.id ? 'is-active' : ''}`}
                aria-pressed={archiveAutoEnabled === opt.id}
                onClick={() => onChangeArchivePreferences(opt.id, archiveRetentionDays)}
              >
                {opt.label}
              </button>
            ))}
          </div>
        </section>

        <section className="mk-settings-row">
          <div className="mk-settings-row-text">
            <div className="mk-settings-label">Retention window (days)</div>
            <div className="mk-settings-hint">
              How long a done / cancelled issue stays visible before the hourly auto-sweep archives it. Measured against the state-transition timestamp, so editing a closed issue&apos;s tags or description doesn&apos;t reset the clock. 1–3650; default 7.
            </div>
          </div>
          <input
            type="number"
            min={1}
            max={3650}
            step={1}
            className="mk-tmpl-input"
            style={{ width: '6rem' }}
            value={retentionDraft}
            disabled={!archiveAutoEnabled}
            aria-label="Retention window in days"
            onChange={e => setRetentionDraft(e.target.value)}
            onBlur={() => {
              const n = parseInt(retentionDraft, 10);
              if (Number.isNaN(n) || n < 1 || n > 3650) {
                // Snap back to the last good value so a half-typed
                // entry doesn't fire a 400 on every blur.
                setRetentionDraft(String(archiveRetentionDays));
                return;
              }
              if (n !== archiveRetentionDays) {
                onChangeArchivePreferences(archiveAutoEnabled, n);
              }
            }}
          />
        </section>

        <section className="mk-settings-section">
          <div className="mk-settings-row-text">
            <div className="mk-settings-label">Prompt templates</div>
            <div className="mk-settings-hint">
              The instruction sent to an agent when you dispatch a job at each template, and the issue states each template&apos;s prompt can be launched from. You can add, rename, and delete templates here — built-ins can be deleted too, and &quot;Restore built-ins&quot; re-seeds any that are missing.
            </div>
            <div className="mk-settings-hint">
              A template body becomes a per-mode subagent&apos;s system prompt. After editing a body, run <code>bacio install-agents</code> in the repo to regenerate the <code>.claude/agents/</code> files — until then, dispatched workers still use the previous body.
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

          <div className="mk-tmpl-toolbar">
            <button
              className="mk-segmented-btn"
              onClick={() => setAdding(adding ? null : { ...EMPTY_NEW_TEMPLATE })}
              disabled={savingSlug !== null}
            >
              {adding ? 'Cancel add' : '+ Add template'}
            </button>
            <Tooltip label="Re-seed any built-in template that's been deleted">
              <button
                className="mk-segmented-btn"
                onClick={() => setPendingRestore(true)}
                disabled={savingSlug !== null}
              >
                Restore built-ins
              </button>
            </Tooltip>
          </div>

          {adding && (
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
                  <span>Name (gerund — used by the activity pill)</span>
                  <input
                    className="mk-tmpl-input"
                    value={adding.name}
                    placeholder="e.g. Spiking"
                    onChange={e => setAdding({ ...adding, name: e.target.value })}
                  />
                </label>
              </div>
              {/*
                BACI-67: action_label is the imperative override used by
                the dispatch dropdown buttons (kanban card + issue
                workspace shelf). When empty, the UI derives one from
                Name via the gerund→imperative rule. Optional.
              */}
              <label className="mk-tmpl-add-field">
                <span>Action label (optional · button text on dispatch dropdowns; empty derives from name)</span>
                <input
                  className="mk-tmpl-input"
                  value={adding.actionLabel}
                  placeholder="e.g. Spike"
                  onChange={e => setAdding({ ...adding, actionLabel: e.target.value })}
                />
              </label>
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
                      <Tooltip label="Restore the built-in default body">
                        <button
                          className="mk-tmpl-reset"
                          disabled={busy || (t.isDefault && !dirty)}
                          onClick={() => saveTemplate(t.slug, '')}
                        >
                          Reset body
                        </button>
                      </Tooltip>
                    )}
                    <button
                      className="mk-tmpl-reset"
                      disabled={busy}
                      onClick={() => setRenaming({ slug: t.slug, newSlug: t.slug, newName: t.label })}
                    >
                      Rename
                    </button>
                    <button
                      className="mk-tmpl-reset"
                      disabled={busy}
                      onClick={() => setPendingDelete(t.slug)}
                    >
                      Delete
                    </button>
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
                {/*
                  BACI-67: per-template action_label override. The
                  imperative form goes here; the gerund stays in
                  Name (visible above) and feeds the activity-pill
                  derivation on taken cards. Empty value clears the
                  override and the UI derives from Name.
                */}
                <div className="mk-tmpl-states">
                  <div className="mk-tmpl-states-head">
                    <span className="mk-tmpl-states-label">
                      Action label
                      <span className="mk-tmpl-states-hint"> · imperative; rendered on dispatch dropdowns. Empty = derive from name.</span>
                    </span>
                    {t.isBuiltin && (
                      <Tooltip label={t.defaultActionLabel ? `Reset to the built-in default ("${t.defaultActionLabel}")` : 'Clear the override'}>
                        <button
                          className="mk-tmpl-reset"
                          disabled={busy || t.actionLabelIsDefault}
                          onClick={() => saveActionLabel(t.slug, t.defaultActionLabel)}
                        >
                          Reset action label
                        </button>
                      </Tooltip>
                    )}
                  </div>
                  <input
                    type="text"
                    className="mk-tmpl-input"
                    defaultValue={t.actionLabel}
                    key={`${t.slug}:action:${t.actionLabel}`}
                    placeholder="e.g. Plan, Design, Implement (empty derives from name)"
                    disabled={busy}
                    onBlur={(e) => {
                      const v = e.target.value;
                      if (v !== t.actionLabel) saveActionLabel(t.slug, v);
                    }}
                  />
                </div>
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
                <div className="mk-tmpl-states">
                  <div className="mk-tmpl-states-head">
                    <span className="mk-tmpl-states-label">
                      Concurrency limit
                      <span className="mk-tmpl-states-hint"> · 0 = unlimited</span>
                    </span>
                    {t.isBuiltin && (
                      <Tooltip label={`Reset to the built-in default (${t.defaultConcurrencyLimit})`}>
                        <button
                          className="mk-tmpl-reset"
                          disabled={busy || t.concurrencyIsDefault}
                          onClick={() => saveConcurrency(t.slug, t.defaultConcurrencyLimit)}
                        >
                          Reset limit
                        </button>
                      </Tooltip>
                    )}
                  </div>
                  <input
                    type="number"
                    min={0}
                    step={1}
                    className="mk-tmpl-input mk-tmpl-concurrency"
                    defaultValue={t.concurrencyLimit}
                    key={`${t.slug}:${t.concurrencyLimit}`}
                    disabled={busy}
                    onBlur={(e) => {
                      const n = Math.max(0, parseInt(e.target.value, 10) || 0);
                      if (n !== t.concurrencyLimit) saveConcurrency(t.slug, n);
                    }}
                  />
                </div>
              </div>
            );
          })}
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

      <Modal open={!!renaming} onClose={() => setRenaming(null)} title="Rename template">
        {renaming && (
          <>
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
              <Modal.Close asChild>
                <button className="mk-segmented-btn">Cancel</button>
              </Modal.Close>
              <button className="mk-segmented-btn is-active" onClick={commitRename}>Save</button>
            </div>
          </>
        )}
      </Modal>

      <Modal open={!!pendingDelete} onClose={() => setPendingDelete(null)} title="Delete template">
        <p>
          Delete the template <code>{pendingDelete}</code>? Historical dispatches
          that referenced this slug will keep it verbatim but won't have a body to
          render anymore.
        </p>
        <div className="mk-modal-actions">
          <Modal.Close asChild>
            <button className="mk-segmented-btn">Cancel</button>
          </Modal.Close>
          <button className="mk-segmented-btn is-active" onClick={commitDelete}>Delete</button>
        </div>
      </Modal>

      <Modal open={pendingRestore} onClose={() => setPendingRestore(false)} title="Restore built-in templates">
        <p>
          Re-seed any built-in template that's been deleted, from the
          embedded defaults. Existing templates won't be touched (idempotent).
        </p>
        <div className="mk-modal-actions">
          <Modal.Close asChild>
            <button className="mk-segmented-btn">Cancel</button>
          </Modal.Close>
          <button className="mk-segmented-btn is-active" onClick={commitRestore}>Restore</button>
        </div>
      </Modal>
    </div>
  );
}
