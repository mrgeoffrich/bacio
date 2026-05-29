import React, { useState, useEffect, useCallback } from 'react';
import Modal from '../Modal.jsx';
import Tooltip from '../Tooltip.jsx';
import { reportError } from '../../errors';
import * as api from '../../api';

// BACI-248: System Settings section — the global, app-wide preferences
// pane carved out of the old single-scroll SettingsView body. Mounted
// inside the new sectioned SettingsView when the section rail's
// "System" entry is active. Owns the same five sub-bands the old page
// did (Appearance / Display / Audio / Auto-archive / Prompt-templates /
// About) plus the per-row Client / Server scope chip the ticket calls
// for so the user can see at a glance which preferences live on this
// browser vs the shared store.
//
// Props mirror what App.jsx already passes — no behaviour change
// beyond the layout reshape.

const THEME_OPTIONS = [
  { id: 'system', label: 'System' },
  { id: 'light', label: 'Light' },
  { id: 'dark', label: 'Dark' },
];

// BACI-68: the display.show_archived toggle uses the same two-button
// shape as the other boolean preferences. "Off" hides archived rows
// from default lists / boards (the default); "On" surfaces them
// rendered visibly muted.
const ON_OFF_OPTIONS = [
  { id: false, label: 'Off' },
  { id: true, label: 'On' },
];

// EMPTY_NEW_TEMPLATE is the seed state for the "Add template" inline form.
// actionLabel (BACI-67) is optional — an empty string is the "no override,
// derive from the gerund name" sentinel the backend honours.
const EMPTY_NEW_TEMPLATE = { slug: '', name: '', body: '', actionLabel: '' };

// ScopeChip renders the small "Client" / "Server" pill rendered next
// to the row label inside System. Client = stored in browser
// localStorage (theme); Server = stored in app_settings on the shared
// SQLite store. Hint text and behaviour come from the surrounding row;
// this chip is purely a visual delineator.
function ScopeChip({ kind }) {
  const label = kind === 'client' ? 'Client' : 'Server';
  return (
    <span className={`mk-settings-scope-chip mk-settings-scope-chip--${kind}`}>
      {label}
    </span>
  );
}

