import React, { useState, useEffect, useCallback, useRef } from 'react';
import Icon from './Icon.jsx';
import SectionRail from './settings/SectionRail.jsx';
import SystemSettingsSection from './settings/SystemSettingsSection.jsx';
import SyncSettingsSection from './settings/SyncSettingsSection.jsx';
import PerRepoSettingsSection from './settings/PerRepoSettingsSection.jsx';

// SettingsView is the desktop / web Settings screen — a full-screen
// view (not a modal) covering the content area below the topbar.
//
// BACI-248: the old single-scroll body was carved into three scoped
// sections (System / Sync / Per-repository), each mounted in the
// right-pane router driven by the left-rail SectionRail. This shell
// owns the activeSection state + the router; every section component
// owns its own data loading + mutations. The standalone SyncView is
// folded into the Sync section here; the topbar Sync pill routes here
// with `initialSection='sync'` preselected.
//
// Sub-modal handling (Rename / Delete-template confirm / sync setup /
// phantom link): the child sections fire a `mk-settings-submodal`
// CustomEvent so the page-level Escape listener below skips closing
// the view while a sub-modal is open. Cleaner than threading a prop
// down through three siblings — the page only ever cares about
// "is any sub-modal open right now?".

const SECTIONS = [
  { id: 'system', label: 'System', hint: 'App-wide preferences' },
  { id: 'sync', label: 'Sync', hint: 'Background sync + registry' },
  { id: 'per-repo', label: 'Per-repository', hint: 'Per-repo defaults' },
];

export default function SettingsView({
  theme,
  onChangeTheme,
  showArchived,
  onChangeShowArchived,
  archiveAutoEnabled,
  archiveRetentionDays,
  onChangeArchivePreferences,
  audioEnabled,
  onChangeAudioEnabled,
  columns,
  onClose,
  onTemplatesChanged,
  repoPrefix,
  boards,
  initialSection,
}) {
  // Local-only selection state — opening Settings always lands on
  // System unless the topbar Sync pill routed in with `initialSection`.
  // The design doc explicitly recommends NOT persisting the last
  // section across opens (the discovery cost is low — only three
  // sections, all named by scope).
  const [activeSection, setActiveSection] = useState(() => (
    initialSection === 'sync' || initialSection === 'per-repo'
      ? initialSection
      : 'system'
  ));
  // Re-honour initialSection across opens — if the parent re-opens the
  // overlay with a different preselect (e.g. topbar Sync pill while
  // already on Settings/System), respect it.
  useEffect(() => {
    if (initialSection === 'sync' || initialSection === 'per-repo') {
      setActiveSection(initialSection);
    }
  }, [initialSection]);

  // Sub-modal open ref + counter. Sections fire `mk-settings-submodal`
  // events ({detail: {open: bool}}); the listener increments / decrements
  // a counter so concurrent sub-modals (rare but possible — e.g. open a
  // sync-setup modal while a Phantom modal is also mounted) don't race
  // each other into a wrong "open" state.
  const subModalOpenCount = useRef(0);
  const [subModalOpen, setSubModalOpen] = useState(false);
  useEffect(() => {
    const onSubModal = (e) => {
      const open = !!(e.detail && e.detail.open);
      subModalOpenCount.current = Math.max(0, subModalOpenCount.current + (open ? 1 : -1));
      setSubModalOpen(subModalOpenCount.current > 0);
    };
    window.addEventListener('mk-settings-submodal', onSubModal);
    return () => window.removeEventListener('mk-settings-submodal', onSubModal);
  }, []);

  // Page-level Escape closes the Settings view. Skipped when any
  // sub-modal is open — Radix Dialog catches Escape first via a
  // capture-phase listener and dismisses just the sub-modal, leaving
  // Settings open.
  useEffect(() => {
    const onKey = (e) => {
      if (e.key !== 'Escape') return;
      if (subModalOpen) return;
      onClose();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose, subModalOpen]);

  // Section router — one section mounted at a time. Mount-on-select
  // (no eager pre-load) keeps the polling effects in SyncSettingsSection
  // off the wire when the user is on System / Per-repo.
  const renderSection = useCallback(() => {
    switch (activeSection) {
      case 'sync':
        return <SyncSettingsSection />;
      case 'per-repo':
        return (
          <PerRepoSettingsSection
            activeBoard={repoPrefix}
            boards={boards}
            columns={columns}
          />
        );
      case 'system':
      default:
        return (
          <SystemSettingsSection
            theme={theme}
            onChangeTheme={onChangeTheme}
            showArchived={showArchived}
            onChangeShowArchived={onChangeShowArchived}
            archiveAutoEnabled={archiveAutoEnabled}
            archiveRetentionDays={archiveRetentionDays}
            onChangeArchivePreferences={onChangeArchivePreferences}
            audioEnabled={audioEnabled}
            onChangeAudioEnabled={onChangeAudioEnabled}
            onTemplatesChanged={onTemplatesChanged}
          />
        );
    }
  }, [
    activeSection,
    theme, onChangeTheme,
    showArchived, onChangeShowArchived,
    archiveAutoEnabled, archiveRetentionDays, onChangeArchivePreferences,
    audioEnabled, onChangeAudioEnabled,
    columns, onTemplatesChanged,
    repoPrefix, boards,
  ]);

  return (
    <div className="mk-settings-view">
      <header className="mk-settings-bar">
        <h2 className="mk-settings-title">Settings</h2>
        <button className="mk-icbtn" aria-label="Close" onClick={onClose}>
          <Icon name="x" />
        </button>
      </header>
      <div className="mk-settings-pane">
        <SectionRail
          sections={SECTIONS}
          activeId={activeSection}
          onSelect={setActiveSection}
        />
        <div className="mk-settings-main">
          {renderSection()}
        </div>
      </div>
    </div>
  );
}
