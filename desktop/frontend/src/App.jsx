import React, { useState, useEffect, useCallback, useRef } from 'react';
import { Routes, Route, Navigate, useNavigate, useLocation } from 'react-router';
import { Events } from '@wailsio/runtime';
import Topbar, { NAV } from './components/Topbar.jsx';
import DocsView from './components/DocsView.jsx';
import FeaturesView from './components/FeaturesView.jsx';
import AgentsView from './components/AgentsView.jsx';
import HistoryView from './components/HistoryView.jsx';
import PipelineView from './components/PipelineView.jsx';
import IssueWorkspace from './components/IssueWorkspace.jsx';
import CommandPalette from './components/CommandPalette.jsx';
import IssueComposer from './components/IssueComposer.jsx';
import { readShippedScope, persistShippedScope } from './components/shippedScopePersistence.ts';
import { scopeSinceDays } from './components/shippedScope.ts';
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
import { viewPath, issuePath, viewFromPath } from './lib/routes';

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
  // The selected repo prefix. Starts from the persisted preference (or "" on
  // first run / before the repo list resolves); the mount effect lands it on
  // a real repo once boards load.
  const [activeBoard, setActiveBoard] = useState(readActiveRepo);
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
  // chains api.addIssue → api.dispatchIssue(_, _, 'scope') in one click
  // so a rough one-liner becomes a triage-ready ticket in the background.
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
  // BACI-240: ui.shipped_sfx toggle — when true, the topbar Shipped
  // pill plays a short ka-ching SFX on every genuine ship (the
  // odometer rolling into a new value). Default false. Loaded on
  // mount alongside the other display prefs; flipped from Settings.
  const [audioEnabled, setAudioEnabled] = useState(false);
  // leaderState tracks the UI leader-election result from LeaderService.
  // amLeader = true means this desktop process holds the lease and may
  // dispatch. Standby processes show a chip and disable the per-card button.
  const [leaderState, setLeaderState] = useState({ amLeader: false, holderLabel: '' });
  const [theme, setTheme] = useState(readTheme);
  const [loading, setLoading] = useState(true);

  // BACI-203: derive the active view from the URL path. The Topbar
  // reads the same value (via useLocation()), so the App and the
  // Topbar stay in lockstep without a prop ping-pong.
  const activeView = viewFromPath(location.pathname) || 'board';
  // BACI-203: derive the open issue key from the route. Matches the
  // `/issues/:key` URL shape; null when we're on a list or detail
  // route that isn't an issue workspace.
  const openIssueKey = (() => {
    const m = location.pathname.match(/^\/issues\/([^/]+)$/);
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
  // Once boards resolve, land activeBoard on the persisted repo if it
  // still exists, otherwise the first repo — every screen needs a
  // concrete repo, there's no "all" option. The prompt config is global
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
    ])
      .then(([bs, cols, tpls, displayPrefs, archivePrefs, audioPrefs]) => {
        setBoards(bs);
        setColumns(cols);
        setPromptConfig(tpls);
        setShowArchived(displayPrefs.showArchived);
        setArchiveAutoEnabled(archivePrefs.autoEnabled);
        setArchiveRetentionDays(archivePrefs.retentionDays);
        setAudioEnabled(audioPrefs.shippedSfx);
        setActiveBoard(prev => bs.some(b => b.prefix === prev) ? prev : (bs[0]?.prefix ?? ''));
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
  // this was wired through ShippedPopover; the SFX is now a sibling
  // of useShipFlourish in App.jsx so it fires regardless of view.
  const changeAudioEnabled = useCallback((next) => {
    api.setAudioPreferences(next)
      .then(prefs => setAudioEnabled(prefs.shippedSfx))
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
    navigate(issuePath(card.key));
  }, [navigate]);

  // BACI-114: the kanban blocked popover navigates by key — the
  // target may not be the card the popover is rendered on (it's the
  // blocker, not this card). Same effect as openCard but takes a key
  // directly, mirroring the `onNavigateIssue` path the workspace rail
  // already uses for prev/next sibling jumps.
  const openIssueByKey = useCallback((key) => {
    if (!key) return;
    setSettingsOpen(false);
    setSettingsInitialSection(null);
    navigate(issuePath(key));
  }, [navigate]);

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
      navigate(viewPath('pipeline'));
    }
  }, [navigate]);

  // BACI-166: composer success handler — optimistically prepend the new
  // card with a queued_no_agent waitingState in the 'scope' mode so the
  // breathing waiting border + "Worker has the Scope job" pill render
  // immediately, route to the new issue's workspace, and bump the
  // cards-refresh poll so the authoritative row replaces the optimistic
  // one as soon as the server has it. Mirrors dispatchFromCard's
  // optimistic-waitingState shape exactly.
  const onComposerCreated = useCallback((newCard) => {
    if (!newCard || !newCard.key) return;
    setCards(cs => [
      { ...newCard, waitingState: { kind: 'queued_no_agent', mode: 'scope' } },
      ...cs,
    ]);
    setSettingsOpen(false);
    setSettingsInitialSection(null);
    navigate(issuePath(newCard.key));
    // Don't fire refreshCards synchronously — the dispatch is queued
    // *after* this callback returns (the composer awaits it post-route),
    // so an immediate refetch could race past it. The standing 10s poll
    // catches the authoritative shape on its next tick.
  }, [navigate]);

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
        // unless the user is typing into a field or the doc editor.
        if (isEditingTarget(e.target)) return;
        const idx = Number(e.key) - 1;
        if (idx < NAV.length) {
          navigate(viewPath(NAV[idx].view));
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
          setActiveBoard(board.prefix);
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
  // are gone. The underlying api.* verbs (dispatchIssueChain,
  // queue/cancelFollowOnDispatch) stay for now — Phase 6 decides retire vs
  // keep.

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

  // Assign a preset process to a card — the in-pipeline "pick a process"
  // menu. Refreshes so the new pending job chain renders on the card.
  const setCardProcess = useCallback((key, processSlug) => {
    api.setCardProcess(activeBoard, key, processSlug)
      .then(() => refreshCards({ silent: true }))
      .catch(err => reportError(err, { headline: "Couldn't set the process" }));
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
  // window for the topbar Shipped pill + its popover. Seeded from
  // localStorage on mount so a relaunch lands on the last-picked
  // scope; the picker lives inside ShippedPopover and re-writes on
  // every click via the onScopeChange callback below.
  const [shippedScope, setShippedScope] = useState(readShippedScope);
  const changeShippedScope = useCallback((next) => {
    setShippedScope(next);
    persistShippedScope(next);
  }, []);

  // shippedCount (BACI-187, server-derived for BACI-221) feeds the
  // topbar "Shipped · N" pill. Polled on the standard POLL_INTERVAL_MS
  // cadence — mirrors the leader / agents polls so the chip number
  // stays roughly in lockstep with the rest of the live readouts.
  //
  // The pre-BACI-221 count derived this from the polled `cards` array
  // client-side, which (a) undercounted because cards are filtered by
  // show_archived + the per-feature board-hide set, and (b) couldn't
  // represent "Forever" at all. Moving to server-side counting fixes
  // both and lets the count change with the scope picker.
  const [shippedCount, setShippedCount] = useState(0);
  // refreshShippedCount (BACI-276): the single fetch the poll effect and
  // the on-ship trigger both invoke. Stable across renders so the
  // ship-flourish callback can call it without re-running its own
  // detection effect. Re-derived only when the board or scope changes.
  const refreshShippedCount = useCallback(() => {
    if (!activeBoard || activeBoard === 'all') {
      setShippedCount(0);
      return;
    }
    const sinceDays = scopeSinceDays(shippedScope);
    api.countShippedIssues(activeBoard, sinceDays)
      .then((n) => setShippedCount(n))
      .catch(() => { /* pill is best-effort; the popover surfaces failures */ });
  }, [activeBoard, shippedScope]);
  useEffect(() => {
    if (!activeBoard || activeBoard === 'all') {
      setShippedCount(0);
      return;
    }
    // Reset to 0 on scope / repo change so a stale count doesn't sit
    // on the chip while the first fetch is in flight.
    setShippedCount(0);
    refreshShippedCount();
    const id = setInterval(refreshShippedCount, POLL_INTERVAL_MS);
    return () => { clearInterval(id); };
  }, [activeBoard, shippedScope, refreshShippedCount]);

  // BACI-240 ka-ching SFX. Hoisted out of ShippedPopover so the
  // audio fires regardless of the active view (BACI-254). The hook
  // returns a stable `play` reference; the gating (enabled flag,
  // reduced-motion, autoplay-policy lock) lives inside the hook so
  // every caller stays oblivious to those branches.
  const { play: playShipSfx } = useShipSfx({ enabled: audioEnabled });

  // BACI-254: per-shipped-card callback. `useShipFlourish` fires
  // `onShip(keys)` with every card that transitioned into done in
  // this tick — possibly more than one. We play() once per key so a
  // burst ship lands every audio cue rather than the single-pick the
  // pre-BACI-254 wiring afforded. `playShipSfx` is stable from
  // useShipSfx, so this callback identity is too — keeps the
  // useShipFlourish detection effect's dep list quiet.
  const onCardsShipped = useCallback((keys) => {
    for (let i = 0; i < keys.length; i++) playShipSfx();
    // BACI-276: bump the Pipeline odometer the moment a card lands in
    // done rather than waiting for the next 10s poll, so the count rolls
    // in lockstep with the glow. One refresh per tick is enough — the
    // count is a server total, not a per-key delta.
    refreshShippedCount();
  }, [playShipSfx, refreshShippedCount]);

  // BACI-193 ship flourish: detect cards that just transitioned into
  // `done` from a non-terminal column, expose the flying key + flash
  // signal to Topbar / ShippedPopover. The hook diffs the `cards`
  // array against its own internal previous-columns snapshot so the
  // poll-driven re-render is the only thing it needs. The `onShip`
  // callback fan-out is the BACI-254 SFX trigger — see comment above.
  const { flyingKey: flyingShipKey, flashing: shipFlashing, onFlightDone: onShipFlightDone } = useShipFlourish(cards, { onShip: onCardsShipped });

  // BACI-203: navigate-by-key callback for prev/next sibling jumps and
  // the kanban blocked-popover link. Kept as a memoised callback so
  // KanbanCard / IssueWorkspace / RelationsPanel can drop it into
  // effect-dep arrays without thrashing.
  const navigateToIssue = useCallback((key) => {
    if (!key) return;
    navigate(issuePath(key));
  }, [navigate]);

  return (
    <TooltipProvider delayDuration={250} skipDelayDuration={150}>
    <LazyMotion features={domMax} strict>
    {/* BACI-268: tag the shell on the Pipeline route so the bottom-right
        Activity tray lifts above the drag-to-cancel bin (which only renders
        on this route); other routes keep the tray in the corner. */}
    <div className={`mk-app${activeView === 'pipeline' ? ' is-pipeline' : ''}`}>
      {/* BACI-193: wrap Topbar + main view in one LayoutGroup so the
          ship-flourish layoutId match can cross from the kanban card
          to the Shipped pill destination inside Topbar. Without the
          group, Motion namespaces layoutIds per subtree and the two
          ends never see each other. */}
      <LayoutGroup id="kanban">
      <Topbar
        boards={boards}
        activeBoard={activeBoard}
        onPickBoard={setActiveBoard}
        onAddRepository={addRepository}
        onBeforeNavigate={() => { setSettingsOpen(false); setSettingsInitialSection(null); }}
        onOpenPalette={() => setPaletteOpen(true)}
        onOpenSettings={() => { setSettingsInitialSection(null); setSettingsOpen(true); }}
        onOpenSync={openSync}
        onOpenComposer={() => setComposerOpen(true)}
        leaderState={leaderState}
        agentCounts={agentCounts}
      />
      {loading ? (
        <div className="mk-app-state">Loading…</div>
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
            columns={columns}
            onClose={closeSettings}
            onTemplatesChanged={refreshPromptConfig}
            repoPrefix={activeBoard}
            boards={boards}
            initialSection={settingsInitialSection}
          />
        </ErrorBoundary>
      ) : (
        <Routes>
          {/* Redirect / to /pipeline — the Pipeline is the only driving
              surface now (the issues board was removed in the Phase 5
              cutover). The /issues/:key workspace route stays. */}
          <Route path="/" element={<Navigate to={viewPath('pipeline')} replace />} />
          <Route
            path="/pipeline"
            element={
              <ErrorBoundary headline="Something went wrong in Pipeline" label="The Pipeline view crashed">
                <PipelineView
                  cards={cards}
                  activeBoard={activeBoard}
                  promptConfig={promptConfig}
                  onOpenCard={openCard}
                  onOpenIssue={navigateToIssue}
                  onMoveCard={moveCard}
                  onCancelCard={cancelCardFromPipeline}
                  onReorder={reorderPipelineCard}
                  onSetProcess={setCardProcess}
                  onStartJob={startCardJob}
                  onStopJob={stopCardJob}
                  onSetEngineMode={setCardEngineMode}
                  onShip={shipCardFromPipeline}
                  onSetAutoShip={setRepoAutoShip}
                  onShipDispatch={dispatchFromCard}
                  onCancelWaiting={cancelWaitingFromCard}
                  shippedCount={shippedCount}
                  shippedScope={shippedScope}
                  onShippedScopeChange={changeShippedScope}
                  flyingShipKey={flyingShipKey}
                  shipFlashing={shipFlashing}
                  onShipFlightDone={onShipFlightDone}
                />
              </ErrorBoundary>
            }
          />
          {/* Phase 5 cutover: the /issues board LIST route is removed —
              the Pipeline (/pipeline) is the only board surface now. The
              /issues/:key workspace route below stays as the view/edit
              home for a single issue. */}
          {/* Inline the element rather than wrapping it in a component
              declared inside App: a nested function component would have a
              fresh identity on every App render, and react-router would
              unmount → remount the entire workspace subtree on each render
              (e.g. the 10s brief poll), wiping scroll position and any
              transient IssueWorkspace state. openIssueKey is already
              derived from location.pathname above, so the useParams
              adapter is unnecessary. */}
          <Route
            path="/issues/:key"
            element={
              <ErrorBoundary headline="Something went wrong in the issue view" label="The issue view crashed">
                <IssueWorkspace
                  activeBoard={activeBoard}
                  openIssueKey={openIssueKey}
                  brief={openIssueBrief}
                  cards={cards}
                  onClose={closeIssue}
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
            path="/features"
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
            path="/features/:slug"
            element={
              <ErrorBoundary headline="Something went wrong in Features" label="The Features view crashed">
                <FeaturesView activeBoard={activeBoard} onChangeHidden={refreshCards} />
              </ErrorBoundary>
            }
          />
          <Route
            path="/documents"
            element={
              <ErrorBoundary headline="Something went wrong in Docs" label="The Docs view crashed">
                <DocsView activeBoard={activeBoard} onOpenIssue={navigateToIssue} />
              </ErrorBoundary>
            }
          />
          <Route
            path="/documents/:slug"
            element={
              <ErrorBoundary headline="Something went wrong in Docs" label="The Docs view crashed">
                <DocsView activeBoard={activeBoard} onOpenIssue={navigateToIssue} />
              </ErrorBoundary>
            }
          />
          <Route
            path="/agents"
            element={
              <ErrorBoundary headline="Something went wrong in Agents" label="The Agents view crashed">
                <AgentsView agents={agents} onRefresh={refreshAgents} />
              </ErrorBoundary>
            }
          />
          <Route
            path="/history"
            element={
              <ErrorBoundary headline="Something went wrong in History" label="The History view crashed">
                <HistoryView activeBoard={activeBoard} />
              </ErrorBoundary>
            }
          />
          {/* Unknown route lands on the Pipeline so refreshes / stray
              links don't strand the user on a 404 we don't render. */}
          <Route path="*" element={<Navigate to={viewPath('pipeline')} replace />} />
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
