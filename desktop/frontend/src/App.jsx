import React, { useState, useEffect, useCallback, useRef } from 'react';
import { Routes, Route, Navigate, useNavigate, useLocation } from 'react-router';
import { Events } from '@wailsio/runtime';
import Topbar, { NAV } from './components/Topbar.jsx';
import DocsView from './components/DocsView.jsx';
import FeaturesView from './components/FeaturesView.jsx';
import AgentsView from './components/AgentsView.jsx';
import HistoryView from './components/HistoryView.jsx';
import MonitorView from './components/MonitorView.jsx';
import TranscriptRoute from './components/TranscriptRoute.jsx';
import PipelineView from './components/PipelineView.jsx';
import ProcessEditor from './components/ProcessEditor.jsx';
import IssueWorkspace from './components/IssueWorkspace.jsx';
import CommandPalette from './components/CommandPalette.jsx';
import IssueComposer from './components/IssueComposer.jsx';
import RepoNotFound from './components/RepoNotFound.jsx';
import { readShippedScope, persistShippedScope } from './components/shippedScopePersistence.ts';
import { scopeSinceParams } from './components/shippedScope.ts';
import SettingsView from './components/SettingsView.jsx';
import ErrorBoundary from './components/ErrorBoundary.jsx';
import ErrorModal from './components/ErrorModal.jsx';
import { Provider as TooltipProvider } from '@radix-ui/react-tooltip';
import { LazyMotion, domMax, LayoutGroup } from 'motion/react';
import { reportError } from './errors';
import { WEB_MODE } from './env';
import * as api from './api';
import { isTerminalState, stripBlockerFromCards, restoreBlockedByFromSnapshot } from './lib/issueState';
import { useShipFlourish } from './lib/shipFlourish';
import { useShipSfx } from './lib/shipSfx';
import { decideOdometerAction } from './lib/odometer';
import { viewPath, issuePath, processEditPath, viewFromPath, prefixFromPath } from './lib/routes';

const THEME_KEY = 'bacio-theme'; // persisted preference: 'system' | 'light' | 'dark'
const REPO_KEY = 'bacio-active-repo'; // persisted preference: last-selected repo prefix
const POLL_INTERVAL_MS = 10_000; // Board/Agents auto-refresh cadence while on-screen

// localStorage is always present inside the Wails webview, but a hardened
// browser profile can throw on access — fall back to defaults rather than
// failing to boot.
function readTheme() {
  try { return localStorage.getItem(THEME_KEY) || 'system'; }
  catch { return 'system'; }
}
function persistTheme(theme) {
  try { localStorage.setItem(THEME_KEY, theme); }
  catch { /* non-fatal — the preference just won't survive a relaunch */ }
}
function readActiveRepo() {
  try { return localStorage.getItem(REPO_KEY) || ''; }
  catch { return ''; }
}
function persistActiveRepo(prefix) {
  try { localStorage.setItem(REPO_KEY, prefix); }
  catch { /* non-fatal — the preference just won't survive a relaunch */ }
}

// BACI-285: the prefix-less page words a stale legacy link
// (`/ui/pipeline`, `/ui/issues/BACI-1`, ...) can lead with. The first
// path segment landing on one of these — instead of a known repo
// prefix — is treated as a soft-redirect (rebase the path under the
// active repo's prefix) rather than a hard 404 (RepoNotFound).
const LEGACY_PAGE_WORDS = new Set(['pipeline', 'issues', 'features', 'documents', 'agents', 'history', 'monitor']);

// legacyPageWord reports whether the URL's first segment is one of the
// recognised prefix-less page words — the signal that an unmatched
// prefix is a stale legacy link to soft-redirect rather than a
// genuinely-unknown repo to hard-404.
function legacyPageWord(first) {
  return LEGACY_PAGE_WORDS.has(first);
}

// isEditingTarget reports whether a keystroke landed in something the user is
// typing into — a form field or the contenteditable doc editor — so global
// hotkeys can stand down rather than hijack the keypress.
function isEditingTarget(el) {
  if (!el) return false;
  const tag = el.tagName;
  return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || el.isContentEditable;
}

