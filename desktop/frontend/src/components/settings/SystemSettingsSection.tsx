import React, { useState, useEffect } from 'react';
import Tooltip from '../Tooltip';
import { useTimezoneOptions } from './useTimezoneOptions';
import { EMPTY_NEW_TEMPLATE, useTemplateManagement } from './useTemplateManagement';
import TemplateAddForm from './TemplateAddForm';
import TemplateRow from './TemplateRow';
import ConfirmModal from './ConfirmModal';

// BACI-248: System Settings section — the global, app-wide preferences
// pane carved out of the old single-scroll SettingsView body. Mounted
// inside the new sectioned SettingsView when the section rail's
// "System" entry is active. Owns the same five sub-bands the old page
// did (Appearance / Display / Audio / Auto-archive / Prompt-templates /
// About) plus the per-row Client / Server scope chip the ticket calls
// for so the user can see at a glance which preferences live on this
// browser vs the shared store.
//
// BACI-364: the dense prompt-template manager (the 8-useState CRUD cluster
// + its rows / forms / confirm modals) now lives behind useTemplateManagement
// and the TemplateRow / TemplateAddForm / ConfirmModal components — this file
// is the form shell. Props mirror what App.tsx already passes — no behaviour
// change beyond the layout reshape.

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

// ScopeChip renders the small "Client" / "Server" pill rendered next
// to the row label inside System. Client = stored in browser
// localStorage (theme); Server = stored in app_settings on the shared
// SQLite store. Hint text and behaviour come from the surrounding row;
// this chip is purely a visual delineator.
type ScopeChipProps = { kind: 'client' | 'server' };

function ScopeChip({ kind }: ScopeChipProps) {
  const label = kind === 'client' ? 'Client' : 'Server';
  return (
    <span className={`mk-settings-scope-chip mk-settings-scope-chip--${kind}`}>
      {label}
    </span>
  );
}

type SystemSettingsSectionProps = {
  theme: string;
  onChangeTheme: (theme: string) => void;
  showArchived: boolean;
  onChangeShowArchived: (next: boolean) => void;
  archiveAutoEnabled: boolean;
  archiveRetentionDays: number;
  onChangeArchivePreferences: (autoEnabled: boolean, retentionDays: number) => void;
  audioEnabled: boolean;
  onChangeAudioEnabled: (next: boolean) => void;
  timezone: string;
  onChangeTimezone: (next: string) => void;
  onTemplatesChanged?: () => void;
};

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
  timezone,
  onChangeTimezone,
  onTemplatesChanged,
}: SystemSettingsSectionProps) {
  const timezoneOptions = useTimezoneOptions(timezone);

  // BACI-162: local draft for the retention-days input. We commit to
  // the App-owned state via onChangeArchivePreferences on blur (rather
  // than every keystroke) so a half-typed value doesn't round-trip
  // through the API on every digit. Kept in lockstep with the App
  // state by syncing on prop change.
  const [retentionDraft, setRetentionDraft] = useState(String(archiveRetentionDays));
  useEffect(() => {
    setRetentionDraft(String(archiveRetentionDays));
  }, [archiveRetentionDays]);

  const {
    templates,
    placeholders,
    drafts,
    savingSlug,
    bacioVer,
    adding,
    setAdding,
    renaming,
    setRenaming,
    pendingDelete,
    setPendingDelete,
    pendingRestore,
    setPendingRestore,
    setDraft,
    saveTemplate,
    saveConcurrency,
    saveActionLabel,
    commitAdd,
    commitRename,
    commitDelete,
    commitRestore,
  } = useTemplateManagement(onTemplatesChanged);

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

      {/* BACI-312: timezone — server-side setting driving the Pipeline
          Shipped pill's local-midnight "Today" cutoff. A native <select>
          over the browser's IANA zone list (type-ahead searchable); the
          server stays timezone-agnostic — the browser does the midnight
          math. Auto-detected + persisted on first run by App.tsx. */}
      <section className="mk-settings-row">
        <div className="mk-settings-row-text">
          <div className="mk-settings-label">
            Timezone
            <ScopeChip kind="server" />
          </div>
          <div className="mk-settings-hint">
            Your IANA timezone. The Pipeline's Shipped pill counts "Today" from local midnight in this zone (rather than a rolling 24 hours). Auto-detected from your browser on first run — change it here if it's wrong.
          </div>
        </div>
        <select
          className="mk-tmpl-input"
          aria-label="Timezone"
          value={timezone || ''}
          onChange={(e) => onChangeTimezone(e.target.value)}
        >
          {!timezone && <option value="" disabled>Detecting…</option>}
          {timezoneOptions.map(z => (
            <option key={z} value={z}>{z}</option>
          ))}
        </select>
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
          <TemplateAddForm
            draft={adding}
            onChange={setAdding}
            onCommit={commitAdd}
          />
        )}

        {templates.map(t => (
          <TemplateRow
            key={t.slug}
            template={t}
            draft={drafts[t.slug] ?? t.body}
            busy={savingSlug === t.slug}
            onDraftChange={setDraft}
            onSaveBody={saveTemplate}
            onSaveActionLabel={saveActionLabel}
            onSaveConcurrency={saveConcurrency}
            onRename={tpl => setRenaming({ slug: tpl.slug, newSlug: tpl.slug, newName: tpl.label })}
            onDelete={setPendingDelete}
          />
        ))}
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

      <ConfirmModal
        open={!!renaming}
        onClose={() => setRenaming(null)}
        title="Rename template"
        confirmLabel="Save"
        onConfirm={commitRename}
      >
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
          </>
        )}
      </ConfirmModal>

      <ConfirmModal
        open={!!pendingDelete}
        onClose={() => setPendingDelete(null)}
        title="Delete template"
        confirmLabel="Delete"
        onConfirm={commitDelete}
      >
        <p>
          Delete the template <code>{pendingDelete}</code>? Historical dispatches
          that referenced this slug will keep it verbatim but won&apos;t have a body to
          render anymore.
        </p>
      </ConfirmModal>

      <ConfirmModal
        open={pendingRestore}
        onClose={() => setPendingRestore(false)}
        title="Restore built-in templates"
        confirmLabel="Restore"
        onConfirm={commitRestore}
      >
        <p>
          Re-seed any built-in template that&apos;s been deleted, from the
          embedded defaults. Existing templates won&apos;t be touched (idempotent).
        </p>
      </ConfirmModal>
    </div>
  );
}