export default function SystemSettingsSection({
  theme,
  onChangeTheme,
  showArchived,
  onChangeShowArchived,
  archiveAutoEnabled,
  archiveRetentionDays,
  onChangeArchivePreferences,
  audioEnabled,
  onChangeAudioEnabled,
  onTemplatesChanged,
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

  const commitAdd = useCallback(async () => {
    if (!adding) return;
    setSavingSlug(adding.slug || '__new__');
    try {
      await api.addPromptTemplate(adding.slug, adding.name, adding.body, adding.actionLabel || '');
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

  // Any submodal open (rename / delete / restore confirm) suppresses
  // the page-level Escape close in SettingsView — Radix Dialog catches
  // Escape first and dismisses just the modal, leaving the section
  // pane mounted.
  const subModalOpen = !!(adding || renaming || pendingDelete || pendingRestore);
  useEffect(() => {
    // BACI-248: bubble the submodal-open signal up so the SettingsView
    // shell can suppress its own Escape-to-close listener while a
    // child modal is mounted. We hang it off the parent via a custom
    // event because the props that arrive here are App-owned; a callback
    // prop is the cleaner door but adds another seam — the event keeps
    // the parent shell ignorant of which section is mounted.
    window.dispatchEvent(new CustomEvent('mk-settings-submodal', { detail: { open: subModalOpen } }));
    return () => {
      if (subModalOpen) {
        window.dispatchEvent(new CustomEvent('mk-settings-submodal', { detail: { open: false } }));
      }
    };
  }, [subModalOpen]);

  // The Restore button is always available — the backend's
  // RestoreBuiltinPromptTemplates is idempotent (only inserts slugs that
  // aren't already present). Computing "missing" client-side meant
  // hardcoding the canonical built-in slug list, which silently went
  // stale every time a new built-in was added.

  return (
    <div className="mk-settings-section-pane">
      <header className="mk-settings-section-head">
        <h3 className="mk-settings-section-title">System</h3>
        <p className="mk-settings-section-subtitle">
          App-wide preferences. Client-side settings live in this browser;
          server-side settings persist in the shared bacio store.
        </p>
      </header>

      {/* Appearance — client-side only (theme is localStorage, not
          synced to the server in this scope per BACI-248 out-of-scope). */}
      <section className="mk-settings-row">
        <div className="mk-settings-row-text">
          <div className="mk-settings-label">
            Theme
            <ScopeChip kind="client" />
          </div>
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

      {/* Display — server-side (display.show_archived). */}
      <section className="mk-settings-row">
        <div className="mk-settings-row-text">
          <div className="mk-settings-label">
            Show archived items
            <ScopeChip kind="server" />
          </div>
          <div className="mk-settings-hint">
            When on, archived issues, documents and features surface in the board, doc list and feature list (rendered visibly muted). When off (the default), they&apos;re hidden. The hourly auto-sweep archives completed work older than the retention window below; you can also archive any item manually.
          </div>
        </div>
        <div className="mk-segmented" role="group" aria-label="Show archived items">
          {ON_OFF_OPTIONS.map(opt => (
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

      {/* BACI-240 / BACI-295: ship-flourish ka-ching SFX toggle. On by
          default now. The play path is silently no-op'd by the browser's
          autoplay policy (the page needs at least one user gesture before
          audio is allowed), so it just stays quiet until you interact
          with the page. prefers-reduced-motion no longer mutes it —
          that preference governs animation, not audio. */}
      <section className="mk-settings-row">
        <div className="mk-settings-row-text">
          <div className="mk-settings-label">
            Ship sound
            <ScopeChip kind="server" />
          </div>
          <div className="mk-settings-hint">
            When on, the Pipeline's Shipped pill plays a short ka-ching whenever the Shipped count rolls up. On by default. Honours the OS-level mute and the browser autoplay policy — the sound silently no-ops (it needs at least one click on the page before audio is allowed) rather than erroring.
          </div>
        </div>
        <div className="mk-segmented" role="group" aria-label="Ship sound">
          {ON_OFF_OPTIONS.map(opt => (
            <button
              key={String(opt.id)}
              className={`mk-segmented-btn ${audioEnabled === opt.id ? 'is-active' : ''}`}
              aria-pressed={audioEnabled === opt.id}
              onClick={() => onChangeAudioEnabled(opt.id)}
            >
              {opt.label}
            </button>
          ))}
        </div>
      </section>

      {/* Auto-archive — pair of server-side settings. */}
      <section className="mk-settings-row">
        <div className="mk-settings-row-text">
          <div className="mk-settings-label">
            Auto-archive completed issues
            <ScopeChip kind="server" />
          </div>
          <div className="mk-settings-hint">
            When on (the default), the hourly sweep hides done / cancelled issues whose terminal-state timestamp is older than the retention window. You can always unarchive items from the board. The feature + linked-doc cascade still runs when this is off, so manually archiving an issue still tidies up its parents.
          </div>
        </div>
        <div className="mk-segmented" role="group" aria-label="Auto-archive completed issues">
          {ON_OFF_OPTIONS.map(opt => (
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
          <div className="mk-settings-label">
            Retention window (days)
            <ScopeChip kind="server" />
          </div>
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

      {/* Prompt templates — the dense editor that used to dominate the
          page. Lives at the foot of System under a divider so the
          small toggles above don't visually flow into it. */}
      <section className="mk-settings-section">
        <div className="mk-settings-row-text">
          <div className="mk-settings-label">
            Prompt templates
            <ScopeChip kind="server" />
          </div>
          <div className="mk-settings-hint">
            The instruction sent to an agent when you dispatch a job at each template. You can add, rename, and delete templates here — built-ins can be deleted too, and &quot;Restore built-ins&quot; re-seeds any that are missing.
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

      {/* About — read-only footer band. */}
      <section className="mk-settings-row">
        <div className="mk-settings-row-text">
          <div className="mk-settings-label">Bacio version</div>
          <div className="mk-settings-hint">
            The version of the bacio binary this desktop app is running.
            Cross-check against the &quot;Bacio version&quot; on each agent in the
            Agents panel to spot agents talking to outdated channels.
          </div>
        </div>
        <div className="mk-settings-value"><code>{bacioVer || '—'}</code></div>
      </section>

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
          that referenced this slug will keep it verbatim but won&apos;t have a body to
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
          Re-seed any built-in template that&apos;s been deleted, from the
          embedded defaults. Existing templates won&apos;t be touched (idempotent).
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
