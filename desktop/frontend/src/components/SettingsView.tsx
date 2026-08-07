import { useState, useEffect, useCallback, useRef } from 'react';
import Icon from './Icon';
import SectionRail from './settings/SectionRail';
import SystemSettingsSection from './settings/SystemSettingsSection';
import SyncSettingsSection from './settings/SyncSettingsSection';
import PerRepoSettingsSection from './settings/PerRepoSettingsSection';
import { usePreferences } from '../state/PreferencesProvider';
import { useActiveRepo } from '../state/RepoProvider';

// SettingsView is the desktop / web Settings screen — a full-screen
// view (not a modal) covering the content area below the topbar.
//
// BACI-248: the old single-scroll body was carved into scoped sections,
// each mounted in the right-pane router driven by the left-rail
// SectionRail. This shell owns the activeSection state + the router;
// every section component owns its own data loading + mutations. The
// standalone SyncView is folded into the Sync section here; the topbar
// Sync pill routes here with `initialSection='sync'` preselected.
//
// The space's own settings lead the rail and are the default section:
// they are the ones a user reaches for, and now that a bacio store
// holds several spaces of two different kinds, "which space am I
// editing" is the first question the page has to answer. The rail
// entry is named after the active space for exactly that reason — the
// section used to carry its own repo dropdown, a second and
// independent notion of the current space that could silently disagree
// with the topbar.
//
// Sub-modal handling (Rename / Delete-template confirm / sync setup /
// phantom link): the child sections fire a `mk-settings-submodal`
// CustomEvent so the page-level Escape listener below skips closing
// the view while a sub-modal is open. Cleaner than threading a prop
// down through three siblings — the page only ever cares about
// "is any sub-modal open right now?".

type SettingsSection = 'space' | 'system' | 'sync';

// BACI-361: the App-wide preferences + the active repo's board/columns come
// from usePreferences() / useActiveRepo(); only the shell-owned overlay
// controls (onClose, initialSection) stay props.
type SettingsViewProps = {
  onClose: () => void;
  initialSection: string | null;
};

export default function SettingsView({ onClose, initialSection }: SettingsViewProps) {
  const {
    theme,
    setTheme: onChangeTheme,
    showArchived,
    changeShowArchived: onChangeShowArchived,
    archiveAutoEnabled,
    archiveRetentionDays,
    changeArchivePreferences: onChangeArchivePreferences,
    audioEnabled,
    changeAudioEnabled: onChangeAudioEnabled,
    timezone,
    changeTimezone: onChangeTimezone,
    refreshPromptConfig: onTemplatesChanged,
  } = usePreferences();
  // Only the rail hint needs the prefix here now — the Space section
  // reads the active space from useActiveRepo() itself rather than
  // taking it as a prop, so there is exactly one notion of "the current
  // space" on this page.
  const { activeBoard: repoPrefix, columns } = useActiveRepo();
  // The rail is built here rather than as a module const so the Space
  // entry's hint can name the space you are actually editing — the
  // affordance that replaced the section's own repo dropdown.
  const SECTIONS: { id: SettingsSection; label: string; hint: string }[] = [
    { id: 'space', label: 'Space', hint: repoPrefix || 'No space selected' },
    { id: 'system', label: 'System', hint: 'App-wide preferences' },
    { id: 'sync', label: 'Sync', hint: 'Background sync + registry' },
  ];
  // Local-only selection state — opening Settings always lands on the
  // active space's own settings unless the topbar Sync pill routed in
  // with `initialSection`. The design doc explicitly recommends NOT
  // persisting the last section across opens (the discovery cost is low
  // — only three sections, all named by scope).
  const [activeSection, setActiveSection] = useState<SettingsSection>(() => (
    initialSection === 'sync' || initialSection === 'system'
      ? initialSection
      : 'space'
  ));
  // Re-honour initialSection across opens — if the parent re-opens the
  // overlay with a different preselect (e.g. topbar Sync pill while
  // already on Settings/Space), respect it.
  useEffect(() => {
    if (initialSection === 'sync' || initialSection === 'system') {
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
    const onSubModal = (e: Event) => {
      const detail = (e as CustomEvent<{ open?: boolean }>).detail;
      const open = !!(detail && detail.open);
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
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape') return;
      if (subModalOpen) return;
      onClose();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose, subModalOpen]);

  // Section router — one section mounted at a time. Mount-on-select
  // (no eager pre-load) keeps the polling effects in SyncSettingsSection
  // off the wire when the user is on Space / System.
  const renderSection = useCallback(() => {
    switch (activeSection) {
      case 'sync':
        return <SyncSettingsSection />;
      case 'space':
        return <PerRepoSettingsSection columns={columns} />;
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
            timezone={timezone}
            onChangeTimezone={onChangeTimezone}
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
    timezone, onChangeTimezone,
    columns, onTemplatesChanged,
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