export default function App() {
  const navigate = useNavigate();
  const location = useLocation();

  const [boards, setBoards] = useState([]);
  const [columns, setColumns] = useState([]);
  const [cards, setCards] = useState([]);
  // BACI-203: openIssueKey is now derived from the URL — the
  // `/issues/:key` workspace route owns the source of truth. The App
  // still keeps the brief payload around (loaded eagerly on key change,
  // polled every 10s while the workspace route is mounted) and a
  // descEditing flag the workspace's InlineDescriptionEditor propagates
  // up so the brief-poll merge can preserve the in-progress textarea.
  const [openIssueBrief, setOpenIssueBrief] = useState(null);
  const [descEditing, setDescEditing] = useState(false);
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);
  // BACI-248: SettingsView now owns the Sync section internally. The
  // topbar Sync pill opens Settings with `initialSection='sync'`
  // preselected so the muscle memory of "click sync pill, see sync
  // state" is preserved; settingsInitialSection rides alongside
  // settingsOpen and gets reset to null whenever the view closes so
  // the next plain "click the gear" open lands on System.
  const [settingsInitialSection, setSettingsInitialSection] = useState(null);
  // BACI-166: the "+ from prompt" composer is a sibling modal flag —
  // reached via the Topbar's `+` button or the ⌘N shortcut. The modal
  // creates a fresh todo card from a rough one-liner (BACI-300 retired
  // the auto-scope dispatch — triage is a Pipeline stage now).
  const [composerOpen, setComposerOpen] = useState(false);
  const [agents, setAgents] = useState([]);
  // promptConfig is the global (repo-independent) dispatch-prompt config:
  // one entry per stage with its label and the issue states it's valid
  // to run from. Board → KanbanCard reads it to gate the per-card action
  // button. Loaded on mount; reloaded when the Settings view closes.
  const [promptConfig, setPromptConfig] = useState([]);
  // BACI-188: per-column collapse state lives in Board.jsx now —
  // localStorage-backed, per-repo. The App-wide hide-empty-columns
  // preference was removed in BACI-188 (Settings toggle gone, REST /
  // Wails / store endpoints deleted).
  // BACI-68: the App-owned display preference. When true, archived
  // rows surface in default lists / board / docs / features views
  // (rendered visibly muted). When false (the default), they're
  // hidden. The per-call --include-archived flag the CLI exposes has
  // no desktop counterpart — toggle the setting from Settings instead.
  const [showArchived, setShowArchived] = useState(false);
  // BACI-162: the App-owned auto-archive settings. archiveAutoEnabled
  // gates the hourly issue auto-archive pass (defaults to true);
  // archiveRetentionDays is the number of days a terminal-state issue's
  // terminal_at must sit before the next sweep archives it. Loaded
  // alongside the other settings on mount; flipped from the Settings
  // screen via setArchivePreferences (atomic pair write).
  const [archiveAutoEnabled, setArchiveAutoEnabled] = useState(true);
  const [archiveRetentionDays, setArchiveRetentionDays] = useState(7);
  // BACI-240: ui.shipped_sfx toggle — when true, a short ka-ching SFX
  // plays on every genuine ship (the Pipeline Shipping-column Shipped
  // pill's odometer rolling into a new value). BACI-295 flipped the default
  // ON now that the feature has shipped. This seed only governs the
  // brief window before getAudioPreferences() resolves on mount and
  // overwrites it with the server value; it still mirrors the new
  // default. Flipped from Settings.
  const [audioEnabled, setAudioEnabled] = useState(true);
  // BACI-312: ui.timezone — the user's IANA zone name driving the
  // Pipeline Shipping-column Shipped pill's local-midnight "Today" cutoff.
  // Empty until getTimezonePreferences() resolves on mount; an empty value
  // also triggers the first-run auto-detect-then-persist below. The
  // scopeSinceParams cutoff helper falls back to the browser's own zone
  // when this is still empty, so "Today" never breaks in the gap before
  // the value lands. Flipped from Settings.
  const [timezone, setTimezone] = useState('');
  // leaderState tracks the UI leader-election result from LeaderService.
  // amLeader = true means this desktop process holds the lease and may
  // dispatch. Standby processes show a chip and disable the per-card button.
  const [leaderState, setLeaderState] = useState({ amLeader: false, holderLabel: '' });
  const [theme, setTheme] = useState(readTheme);
  const [loading, setLoading] = useState(true);

  // BACI-285: the URL's first segment is the active repo prefix — the
  // single source of truth for the active repo. `urlPrefix` is the raw
  // segment (may be empty on bare `/`, or an unknown repo on a stale /
  // mistyped link); `activeBoard` below is the validated repo the rest
  // of the App keys off, or "" until boards load / when the prefix is
  // unknown. App sits above <Routes> so it can't own a `:prefix` route
  // param — it parses the prefix off location.pathname the same way it
  // already parses openIssueKey.
  const urlPrefix = prefixFromPath(location.pathname);
  // Match the URL prefix to a known board case-insensitively so a
  // lowercased shared link still resolves; emit the canonical
  // (board.prefix) casing so generated URLs and the localStorage seed
  // stay canonical. "" until boards load or when the prefix is unknown.
  const matchedBoard = boards.find(b => b.prefix.toLowerCase() === urlPrefix.toLowerCase());
  const activeBoard = matchedBoard?.prefix ?? '';
  // BACI-285: a non-empty URL prefix that doesn't match any known board
  // — and isn't a recognised prefix-less legacy page word (those soft-
  // redirect through the <Routes> `*` branch) — is a hard 404 once
  // boards have loaded; RepoNotFound renders instead of the page routes.
  // While loading we defer the decision (boards aren't in yet, so a
  // valid prefix would otherwise 404 itself).
  const prefixUnknown =
    !loading && !!urlPrefix && !matchedBoard && !legacyPageWord(urlPrefix);
  // The repo a prefix-less / bare path should redirect to: the last
  // validated localStorage pick if it still exists, else the first board.
  const fallbackPrefix = (() => {
    const remembered = readActiveRepo();
    if (boards.some(b => b.prefix === remembered)) return remembered;
    return boards[0]?.prefix ?? '';
  })();
  // BACI-285: where a bare `/` or prefix-less legacy path redirects.
  // Bare `/` → the fallback repo's Pipeline. A legacy page link
  // (`/pipeline`, `/issues/BACI-1`, ...) keeps its page path, just
  // rebased under the fallback prefix so the recipient lands on the
  // same screen. An empty fallback (no repos at all) lands on bare `/`
  // so the empty-state renders rather than a `//` path.
  const legacyRedirectTarget = (() => {
    if (!fallbackPrefix) return '/';
    const rest = location.pathname.replace(/^\/+/, '');
    if (rest && legacyPageWord(urlPrefix)) return `/${fallbackPrefix}/${rest}`;
    return viewPath(fallbackPrefix, 'pipeline');
  })();

  // BACI-203: derive the active view from the URL path. The Topbar
  // reads the same value (via useLocation()), so the App and the
  // Topbar stay in lockstep without a prop ping-pong.
  const activeView = viewFromPath(location.pathname) || 'board';
  // BACI-203 / BACI-285: derive the open issue key from the route.
  // Matches the `/<prefix>/issues/:key` URL shape; null when we're on a
  // list or detail route that isn't an issue workspace.
  const openIssueKey = (() => {
    const m = location.pathname.match(/^\/[^/]+\/issues\/([^/]+)$/);
    return m ? m[1] : null;
  })();

  // Resolve the System/Light/Dark preference to a concrete light|dark value
  // and write it to <html data-theme>. In 'system' mode, track the OS setting
  // live so the app follows appearance changes without a relaunch.
  //
  // Only 'system' mode attaches a listener; 'light'/'dark' return no cleanup.
  // That's safe: when switching away from 'system', React runs this effect's
  // previous cleanup (which removes the system listener) before re-running.
  useEffect(() => {
    persistTheme(theme);
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

  // Load the repository list + columns + prompt config once on mount.
  // BACI-285: activeBoard is now derived from the URL prefix (see the
  // urlPrefix block above), so the mount effect no longer seeds it —
  // the redirect routing below lands a prefix-less / bare path on a
  // concrete repo once boards resolve. The prompt config is global
  // (repo-independent), so it loads here too. A mount-time failure
  // no longer blanks the renderer (BACI-43): the modal surfaces the
  // error, the Topbar stays usable, and the views render their own
  // empty states until the data lands.
  useEffect(() => {
    Promise.all([
      api.listBoards(),
      api.listColumns(),
      api.listPromptTemplates(),
      api.getDisplayPreferences(),
      api.getArchivePreferences(),
      api.getAudioPreferences(),
      api.getTimezonePreferences(),
    ])
      .then(([bs, cols, tpls, displayPrefs, archivePrefs, audioPrefs, tzPrefs]) => {
        setBoards(bs);
        setColumns(cols);
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
        setLoading(false);
      })
      .catch(err => {
        reportError(err, { headline: "Couldn't load boards" });
        setLoading(false);
      });
  }, []);

  // Subscribe to leader-election state. Desktop mode reads the per-process
  // LeaderService (the Wails Events bus pushes each tick); web mode polls
  // the bacio api's GET /leader on the same 10s cadence the server's
  // elector heartbeats — close enough that the chip never lags by more
  // than one tick. Both modes seed the initial state on mount so the
  // chip doesn't have to wait for the first interval to fire.
  useEffect(() => {
    api.getLeaderStatus().then(setLeaderState).catch(() => {});
    if (WEB_MODE) {
      const id = setInterval(() => {
        api.getLeaderStatus().then(setLeaderState).catch(() => {});
      }, POLL_INTERVAL_MS);
      return () => clearInterval(id);
    }
    const off = Events.On('leaderStatus', (e) => setLeaderState(e.data));
    return () => { if (typeof off === 'function') off(); };
  }, []);

  // changeShowArchived persists the BACI-68 display.show_archived
  // toggle, then updates the App-owned flag on success so the Board /
  // Docs / Features views react immediately on the next refresh.
  // Optimistic-then-confirmed shape — the theme handler is the model.
  const changeShowArchived = useCallback((next) => {
    api.setDisplayPreferences(next)
      .then(prefs => setShowArchived(prefs.showArchived))
      .catch(err => reportError(err, { headline: "Couldn't save preference" }));
  }, []);

  // changeArchivePreferences persists the BACI-162 auto-archive pair
  // atomically, then updates the App-owned state on success. Both
  // fields travel together; the API rejects retention_days outside
  // 1..3650 with a 400, surfaced via reportError. The Settings UI
  // gates the numeric input on the boolean so the operator can flip
  // off auto-archive without first nudging the number into range.
  const changeArchivePreferences = useCallback((autoEnabled, retentionDays) => {
    api.setArchivePreferences(autoEnabled, retentionDays)
      .then(prefs => {
        setArchiveAutoEnabled(prefs.autoEnabled);
        setArchiveRetentionDays(prefs.retentionDays);
      })
      .catch(err => reportError(err, { headline: "Couldn't save preference" }));
  }, []);

  // changeAudioEnabled persists the BACI-240 ui.shipped_sfx toggle.
  // The App mounts `useShipSfx({ enabled: audioEnabled })` and the
  // hook reads the flag per-play via its internal ref, so a flip
  // surfaces immediately without a re-fetch. Same optimistic-then-
  // confirmed shape as the other preference handlers. Pre-BACI-254
  // this was wired through ShippedPopover; the SFX now rides the
  // shippedCount-rise effect in App.jsx (BACI-295) so it fires
  // regardless of the active view.
  const changeAudioEnabled = useCallback((next) => {
    api.setAudioPreferences(next)
      .then(prefs => setAudioEnabled(prefs.shippedSfx))
      .catch(err => reportError(err, { headline: "Couldn't save preference" }));
  }, []);

  // changeTimezone persists the BACI-312 ui.timezone setting, then
  // updates the App-owned state on success so the Shipped pill's "Today"
  // cutoff recomputes against the new zone on the next poll. Same
  // optimistic-then-confirmed shape as the other preference handlers. A
  // malformed IANA name is rejected with a 400, surfaced via reportError.
  const changeTimezone = useCallback((next) => {
    api.setTimezonePreferences(next)
      .then(prefs => setTimezone(prefs.timezone))
      .catch(err => reportError(err, { headline: "Couldn't save preference" }));
  }, []);

  // refreshPromptConfig reloads the global dispatch-prompt config. Called
  // when the Settings view closes, since editing a stage's state-gate
  // there changes which prompts each card offers. Failures stay silent
  // (console.warn) — the existing in-memory config is still valid and
  // modal-spamming on a poll-loop adjacent refresh would be hostile.
  const refreshPromptConfig = useCallback(() => {
    api.listPromptTemplates()
      .then(setPromptConfig)
      .catch(err => console.warn('prompt config refresh failed:', err));
  }, []);

  // Reload the prompt config whenever the Settings view closes — a
  // state-gate edit there changes which prompts each card offers. The
  // ref guards against the mount-time false→false non-transition.
  const prevSettingsOpen = useRef(false);
  useEffect(() => {
    if (prevSettingsOpen.current && !settingsOpen) refreshPromptConfig();
    prevSettingsOpen.current = settingsOpen;
  }, [settingsOpen, refreshPromptConfig]);

  const closeSettings = useCallback(() => {
    setSettingsOpen(false);
    // Reset the preselect so the next plain "open Settings" lands on
    // System, not whatever the previous open routed in with.
    setSettingsInitialSection(null);
  }, []);
  // BACI-248: the topbar Sync pill opens Settings on its Sync section.
  // The standalone SyncView is gone; this is now a one-line preselect
  // through the existing Settings entry point.
  const openSync = useCallback(() => {
    setSettingsInitialSection('sync');
    setSettingsOpen(true);
  }, []);

  // Remember the selected repo so the app reopens on the same one.
  useEffect(() => {
    if (activeBoard) persistActiveRepo(activeBoard);
  }, [activeBoard]);

  // refreshCards / refreshAgents reload the App-owned card and agent lists
  // for the active repo. Used by the repo-change effect, the screen-switch
  // effect, the 10s poll, the Agents panel's refresh button, and after a
  // dispatch so the counts move. Pass { silent: true } on the poll path so a
  // transient failure logs instead of pushing through the modal — a flapping
  // poll over a sleeping laptop shouldn't spam the user.
  const refreshCards = useCallback((opts = {}) => {
    if (!activeBoard) return;
    api.listCards(activeBoard)
      .then(setCards)
      .catch(err => {
        if (opts.silent) console.warn('card refresh failed:', err);
        else reportError(err, { headline: "Couldn't refresh board" });
      });
  }, [activeBoard]);

  const refreshAgents = useCallback((opts = {}) => {
    if (!activeBoard) return;
    api.listAgents(activeBoard)
      .then(setAgents)
      .catch(err => {
        if (opts.silent) console.warn('agent refresh failed:', err);
        else reportError(err, { headline: "Couldn't refresh agents" });
      });
  }, [activeBoard]);

  // Load cards + agents whenever the selected repository changes. Both stay
  // loaded regardless of the active view — CommandPalette reads cards
  // (and IssueWorkspace reads them for prev/next siblings), the Agents
  // tab reads agents, and either can open from any screen.
  useEffect(() => {
    if (!activeBoard) return;
    refreshCards();
    refreshAgents();
  }, [activeBoard, refreshCards, refreshAgents]);

  // Re-fetch the active screen's data on switch so it's fresh on arrival,
  // not mount-time/cached. Board + Agents only — Features/Docs/History are
  // self-owning components that re-fetch on their own remount.
  useEffect(() => {
    if (!activeBoard) return;
    if (activeView === 'board' || activeView === 'pipeline') refreshCards();
    else if (activeView === 'agents') refreshAgents();
  }, [activeView, refreshCards, refreshAgents]);

  // Poll the Board / Agents screens every 10s so they don't go stale while
  // open. The cleanup clears the interval on navigation away, repo change,
  // or unmount — no leaks, no redundant fetches off-screen.
  useEffect(() => {
    if (!activeBoard) return;
    if (activeView !== 'board' && activeView !== 'agents' && activeView !== 'pipeline') return;
    const id = setInterval(() => {
      if (activeView === 'board' || activeView === 'pipeline') refreshCards({ silent: true });
      else refreshAgents({ silent: true });
    }, POLL_INTERVAL_MS);
    return () => clearInterval(id);
  }, [activeView, activeBoard, refreshCards, refreshAgents]);

  // BACI-74: keep the top-nav Agents counters live regardless of which
  // view is showing. The Agents view's own poll above re-fetches on the
  // same cadence and overwrites the same state — running both is a
  // harmless duplicate hit, not a correctness issue, so the simpler
  // "always poll while a board is selected" rule wins. One small GET
  // per 10s is cheap.
  useEffect(() => {
    if (!activeBoard) return;
    const id = setInterval(() => refreshAgents({ silent: true }), POLL_INTERVAL_MS);
    return () => clearInterval(id);
  }, [activeBoard, refreshAgents]);

  // refreshBrief reloads the IssueWorkspace payload for the open issue.
  // Pass { silent: true } on the poll path so a transient failure logs
  // instead of pushing through the modal — same convention as cards/agents.
  // The descEditing guard preserves the user's in-progress description
  // textarea: when set, the new brief is taken but its description is
  // replaced by the previous one, so a poll landing mid-edit doesn't
  // stomp the buffer. Everything else (tags, comments, claimants, ...)
  // still refreshes.
  const refreshBrief = useCallback((opts = {}) => {
    if (!activeBoard || !openIssueKey) return;
    api.getIssueBrief(activeBoard, openIssueKey)
      .then(brief => {
        setOpenIssueBrief(prev => {
          if (descEditing && prev) {
            return {
              ...brief,
              issue: { ...brief.issue, description: prev.issue.description },
            };
          }
          return brief;
        });
      })
      .catch(err => {
        if (opts.silent) console.warn('brief refresh failed:', err);
        else reportError(err, { headline: "Couldn't refresh issue" });
      });
  }, [activeBoard, openIssueKey, descEditing]);

  // Eager load when openIssueKey changes (null → set, or one issue →
  // another via prev/next). Clear the stale brief so the workspace
  // skeleton renders instead of last issue's data flashing.
  useEffect(() => {
    if (!activeBoard || !openIssueKey) return;
    setOpenIssueBrief(null);
    refreshBrief();
  }, [activeBoard, openIssueKey]);

  // While the workspace is mounted, poll the brief every 10s alongside
  // the other view polls. Off-screen views get no refresh; the cleanup
  // clears the interval on close / repo change / unmount.
  useEffect(() => {
    if (!activeBoard || !openIssueKey || activeView !== 'board') return;
    // activeView === 'board' covers /issues and /issues/:key (both
    // derive to 'board' via viewFromPath); the openIssueKey guard
    // narrows the poll to the workspace route specifically.
    const id = setInterval(() => refreshBrief({ silent: true }), POLL_INTERVAL_MS);
    return () => clearInterval(id);
  }, [activeBoard, openIssueKey, activeView, refreshBrief]);

  // BACI-203: open the issue workspace by routing to /issues/:key.
  // BACI-248: SettingsView is an App-owned overlay (Sync is now folded
  // into it as a section) — dismiss it so the workspace is what the
  // user sees on arrival.
  const openCard = useCallback((card) => {
    if (!card?.key) return;
    setSettingsOpen(false);
    setSettingsInitialSection(null);
    navigate(issuePath(activeBoard, card.key));
  }, [navigate, activeBoard]);

  // BACI-114: the kanban blocked popover navigates by key — the
  // target may not be the card the popover is rendered on (it's the
  // blocker, not this card). Same effect as openCard but takes a key
  // directly, mirroring the `onNavigateIssue` path the workspace rail
  // already uses for prev/next sibling jumps.
  const openIssueByKey = useCallback((key) => {
    if (!key) return;
    setSettingsOpen(false);
    setSettingsInitialSection(null);
    navigate(issuePath(activeBoard, key));
  }, [navigate, activeBoard]);

  // Close the workspace: navigate back. With BrowserRouter the browser's
  // back stack handles "back to the previous view"; navigate(-1) goes
  // one step back, falling through to /pipeline if there's nothing on the
  // back stack (e.g. the user landed directly via a deep link).
  const closeIssue = useCallback(() => {
    setOpenIssueBrief(null);
    setDescEditing(false);
    // history.state is null on the very first entry — fall back to the
    // Pipeline (the board surface post-cutover) so a deep-link refresh
    // doesn't strand the user.
    if (window.history.state && window.history.length > 1) {
      navigate(-1);
    } else {
      navigate(viewPath(activeBoard, 'pipeline'));
    }
  }, [navigate, activeBoard]);

  // BACI-166 / BACI-332: composer success handler — optimistically prepend
  // the new card, then branch on the view it was created from:
  //
  //   - From the Pipeline screen, auto-scope it: pipe the card and queue a
  //     Scope job (the scope-shelve chain) on Auto, so it lands directly in
  //     the active Pipeline and gets scoped straight away rather than sitting
  //     in Backlog. Reuses fastTrackCard's exact in-order api.* sequence
  //     (state → process → engine mode), then routes to the Pipeline page.
  //     On a mid-chain failure the steps before it already persisted and the
  //     refresh leaves a coherent partial state (the bare card in Backlog)
  //     the user can complete by hand.
  //   - Off the Pipeline (board), behaviour is unchanged: route to the new
  //     card's workspace. The standing 10s poll replaces the optimistic row
  //     with the authoritative one on its next tick.
  const onComposerCreated = useCallback(async (newCard) => {
    if (!newCard || !newCard.key) return;
    setCards(cs => [{ ...newCard }, ...cs]);
    setSettingsOpen(false);
    setSettingsInitialSection(null);
    if (activeView === 'pipeline') {
      try {
        await api.setIssueState(activeBoard, newCard.key, 'in_pipeline');
        await api.setCardProcess(activeBoard, newCard.key, { process: 'scope-shelve' });
        await api.setEngineMode(activeBoard, newCard.key, 'auto');
      } catch (err) {
        reportError(err, { headline: "Couldn't scope the new card" });
      } finally {
        refreshCards({ silent: true });
      }
      navigate(viewPath(activeBoard, 'pipeline'));
      return;
    }
    navigate(issuePath(activeBoard, newCard.key));
  }, [navigate, activeBoard, activeView, refreshCards]);

  useEffect(() => {
    const onKey = (e) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault();
        setPaletteOpen(true);
      } else if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'n') {
        // BACI-166: ⌘N opens the IssueComposer. Skip when the user is
        // typing into a field (so the OS-level new-document shortcut
        // muscle memory doesn't fight us inside an editor). Guard on a
        // real repo prefix — the composer needs one to create against.
        if (isEditingTarget(e.target)) return;
        if (!activeBoard || activeBoard === 'all') return;
        e.preventDefault();
        setComposerOpen(true);
      } else if (e.key === 'Escape') {
        // Palette + Settings are still hand-rolled; the workspace
        // closes here when nothing else is in front of it. The composer
        // closes via Radix's built-in onOpenChange so we don't need to
        // intercept Escape for it here. SettingsView handles its own
        // page-level Escape internally (BACI-248) so it can suppress
        // the close while a sub-modal — rename template, sync setup,
        // phantom link — is open; the App-level branch below only
        // fires when SettingsView's listener doesn't (paletteOpen
        // already short-circuited or the user pressed it again).
        if (paletteOpen) {
          setPaletteOpen(false);
        } else if (openIssueKey && !isEditingTarget(e.target)) {
          closeIssue();
        } else if (settingsOpen) {
          // SettingsView already closed itself; this is the no-op
          // safety net when its own listener didn't fire for some
          // reason. Mirror its cleanup so the section preselect
          // resets too.
          setSettingsOpen(false);
          setSettingsInitialSection(null);
        }
      } else if (!e.metaKey && !e.ctrlKey && !e.altKey && e.key >= '1' && e.key <= '9') {
        // Digit keys jump between nav views, like the TUI's tab shortcuts —
        // unless the user is typing into a field or the doc editor. Skip
        // when no real repo is active (loading / not-found screen) so we
        // don't navigate to a prefix-less `//<view>` path.
        if (isEditingTarget(e.target)) return;
        if (!activeBoard) return;
        const idx = Number(e.key) - 1;
        if (idx < NAV.length) {
          navigate(viewPath(activeBoard, NAV[idx].view));
        }
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [paletteOpen, settingsOpen, openIssueKey, activeBoard, closeIssue, navigate]);

  // Add a repository. Desktop pops a native folder picker (Wails);
  // web mode hands the path-input modal's submission through as a
  // payload (BACI-50). On success, refresh the board list and jump
  // to the new repo; an empty prefix means the user cancelled the
  // dialog (desktop only — the web modal always submits a real path).
  const addRepository = (payload) => {
    return api.addRepository(payload)
      .then(board => {
        if (!board.prefix) return undefined;
        return api.listBoards().then(bs => {
          setBoards(bs);
          // BACI-285: the active repo is URL-derived now — route to the
          // new repo's pipeline so it becomes active.
          navigate(viewPath(board.prefix, 'pipeline'));
          return board;
        });
      })
      .catch(err => {
        reportError(err, { headline: "Couldn't add repository" });
        throw err;
      });
  };

  // Drag-to-move: optimistically move the card to the new column, then
  // persist the state change so it survives the next auto-refresh poll.
  // On failure, revert the card to its original column rather than leave
  // it stranded in a column the backend never accepted.
  //
  // Reads the previous column via the functional setCards form (and a
  // capture-and-noop trick) so the callback identity doesn't depend on
  // the cards array — keeps KanbanCard's React.memo effective.
  //
  // BACI-146: when the moved card lands in a terminal column
  // (done/cancelled) we also strip its key from every sibling card's
  // `blockedBy` array so the lock icon clears immediately rather than
  // waiting up to POLL_INTERVAL_MS for `refreshCards` to refetch the
  // server-filtered view. The server runs the same rule via
  // `boardcards.isCardBlockerOpen`, so this is purely a convergence
  // shortcut — the next refresh re-asserts the authoritative shape.
  // The inverse edge (terminal → non-terminal, ie. blocker reopened)
  // is covered by the targeted `refreshCards()` we fire after a
  // successful state move so the icon re-arms within ~one round-trip.
  const moveCard = useCallback((key, toCol) => {
    let prevCol = null;
    let prevBlockedBy = null;
    setCards(cs => {
      const prev = cs.find(c => c.key === key);
      if (!prev || prev.column === toCol) return cs;
      prevCol = prev.column;
      const enteringTerminal = isTerminalState(toCol) && !isTerminalState(prevCol);
      if (enteringTerminal) {
        // Snapshot only the cards that actually list `key` as a
        // blocker so rollback can put them back. Cards we didn't
        // touch stay out of the snapshot map.
        prevBlockedBy = new Map();
        for (const c of cs) {
          if ((c.blockedBy || []).some(b => b.key === key)) {
            prevBlockedBy.set(c.key, c.blockedBy);
          }
        }
      }
      const moved = cs.map(c => c.key === key ? { ...c, column: toCol } : c);
      return enteringTerminal ? stripBlockerFromCards(moved, key) : moved;
    });
    if (prevCol === null) return;
    api.setIssueState(activeBoard, key, toCol)
      .then(() => {
        // BACI-146: a non-silent refresh covers the reopen edge
        // (terminal → non-terminal) where the server needs to put
        // each affected sibling's `blockedBy` back. One HTTP call
        // per move is cheap and surfaces stale-board errors loudly.
        if (isTerminalState(prevCol) !== isTerminalState(toCol)) {
          refreshCards();
        }
      })
      .catch(err => {
        reportError(err, { headline: "Couldn't move card" });
        setCards(cs => {
          const reverted = cs.map(c => c.key === key ? { ...c, column: prevCol } : c);
          return prevBlockedBy ? restoreBlockedByFromSnapshot(reverted, prevBlockedBy) : reverted;
        });
      });
  }, [activeBoard, refreshCards]);

  // Dispatch a prompt from a card's action button: the backend gates the
  // mode on the issue's state and enqueues a target-less dispatch the
  // matcher binds later — the caller names neither an agent nor a note.
  // Post-BACI-51 the call always succeeds (gate-aside); the waiting
  // spinner takes over until the matcher binds.
  //
  // BACI-145: set an optimistic waitingState *before* the request so the
  // spinner and drag-disable kick in on the click. The optimistic state
  // is the no-agent flavour ("Waiting for an available agent") — the
  // first poll tick that arrives after the matcher actually classifies
  // the dispatch will replace it with the authoritative state (which
  // might be queued_blocked-by-concurrency-cap, or delivered if the
  // matcher binds + delivers fast). Revert to null on failure so the
  // spinner disappears.
  const dispatchFromCard = useCallback((cardKey, mode) => {
    setCards(cs => cs.map(c => c.key === cardKey ? { ...c, waitingState: { kind: 'queued_no_agent', mode } } : c));
    api.dispatchIssue(activeBoard, cardKey, mode)
      .catch(err => {
        setCards(cs => cs.map(c => c.key === cardKey ? { ...c, waitingState: null } : c));
        reportError(err, { headline: "Couldn't dispatch agent" });
      });
  }, [activeBoard]);

  // Phase 5 cutover: dispatchChainFromCard / setFollowOnFromCard /
  // cancelFollowOnFromCard / quickEvalComment were board-only affordances
  // (the kanban card's compound-dispatch, follow-on chip, and quick-eval
  // composer). The Pipeline drives work through the engine's job chain
  // instead, so they lost their only caller when the board was removed and
  // are gone. BACI-279 (Phase 6 decision) retired the underlying
  // compound-dispatch / follow-on api.* verbs too — quick-eval stays (eval
  // comments still render in the Activity timeline).

  // BACI-51 spinner-as-cancel-button handler: withdraw a card's queued
  // (or pending/delivered) dispatch. Optimistically clears the local
  // waitingState so the spinner disappears immediately; the refresh
  // poll reads the authoritative state.
  const cancelWaitingFromCard = useCallback((cardKey) => {
    api.cancelWaitingDispatch(activeBoard, cardKey)
      .then(() => {
        setCards(cs => cs.map(c => c.key === cardKey ? { ...c, waitingState: null } : c));
      })
      .catch(err => reportError(err, { headline: "Couldn't cancel queued dispatch" }));
  }, [activeBoard]);

  // ─── Pipeline (Phase 4) handlers ───────────────────────────────────
  // Each wraps the api.* pipeline call and refreshes the cards array so
  // the board re-renders with the server's authoritative shape (job
  // chain, engine mode, column). Optimistic flips mirror the
  // moveCard / dispatchFromCard patterns where the change is cheap to
  // predict; everything else relies on the refresh. The engine owns job
  // progression — these are the user-driven controls that nudge it.

  // Assign a process to a card — the in-pipeline "pick a process" picker.
  // selection is a discriminated shape ({ stages } from the cumulative
  // stepper, or { process } from the kept skip-Plan preset buttons).
  // Refreshes so the new pending job chain renders on the card.
  const setCardProcess = useCallback((key, selection) => {
    api.setCardProcess(activeBoard, key, selection)
      .then(() => refreshCards({ silent: true }))
      .catch(err => reportError(err, { headline: "Couldn't set the process" }));
  }, [activeBoard, refreshCards]);

  // BACI-334 "Confirm + Auto" — the picker's second confirm: set the process
  // and turn Auto on in one click. Mirrors fastTrackCard's ordered pair:
  // process must land before Auto (Auto on a chain-less card drives nothing),
  // a single error headline covers both calls, and the `finally` refresh
  // leaves a coherent partial state if the Auto call fails after the process
  // saved (chain set, Auto off — the user finishes with the card's switch).
  const setCardProcessAuto = useCallback(async (key, selection) => {
    try {
      await api.setCardProcess(activeBoard, key, selection);
      await api.setEngineMode(activeBoard, key, 'auto');
    } catch (err) {
      reportError(err, { headline: "Couldn't set the process" });
    } finally {
      refreshCards({ silent: true });
    }
  }, [activeBoard, refreshCards]);

  // Edit the pending tail of a card's chain — the BACI-294 Edit Process
  // screen's Save. stages is the re-ordered pending tail only; the server
  // keeps the completed/running/cancelled jobs as a locked prefix. Returns
  // the promise so ProcessEditor can navigate back on success / show its
  // own error banner on failure (no reportError here — the editor owns the
  // surface). Refreshes so the new chain renders on the card on return.
  const editCardProcessTail = useCallback((key, stages) => {
    return api.editCardProcessTail(activeBoard, key, stages)
      .then(() => refreshCards({ silent: true }));
  }, [activeBoard, refreshCards]);

  // BACI-334 "Save + Auto" — the editor's second save: persist the re-ordered
  // tail then turn Auto on. Returns the promise (process before Auto, same as
  // setCardProcessAuto) so ProcessEditor keeps its navigate-back / error-banner
  // contract; the engine-mode call rides inside that promise so a failure in
  // either step surfaces in the editor's own banner.
  const editCardProcessTailAuto = useCallback((key, stages) => {
    return api.editCardProcessTail(activeBoard, key, stages)
      .then(() => api.setEngineMode(activeBoard, key, 'auto'))
      .then(() => refreshCards({ silent: true }));
  }, [activeBoard, refreshCards]);

  // Manual Start — advance one step (start the next pending job, or run
  // the Ship hand-off when the chain ends in one).
  const startCardJob = useCallback((key) => {
    api.startCardJob(activeBoard, key)
      .then(() => refreshCards({ silent: true }))
      .catch(err => reportError(err, { headline: "Couldn't start the job" }));
  }, [activeBoard, refreshCards]);

  // Manual Stop — cancel the running job and halt Auto.
  const stopCardJob = useCallback((key) => {
    api.stopCardJob(activeBoard, key)
      .then(() => refreshCards({ silent: true }))
      .catch(err => reportError(err, { headline: "Couldn't stop the job" }));
  }, [activeBoard, refreshCards]);

  // Reset the process (BACI-314) — wipe the card's whole job chain so it
  // drops back to the from-scratch "Pick a process" picker. The card's
  // in-card confirm gates this; the engine refuses while a job is running
  // (the card hides Reset in that state). Refreshes so the emptied chain
  // re-renders as the fresh picker.
  const resetCardProcess = useCallback((key) => {
    api.resetCardProcess(activeBoard, key)
      .then(() => refreshCards({ silent: true }))
      .catch(err => reportError(err, { headline: "Couldn't reset the process" }));
  }, [activeBoard, refreshCards]);

  // Per-job Re-run — reset an aborted (stopped) step to pending and
  // re-dispatch it (BACI-291). seq is the job's 1-based sequence.
  const rerunCardJob = useCallback((key, seq) => {
    api.rerunCardJob(activeBoard, key, seq)
      .then(() => refreshCards({ silent: true }))
      .catch(err => reportError(err, { headline: "Couldn't re-run the job" }));
  }, [activeBoard, refreshCards]);

  // Engine drive-mode toggle ("off" | "auto"). Optimistic flip on the
  // card so the switch reacts on click; the refresh re-asserts.
  const setCardEngineMode = useCallback((key, mode) => {
    setCards(cs => cs.map(c => c.key === key ? { ...c, engineMode: mode } : c));
    api.setEngineMode(activeBoard, key, mode)
      .then(() => refreshCards({ silent: true }))
      .catch(err => {
        reportError(err, { headline: "Couldn't change the drive mode" });
        refreshCards({ silent: true });
      });
  }, [activeBoard, refreshCards]);

  // Fast-track (BACI-311) — collapse the manual drag → pick-process →
  // toggle-Auto flow into one click on a Backlog card. Runs the three
  // existing api.* calls strictly in order, awaiting each: move must land
  // before process (process applies to the in_pipeline card), process
  // before Auto (Auto on a chain-less card would drive nothing). On a
  // mid-chain failure the steps before it already persisted and the ones
  // after never ran, so the `finally` refresh leaves the board in a
  // coherent partial state the user can complete by hand.
  const fastTrackCard = useCallback(async (key) => {
    try {
      await api.setIssueState(activeBoard, key, 'in_pipeline');
      await api.setCardProcess(activeBoard, key, { process: 'plan-implement-ship' });
      await api.setEngineMode(activeBoard, key, 'auto');
    } catch (err) {
      reportError(err, { headline: "Couldn't fast-track the card" });
    } finally {
      refreshCards({ silent: true });
    }
  }, [activeBoard, refreshCards]);

  // Ship hand-off — move an in_pipeline card to to_be_shipped (no agent
  // dispatched here; the ship agent fires from the Shipping column).
  // Optimistic column move mirrors moveCard.
  const shipCardFromPipeline = useCallback((key) => {
    let prevCol = null;
    setCards(cs => cs.map(c => {
      if (c.key !== key) return c;
      prevCol = c.column;
      return { ...c, column: 'to_be_shipped' };
    }));
    api.shipCard(activeBoard, key)
      .then(() => refreshCards({ silent: true }))
      .catch(err => {
        reportError(err, { headline: "Couldn't ship the card" });
        if (prevCol) setCards(cs => cs.map(c => c.key === key ? { ...c, column: prevCol } : c));
      });
  }, [activeBoard, refreshCards]);

  // BACI-268: trash-bin drag-to-cancel. Routes a card dropped onto the
  // Pipeline's trash bin through the terminal-move path — moveCard already
  // handles the optimistic column flip (the card leaves its column),
  // `cancelled`-as-terminal blocker-strip, persistence, and rollback.
  const cancelCardFromPipeline = useCallback((key) => {
    moveCard(key, 'cancelled');
  }, [moveCard]);

  // BACI-330: drag-to-done zone. Twin of cancelCardFromPipeline — routes a
  // card dropped onto the Done zone through the same terminal-move path
  // (`done` is already in TERMINAL_STATES), so the optimistic column flip,
  // blocker-strip, persistence, and rollback all come for free.
  const doneCardFromPipeline = useCallback((key) => {
    moveCard(key, 'done');
  }, [moveCard]);

  // Backlog / Shipping drag-to-reorder. position is 1-based within the
  // card's (repo, state) band. PipelineView handles the optimistic
  // in-list move during the drag; this persists + reconciles.
  const reorderPipelineCard = useCallback((key, position) => {
    api.reorderCard(activeBoard, key, position)
      .then(() => refreshCards({ silent: true }))
      .catch(err => {
        reportError(err, { headline: "Couldn't reorder" });
        refreshCards({ silent: true });
      });
  }, [activeBoard, refreshCards]);

  // Per-repo Shipping auto-ship toggle. PipelineView owns the display
  // state (seeded from localStorage — the backend exposes no GET); this
  // persists the change. Returns the promise so the view can revert its
  // optimistic flip on failure.
  const setRepoAutoShip = useCallback((enabled) => {
    return api.setAutoShip(activeBoard, enabled)
      .catch(err => { reportError(err, { headline: "Couldn't toggle auto-ship" }); throw err; });
  }, [activeBoard]);

  // Per-repo Pipeline Backlog-column collapse preference (BACI-288).
  // PipelineView owns the display state (seeded from the backend GET);
  // this persists the change. Returns the promise so the view can revert
  // its optimistic flip on failure.
  const setBacklogCollapsed = useCallback((collapsed) => {
    return api.setBacklogCollapsed(activeBoard, collapsed)
      .catch(err => { reportError(err, { headline: "Couldn't collapse backlog" }); throw err; });
  }, [activeBoard]);

  // Workspace write callbacks — each wraps the existing api.* call and
  // refreshes the brief so the inline view re-renders with the
  // persisted state. Failures surface through reportError; the
  // workspace components catch their own setBusy flags.
  const saveDescription = useCallback(async (description) => {
    if (!openIssueKey) return;
    try {
      await api.updateIssueDescription(activeBoard, openIssueKey, description);
      refreshBrief();
      // The Board card carries no description column, but the card list
      // still benefits from a refresh so any concurrent state changes
      // land on screen.
      refreshCards({ silent: true });
    } catch (err) {
      reportError(err, { headline: "Couldn't save description" });
      throw err;
    }
  }, [activeBoard, openIssueKey, refreshBrief, refreshCards]);

  // BACI-292: title editing from the workspace header. The card list does
  // carry the title, so the cards refresh matters here — the kanban card
  // re-renders with the new title without waiting for the 10s poll.
  const saveTitle = useCallback(async (title) => {
    if (!openIssueKey) return;
    try {
      await api.updateIssueTitle(activeBoard, openIssueKey, title);
      refreshBrief();
      refreshCards({ silent: true });
    } catch (err) {
      reportError(err, { headline: "Couldn't save title" });
      throw err;
    }
  }, [activeBoard, openIssueKey, refreshBrief, refreshCards]);

  // BACI-141: opts is an optional third arg carrying the eval flag and
  // the transcript_event_ref the per-event composer in the transcript
  // viewer fills in. The existing CommentComposer path keeps passing
  // (author, body) and lands the comment as a plain row.
  const addComment = useCallback(async (author, body, opts) => {
    if (!openIssueKey) return;
    try {
      await api.addComment(activeBoard, openIssueKey, author, body, opts);
      refreshBrief();
    } catch (err) {
      reportError(err, { headline: "Couldn't add comment" });
      throw err;
    }
  }, [activeBoard, openIssueKey, refreshBrief]);

  const deleteComment = useCallback(async (commentUUID) => {
    if (!openIssueKey || !commentUUID) return;
    try {
      await api.deleteComment(activeBoard, openIssueKey, commentUUID);
      refreshBrief();
    } catch (err) {
      reportError(err, { headline: "Couldn't delete comment" });
      throw err;
    }
  }, [activeBoard, openIssueKey, refreshBrief]);

  const attachPR = useCallback(async (url) => {
    if (!openIssueKey) return;
    await api.attachPullRequest(activeBoard, openIssueKey, url);
    refreshBrief();
  }, [activeBoard, openIssueKey, refreshBrief]);

  // BACI-74: derive available/busy counts from the existing 10s-polled
  // agents array so the top-nav Agents button can show two small numeric
  // counters when the active repo has any non-ended sessions. Source of
  // truth + derivation rules: BACI-74's design notes — `status: ended`
  // is excluded; `busy` (which already folds `waiting` in) decides the
  // busy bucket; available = non-ended - busy.
  const agentCounts = React.useMemo(() => {
    let available = 0;
    let busy = 0;
    for (const a of agents) {
      if (a.status === 'ended') continue;
      if (a.busy) busy++;
      else available++;
    }
    return { available, busy };
  }, [agents]);

  // shippedScope (BACI-221) is the active Today / Last Week / Forever
  // window for the Pipeline Shipping-column Shipped pill + its popover. Seeded from
  // localStorage on mount so a relaunch lands on the last-picked
  // scope; the picker lives inside ShippedPopover and re-writes on
  // every click via the onScopeChange callback below.
  const [shippedScope, setShippedScope] = useState(readShippedScope);
  const changeShippedScope = useCallback((next) => {
    setShippedScope(next);
    persistShippedScope(next);
  }, []);

  // shippedCount (BACI-187, server-derived for BACI-221) feeds the
  // Pipeline Shipping-column "Shipped · N" pill. Polled on the standard POLL_INTERVAL_MS
  // cadence — mirrors the leader / agents polls so the chip number
  // stays roughly in lockstep with the rest of the live readouts.
  //
  // The pre-BACI-221 count derived this from the polled `cards` array
  // client-side, which (a) undercounted because cards are filtered by
  // show_archived + the per-feature board-hide set, and (b) couldn't
  // represent "Forever" at all. Moving to server-side counting fixes
  // both and lets the count change with the scope picker.
  const [shippedCount, setShippedCount] = useState(0);
  // BACI-295: previous-count ref the count-rise SFX watch (below) diffs
  // against, so a genuine rise can be told apart from a first-load /
  // navigation refill. The scope/repo-change effect blanks `shippedCount`
  // to the `null` loading sentinel rather than poking this ref directly —
  // that null is what makes the watch snap (not ding) on the refill.
  const prevShippedCountRef = useRef(null);
  // refreshShippedCount (BACI-276): the single fetch the poll effect and
  // the on-ship trigger both invoke. Stable across renders so the
  // ship-flourish callback can call it without re-running its own
  // detection effect. Re-derived only when the board or scope changes.
  const refreshShippedCount = useCallback(() => {
    if (!activeBoard || activeBoard === 'all') {
      setShippedCount(0);
      return;
    }
    // BACI-312: 'today' now resolves to an absolute local-midnight cutoff
    // in the user's timezone (sinceTs); week/forever keep the relative
    // sinceDays window.
    const { sinceDays, sinceTs } = scopeSinceParams(shippedScope, timezone);
    api.countShippedIssues(activeBoard, sinceDays, sinceTs)
      .then((n) => setShippedCount(n))
      .catch(() => { /* pill is best-effort; the popover surfaces failures */ });
  }, [activeBoard, shippedScope, timezone]);
  useEffect(() => {
    if (!activeBoard || activeBoard === 'all') {
      setShippedCount(0);
      return;
    }
    // Blank the count to `null` (not 0) on scope / repo change so a
    // stale number doesn't sit on the pill while the first fetch is in
    // flight, AND so the post-navigation refill never dings. `null` is
    // the loading sentinel the SFX watch below treats as a snap on both
    // sides: decideOdometerAction(prev, null) snaps, then
    // decideOdometerAction(null, fetched) snaps, so the refill reads as
    // a fresh first-load — not a 0 -> N rise. (BACI-295 tried to do this
    // with `setShippedCount(0); prevShippedCountRef.current = null`, but
    // the intermediate 0 commit consumed the prev=null snap, leaving the
    // real value to read as a rise from 0 — so it actually rang on every
    // first-load / scope / repo change. The null sentinel fixes that.)
    setShippedCount(null);
    refreshShippedCount();
    const id = setInterval(refreshShippedCount, POLL_INTERVAL_MS);
    return () => { clearInterval(id); };
  }, [activeBoard, shippedScope, refreshShippedCount]);

  // BACI-287: notification-bell unread count. Global / cross-repo (the
  // bell lists notifications from every repo), polled on the standard
  // POLL_INTERVAL_MS cadence like the shipped count / leader / agents
  // readouts so the badge stays roughly live without the dropdown being
  // open. The bell also calls setNotifUnreadCount directly after a
  // mark-read / mark-all so the badge updates instantly.
  const [notifUnreadCount, setNotifUnreadCount] = useState(0);
  const refreshNotifCount = useCallback(() => {
    api.countUnreadNotifications()
      .then((n) => setNotifUnreadCount(n))
      .catch(() => { /* badge is best-effort; the dropdown surfaces failures */ });
  }, []);
  useEffect(() => {
    refreshNotifCount();
    const id = setInterval(refreshNotifCount, POLL_INTERVAL_MS);
    return () => { clearInterval(id); };
  }, [refreshNotifCount]);

  // BACI-287: deep-link a notification row to its issue. Cross-repo — the
  // row carries its own prefix, which may differ from the active board, so
  // navigate to that prefix's workspace route directly.
  const openNotificationIssue = useCallback((prefix, key) => {
    if (!prefix || !key) return;
    navigate(issuePath(prefix, key));
  }, [navigate]);

  // BACI-240 ka-ching SFX. Hoisted out of ShippedPopover so the
  // audio fires regardless of the active view (BACI-254). The hook
  // returns a stable `play` reference; the gating (enabled flag,
  // autoplay-policy lock) lives inside the hook so every caller stays
  // oblivious to those branches.
  const { play: playShipSfx } = useShipSfx({ enabled: audioEnabled });

  // BACI-295: tie the ka-ching to the same signal that rolls the
  // odometer — the server-derived `shippedCount` rising — rather than
  // the local `cards`-array → done diff it used to ride. The count is a
  // COUNT(*) over every done issue in scope, so it catches ships the
  // cards diff never observes (already done at first poll, filtered out
  // by show-archived / per-feature board-hide, shipped by a background
  // agent or another machine, or gone from the loaded set on ship).
  // Reuse the odometer's pure "did the count genuinely rise" decision
  // so the sound observes exactly what the user sees roll up.
  //
  // A ref (not state) holds the previous count so the watch doesn't
  // itself trigger a render. First mount snaps (prev null → no sound)
  // the same way the odometer does, so an initial non-zero count on
  // load doesn't ding. A +N burst rolls the odometer once to the new
  // value and so plays a single ka-ching — intentionally one-per-roll,
  // not one-per-card (the pre-BACI-295 wiring played N times); the
  // sound matches the number rolling once, not each card.
  //
  // No false ding on navigation: the scope/repo-change effect above
  // blanks `shippedCount` to the `null` loading sentinel before the
  // refetch. decideOdometerAction snaps on a null — both as `next` (the
  // blanked value) and, once stored here, as `prev` (the fetched value) —
  // so the refill after a repo/scope switch reads as a fresh first-load
  // snap, not a rise. No false ding when you just switched repo or scope.
  useEffect(() => {
    const action = decideOdometerAction(prevShippedCountRef.current, shippedCount);
    if (action.kind === 'roll') playShipSfx();
    prevShippedCountRef.current = shippedCount;
  }, [shippedCount, playShipSfx]);

  // BACI-254 / BACI-276 callback for locally-observed ships.
  // `useShipFlourish` fires `onShip(keys)` for every card that
  // transitioned into done in this tick. The audio no longer rides
  // this path (BACI-295 moved it onto the count-rise effect above);
  // the callback's remaining job is to bump the Pipeline odometer the
  // moment a card lands in done rather than waiting for the next 10s
  // poll, so the count rolls — and the sound fires — in lockstep with
  // the glow. One refresh per tick is enough: the count is a server
  // total, not a per-key delta.
  const onCardsShipped = useCallback(() => {
    refreshShippedCount();
  }, [refreshShippedCount]);

  // BACI-193 ship flourish: detect cards that just transitioned into
  // `done` from a non-terminal column, expose the flying key + flash
  // signal to PipelineView / ShippedPopover. The hook diffs the `cards`
  // array against its own internal previous-columns snapshot so the
  // poll-driven re-render is the only thing it needs. The `onShip`
  // callback now only drives the prompt count refresh (BACI-295 moved
  // the SFX onto the count-rise effect) — see onCardsShipped above.
  const { flyingKey: flyingShipKey, flashing: shipFlashing, onFlightDone: onShipFlightDone } = useShipFlourish(cards, { onShip: onCardsShipped });

  // BACI-203: navigate-by-key callback for prev/next sibling jumps and
  // the kanban blocked-popover link. Kept as a memoised callback so
  // KanbanCard / IssueWorkspace / RelationsPanel can drop it into
  // effect-dep arrays without thrashing.
  const navigateToIssue = useCallback((key) => {
    if (!key) return;
    navigate(issuePath(activeBoard, key));
  }, [navigate, activeBoard]);

  // BACI-285: repo switch — the active repo is URL-derived now, so
  // picking a repo routes to it. Land on the same page in the new repo
  // (the current view from viewFromPath); the trailing entity segment
  // on a detail route (`/BACI/issues/BACI-100`) is naturally dropped
  // because viewPath emits the list/page root only. Used by the Topbar
  // RepoPicker and the RepoNotFound screen. Falls back to the board
  // view ('board' → /issues) when the current path isn't a known nav
  // view (e.g. the repo-not-found screen itself).
  const pickBoard = useCallback((prefix) => {
    if (!prefix) return;
    const currentView = viewFromPath(location.pathname) || 'pipeline';
    navigate(viewPath(prefix, currentView));
  }, [navigate, location.pathname]);

  return (
    <TooltipProvider delayDuration={250} skipDelayDuration={150}>
    <LazyMotion features={domMax} strict>
    {/* BACI-268: tag the shell on the Pipeline route so the bottom-right
        Activity tray lifts above the drag-to-cancel bin (which only renders
        on this route); other routes keep the tray in the corner. */}
    <div className={`mk-app${activeView === 'pipeline' ? ' is-pipeline' : ''}`}>
      {/* BACI-193: wrap Topbar + main view in one LayoutGroup so the
          ship-flourish layoutId match can cross from the pipeline card
          to the Shipped pill destination in the Pipeline's Shipping
          column. Without the group, Motion namespaces layoutIds per
          subtree and the two ends never see each other. */}
      <LayoutGroup id="kanban">
      <Topbar
        boards={boards}
        activeBoard={activeBoard}
        onPickBoard={pickBoard}
        onAddRepository={addRepository}
        onBeforeNavigate={() => { setSettingsOpen(false); setSettingsInitialSection(null); }}
        onOpenSettings={() => { setSettingsInitialSection(null); setSettingsOpen(true); }}
        onOpenSync={openSync}
        leaderState={leaderState}
        agentCounts={agentCounts}
        notifUnreadCount={notifUnreadCount}
        onNotifCountChange={setNotifUnreadCount}
        onOpenNotificationIssue={openNotificationIssue}
        shippedCount={shippedCount}
        shippedScope={shippedScope}
        onShippedScopeChange={changeShippedScope}
        timezone={timezone}
        flyingShipKey={flyingShipKey}
        shipFlashing={shipFlashing}
        onShipFlightDone={onShipFlightDone}
        onOpenIssue={navigateToIssue}
      />
      {loading ? (
        <div className="mk-app-state">Loading…</div>
      ) : boards.length === 0 ? (
        // BACI-285: no repos registered yet — there's no prefix to route
        // to, so render an empty state (and skip the <Routes> block,
        // whose `/` redirect would otherwise loop on an empty fallback).
        // Add one via the topbar RepoPicker's "Add Repository" action.
        <div className="mk-app-state">No repositories yet — add one from the repo picker above.</div>
      ) : settingsOpen ? (
        <ErrorBoundary headline="Something went wrong in Settings" label="The Settings view crashed">
          <SettingsView
            theme={theme}
            onChangeTheme={setTheme}
            showArchived={showArchived}
            onChangeShowArchived={changeShowArchived}
            archiveAutoEnabled={archiveAutoEnabled}
            archiveRetentionDays={archiveRetentionDays}
            onChangeArchivePreferences={changeArchivePreferences}
            audioEnabled={audioEnabled}
            onChangeAudioEnabled={changeAudioEnabled}
            timezone={timezone}
            onChangeTimezone={changeTimezone}
            columns={columns}
            onClose={closeSettings}
            onTemplatesChanged={refreshPromptConfig}
            repoPrefix={activeBoard}
            boards={boards}
            initialSection={settingsInitialSection}
          />
        </ErrorBoundary>
      ) : prefixUnknown ? (
        // BACI-285: the URL's first segment is a genuinely-unknown repo
        // prefix (not a known board, not a recognised legacy page word).
        // Render the hard-404 screen rather than stranding the user on a
        // blank board keyed off an empty repo. Legacy page links
        // (`/ui/pipeline`, ...) flow into the <Routes> branch below and
        // soft-redirect through its `*` route instead.
        <RepoNotFound prefix={urlPrefix} boards={boards} onPickBoard={pickBoard} />
      ) : (
        <Routes>
          {/* BACI-285: every page route is scoped to the active repo's
              prefix (`/<PREFIX>/<page>`). App reads the prefix off
              location.pathname (activeBoard) rather than useParams, so
              the page elements below don't need the route param — the
              `:prefix` segment exists only to anchor the route tree. */}
          {/* Bare `/` and any prefix-less / stale path redirect to the
              fallback repo's Pipeline so refreshes / legacy links don't
              strand the user. The Pipeline is the only driving surface
              (the issues board was removed in the Phase 5 cutover). */}
          <Route path="/" element={<Navigate to={legacyRedirectTarget} replace />} />
          <Route
            path="/:prefix/pipeline"
            element={
              <ErrorBoundary headline="Something went wrong in Pipeline" label="The Pipeline view crashed">
                <PipelineView
                  cards={cards}
                  activeBoard={activeBoard}
                  promptConfig={promptConfig}
                  onOpenComposer={() => setComposerOpen(true)}
                  onOpenCard={openCard}
                  onOpenIssue={navigateToIssue}
                  onMoveCard={moveCard}
                  onFastTrack={fastTrackCard}
                  onCancelCard={cancelCardFromPipeline}
                  onDoneCard={doneCardFromPipeline}
                  onReorder={reorderPipelineCard}
                  onSetProcess={setCardProcess}
                  onSetProcessAuto={setCardProcessAuto}
                  onResetProcess={resetCardProcess}
                  onEditProcess={(key) => navigate(processEditPath(activeBoard, key))}
                  onStartJob={startCardJob}
                  onStopJob={stopCardJob}
                  onRerunJob={rerunCardJob}
                  onSetEngineMode={setCardEngineMode}
                  onShip={shipCardFromPipeline}
                  onSetAutoShip={setRepoAutoShip}
                  onSetBacklogCollapsed={setBacklogCollapsed}
                  onShipDispatch={dispatchFromCard}
                  onCancelWaiting={cancelWaitingFromCard}
                />
              </ErrorBoundary>
            }
          />
          {/* BACI-294: the full-screen Edit Process editor for an
              in_pipeline card's job chain. Reads the card's live chain off
              the cached `cards`; Save persists the re-ordered pending tail
              (locked prefix preserved server-side) and returns to the
              Pipeline. */}
          <Route
            path="/:prefix/pipeline/:key/process"
            element={
              <ErrorBoundary headline="Something went wrong in Edit Process" label="The Edit Process view crashed">
                <ProcessEditor
                  cards={cards}
                  activeBoard={activeBoard}
                  onSave={editCardProcessTail}
                  onSaveAuto={editCardProcessTailAuto}
                />
              </ErrorBoundary>
            }
          />
          {/* Phase 5 cutover: the /issues board LIST route is removed —
              the Pipeline is the only board surface now. The
              /:prefix/issues/:key workspace route below stays as the
              view/edit home for a single issue. */}
          {/* Inline the element rather than wrapping it in a component
              declared inside App: a nested function component would have a
              fresh identity on every App render, and react-router would
              unmount → remount the entire workspace subtree on each render
              (e.g. the 10s brief poll), wiping scroll position and any
              transient IssueWorkspace state. openIssueKey is already
              derived from location.pathname above, so the useParams
              adapter is unnecessary. */}
          <Route
            path="/:prefix/issues/:key"
            element={
              <ErrorBoundary headline="Something went wrong in the issue view" label="The issue view crashed">
                <IssueWorkspace
                  activeBoard={activeBoard}
                  openIssueKey={openIssueKey}
                  brief={openIssueBrief}
                  cards={cards}
                  onClose={closeIssue}
                  onSaveTitle={saveTitle}
                  onSaveDescription={saveDescription}
                  onAddComment={addComment}
                  onDeleteComment={deleteComment}
                  onCancelWaiting={() => openIssueKey && cancelWaitingFromCard(openIssueKey)}
                  onAttachPR={attachPR}
                  onNavigateIssue={navigateToIssue}
                  onDescEditingChange={setDescEditing}
                />
              </ErrorBoundary>
            }
          />
          <Route
            path="/:prefix/features"
            element={
              <ErrorBoundary headline="Something went wrong in Features" label="The Features view crashed">
                {/* BACI-177: refresh the cached board cards when the user
                    flips the per-feature "Show on board" toggle so the
                    change is visible on the next nav back to the board
                    without waiting for the 10s poll. */}
                <FeaturesView activeBoard={activeBoard} onChangeHidden={refreshCards} />
              </ErrorBoundary>
            }
          />
          <Route
            path="/:prefix/features/:slug"
            element={
              <ErrorBoundary headline="Something went wrong in Features" label="The Features view crashed">
                <FeaturesView activeBoard={activeBoard} onChangeHidden={refreshCards} />
              </ErrorBoundary>
            }
          />
          <Route
            path="/:prefix/documents"
            element={
              <ErrorBoundary headline="Something went wrong in Docs" label="The Docs view crashed">
                <DocsView activeBoard={activeBoard} onOpenIssue={navigateToIssue} />
              </ErrorBoundary>
            }
          />
          <Route
            path="/:prefix/documents/:slug"
            element={
              <ErrorBoundary headline="Something went wrong in Docs" label="The Docs view crashed">
                <DocsView activeBoard={activeBoard} onOpenIssue={navigateToIssue} />
              </ErrorBoundary>
            }
          />
          <Route
            path="/:prefix/agents"
            element={
              <ErrorBoundary headline="Something went wrong in Agents" label="The Agents view crashed">
                <AgentsView agents={agents} onRefresh={refreshAgents} />
              </ErrorBoundary>
            }
          />
          <Route
            path="/:prefix/history"
            element={
              <ErrorBoundary headline="Something went wrong in History" label="The History view crashed">
                <HistoryView activeBoard={activeBoard} />
              </ErrorBoundary>
            }
          />
          <Route
            path="/:prefix/monitor"
            element={
              <ErrorBoundary headline="Something went wrong in Monitor" label="The Monitor view crashed">
                <MonitorView activeBoard={activeBoard} />
              </ErrorBoundary>
            }
          />
          {/* BACI-322: the Transcripts sub-tab is the same Monitor shell — it
              derives the active sub-tab from the URL, so the segmented control
              and the deep-link / refresh land on Transcripts. */}
          <Route
            path="/:prefix/monitor/transcripts"
            element={
              <ErrorBoundary headline="Something went wrong in Monitor" label="The Monitor view crashed">
                <MonitorView activeBoard={activeBoard} />
              </ErrorBoundary>
            }
          />
          {/* BACI-322: the deep-linkable full-transcript page for one dispatch.
              react-router v7 ranks this more-specific path above /:prefix/monitor
              and the /:prefix/* catch-all, and the SPA fallback serves index.html
              for it on both transports, so a cold refresh resolves it. */}
          <Route
            path="/:prefix/monitor/transcript/:id"
            element={
              <ErrorBoundary headline="Something went wrong in the transcript view" label="The transcript view crashed">
                <TranscriptRoute />
              </ErrorBoundary>
            }
          />
          {/* Catch-all: an unknown page under a *valid* prefix lands on
              that repo's Pipeline; a prefix-less / stale single-segment
              legacy path (e.g. `/pipeline`, which react-router matches
              here as `:prefix=pipeline` with an empty splat) has an
              empty activeBoard, so it falls through to the legacy
              redirect instead of building a `//pipeline` path. Both
              shapes resolve here because `/:prefix/*` outranks a bare
              `*` in react-router and would otherwise swallow the legacy
              case. */}
          <Route
            path="/:prefix/*"
            element={
              <Navigate
                to={activeBoard ? viewPath(activeBoard, 'pipeline') : legacyRedirectTarget}
                replace
              />
            }
          />
          <Route path="*" element={<Navigate to={legacyRedirectTarget} replace />} />
        </Routes>
      )}
      <ErrorBoundary headline="Something went wrong in the command palette" label="The command palette crashed">
        <CommandPalette
          open={paletteOpen}
          cards={cards}
          onClose={() => setPaletteOpen(false)}
          onPick={openCard}
        />
      </ErrorBoundary>
      {/* BACI-166: + from prompt composer. Sibling of CommandPalette /
          ErrorModal so it overlays whatever view is current. */}
      <ErrorBoundary headline="Something went wrong in the issue composer" label="The issue composer crashed">
        <IssueComposer
          open={composerOpen}
          onClose={() => setComposerOpen(false)}
          repoPrefix={activeBoard}
          onCreated={onComposerCreated}
        />
      </ErrorBoundary>
      <ErrorModal />
      </LayoutGroup>
    </div>
    </LazyMotion>
    </TooltipProvider>
  );
}
