import { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react';
import type { Dispatch, ReactNode, SetStateAction } from 'react';
import * as api from '../api';
import type { PromptTemplateDTO } from '../api';
import { reportError } from '../errors';
import { useLocalStorage } from '../lib/hooks/useLocalStorage';

// PreferencesProvider (BACI-361) owns the rare-cadence global preference
// state App.tsx used to carry: the theme + its <html data-theme> effect,
// the BACI-68 show-archived toggle, the BACI-162 auto-archive pair, the
// BACI-240 ship SFX toggle, the BACI-312 timezone (with first-run
// auto-detect-then-persist), and the global dispatch-prompt config. It is
// the outermost data provider — nothing here keys off the active repo, so
// it sits above RepoProvider in the tree. The state moves verbatim from
// App; only the wrapping changes.

const THEME_KEY = 'bacio-theme'; // persisted preference: 'system' | 'light' | 'dark'

export type PreferencesContextValue = {
  theme: string;
  setTheme: Dispatch<SetStateAction<string>>;
  showArchived: boolean;
  changeShowArchived: (next: boolean) => void;
  archiveAutoEnabled: boolean;
  archiveRetentionDays: number;
  changeArchivePreferences: (autoEnabled: boolean, retentionDays: number) => void;
  audioEnabled: boolean;
  changeAudioEnabled: (next: boolean) => void;
  timezone: string;
  changeTimezone: (next: string) => void;
  promptConfig: PromptTemplateDTO[];
  refreshPromptConfig: () => void;
};

const PreferencesContext = createContext<PreferencesContextValue | null>(null);

export function PreferencesProvider({ children }: { children: ReactNode }) {
  const [theme, setTheme] = useLocalStorage(THEME_KEY, 'system');
  // BACI-68: when true, archived rows surface in default lists / board /
  // docs / features views (rendered visibly muted); false (the default)
  // hides them.
  const [showArchived, setShowArchived] = useState(false);
  // BACI-162: auto-archive settings. archiveAutoEnabled gates the hourly
  // issue auto-archive pass; archiveRetentionDays is the terminal-state
  // dwell before a sweep archives. Loaded on mount; flipped atomically.
  const [archiveAutoEnabled, setArchiveAutoEnabled] = useState(true);
  const [archiveRetentionDays, setArchiveRetentionDays] = useState(7);
  // BACI-240 / BACI-295: ui.shipped_sfx — the ka-ching on a genuine ship.
  // Default ON (BACI-295); this seed governs only the brief window before
  // getAudioPreferences() resolves on mount.
  const [audioEnabled, setAudioEnabled] = useState(true);
  // BACI-312: ui.timezone — the IANA zone driving the Shipped pill's
  // local-midnight "Today" cutoff. Empty until getTimezonePreferences()
  // resolves; an empty value triggers the first-run auto-detect-then-
  // persist below.
  const [timezone, setTimezone] = useState('');
  // The global (repo-independent) dispatch-prompt config: one entry per
  // stage with its label and the issue states it's valid to run from.
  // PipelineView reads it to gate the per-card action button. Loaded on
  // mount; reloaded when the Settings view closes (Shell drives that).
  const [promptConfig, setPromptConfig] = useState<PromptTemplateDTO[]>([]);

  // Resolve the System/Light/Dark preference to a concrete light|dark value
  // and write it to <html data-theme>. In 'system' mode, track the OS setting
  // live so the app follows appearance changes without a relaunch.
  //
  // Only 'system' mode attaches a listener; 'light'/'dark' return no cleanup.
  // That's safe: when switching away from 'system', React runs this effect's
  // previous cleanup (which removes the system listener) before re-running.
  useEffect(() => {
    const mq = window.matchMedia('(prefers-color-scheme: dark)');
    const apply = () => {
      const resolved = theme === 'system' ? (mq.matches ? 'dark' : 'light') : theme;
      document.documentElement.dataset.theme = resolved;
    };
    apply();
    if (theme === 'system') {
      mq.addEventListener('change', apply);
      return () => mq.removeEventListener('change', apply);
    }
  }, [theme]);

  // Load the global preferences + prompt config once on mount. A failure
  // surfaces through the error modal; the views render their own empty
  // states until the data lands. (BACI-361 split this out of App's
  // combined boards-and-prefs mount load — boards now load in RepoProvider.)
  useEffect(() => {
    Promise.all([
      api.listPromptTemplates(),
      api.getDisplayPreferences(),
      api.getArchivePreferences(),
      api.getAudioPreferences(),
      api.getTimezonePreferences(),
    ])
      .then(([tpls, displayPrefs, archivePrefs, audioPrefs, tzPrefs]) => {
        setPromptConfig(tpls);
        setShowArchived(displayPrefs.showArchived);
        setArchiveAutoEnabled(archivePrefs.autoEnabled);
        setArchiveRetentionDays(archivePrefs.retentionDays);
        setAudioEnabled(audioPrefs.shippedSfx);
        // BACI-312: seed the timezone from the server. On first run the
        // value is empty — auto-detect the browser's zone and persist it
        // so the setting is pre-filled and the "Today" cutoff is correct
        // from the very first poll. The persist is best-effort: a failure
        // leaves `timezone` empty and scopeSinceParams falls back to the
        // browser zone anyway, so "Today" still works.
        if (tzPrefs.timezone) {
          setTimezone(tzPrefs.timezone);
        } else {
          let detected = '';
          try {
            detected = Intl.DateTimeFormat().resolvedOptions().timeZone || '';
          } catch { /* Intl unavailable — leave empty, fall back to browser zone at use */ }
          if (detected) {
            setTimezone(detected);
            api.setTimezonePreferences(detected)
              .then(prefs => setTimezone(prefs.timezone))
              .catch(() => { /* best-effort first-run persist; cutoff still works via fallback */ });
          }
        }
      })
      .catch(err => reportError(err, { headline: "Couldn't load preferences" }));
  }, []);

  // changeShowArchived persists the BACI-68 display.show_archived toggle,
  // then updates the flag on success so the Board / Docs / Features views
  // react on the next refresh. Optimistic-then-confirmed shape.
  const changeShowArchived = useCallback((next: boolean) => {
    api.setDisplayPreferences(next)
      .then(prefs => setShowArchived(prefs.showArchived))
      .catch(err => reportError(err, { headline: "Couldn't save preference" }));
  }, []);

  // changeArchivePreferences persists the BACI-162 auto-archive pair
  // atomically, then updates state on success. The API rejects
  // retention_days outside 1..3650 with a 400, surfaced via reportError.
  const changeArchivePreferences = useCallback((autoEnabled: boolean, retentionDays: number) => {
    api.setArchivePreferences(autoEnabled, retentionDays)
      .then(prefs => {
        setArchiveAutoEnabled(prefs.autoEnabled);
        setArchiveRetentionDays(prefs.retentionDays);
      })
      .catch(err => reportError(err, { headline: "Couldn't save preference" }));
  }, []);

  // changeAudioEnabled persists the BACI-240 ui.shipped_sfx toggle. The
  // ship SFX hook (in CardsProvider) reads the flag per-play via its
  // internal ref, so a flip surfaces immediately without a re-fetch.
  const changeAudioEnabled = useCallback((next: boolean) => {
    api.setAudioPreferences(next)
      .then(prefs => setAudioEnabled(prefs.shippedSfx))
      .catch(err => reportError(err, { headline: "Couldn't save preference" }));
  }, []);

  // changeTimezone persists the BACI-312 ui.timezone setting, then updates
  // state so the Shipped pill's "Today" cutoff recomputes against the new
  // zone on the next poll. A malformed IANA name is rejected with a 400.
  const changeTimezone = useCallback((next: string) => {
    api.setTimezonePreferences(next)
      .then(prefs => setTimezone(prefs.timezone))
      .catch(err => reportError(err, { headline: "Couldn't save preference" }));
  }, []);

  // refreshPromptConfig reloads the global dispatch-prompt config. Called
  // when the Settings view closes (Shell drives it), since editing a
  // stage's state-gate there changes which prompts each card offers.
  // Failures stay silent — the existing in-memory config is still valid.
  const refreshPromptConfig = useCallback(() => {
    api.listPromptTemplates()
      .then(setPromptConfig)
      .catch(err => console.warn('prompt config refresh failed:', err));
  }, []);

  const value = useMemo<PreferencesContextValue>(
    () => ({
      theme,
      setTheme,
      showArchived,
      changeShowArchived,
      archiveAutoEnabled,
      archiveRetentionDays,
      changeArchivePreferences,
      audioEnabled,
      changeAudioEnabled,
      timezone,
      changeTimezone,
      promptConfig,
      refreshPromptConfig,
    }),
    [
      theme,
      setTheme,
      showArchived,
      changeShowArchived,
      archiveAutoEnabled,
      archiveRetentionDays,
      changeArchivePreferences,
      audioEnabled,
      changeAudioEnabled,
      timezone,
      changeTimezone,
      promptConfig,
      refreshPromptConfig,
    ],
  );

  return <PreferencesContext.Provider value={value}>{children}</PreferencesContext.Provider>;
}

// usePreferences reads the global preference state. Throws when called
// outside the provider so a missing wrap fails loudly rather than handing
// back a silent null.
export function usePreferences(): PreferencesContextValue {
  const ctx = useContext(PreferencesContext);
  if (!ctx) throw new Error('usePreferences must be used within a PreferencesProvider');
  return ctx;
}
