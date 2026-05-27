import React, { useState, useEffect, useCallback, useRef } from 'react';
import { Routes, Route, Navigate, useNavigate, useLocation } from 'react-router';
import { Events } from '@wailsio/runtime';
import Topbar, { NAV } from './components/Topbar.jsx';
import Board from './components/Board.jsx';
import DocsView from './components/DocsView.jsx';
import FeaturesView from './components/FeaturesView.jsx';
import AgentsView from './components/AgentsView.jsx';
import HistoryView from './components/HistoryView.jsx';
import IssueWorkspace from './components/IssueWorkspace.jsx';
import CommandPalette from './components/CommandPalette.jsx';
import IssueComposer from './components/IssueComposer.jsx';
import ActivityTray from './components/ActivityTray.jsx';
import { readPinnedKeys, persistPinnedKeys } from './components/activityTrayPinPersistence';
import { readShippedScope, persistShippedScope } from './components/shippedScopePersistence.ts';
import { scopeSinceDays } from './components/shippedScope.ts';
import SettingsView from './components/SettingsView.jsx';
import SyncView from './components/SyncView.jsx';
import ErrorBoundary from './components/ErrorBoundary.jsx';
import ErrorModal from './components/ErrorModal.jsx';
import { Provider as TooltipProvider } from '@radix-ui/react-tooltip';
import { LazyMotion, domMax, LayoutGroup } from 'motion/react';
import { reportError } from './errors';
import { WEB_MODE } from './env';
import * as api from './api';
import { isTerminalState, stripBlockerFromCards, restoreBlockedByFromSnapshot } from './lib/issueState';
import { useShipFlourish } from './lib/shipFlourish';
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
  // BACI-108: the standalone Sync view is a sibling boolean flag to
  // settingsOpen — reached only via the topbar Sync pill, never the
  // top-nav. Mirrors the Settings entry-point shape.
  const [syncOpen, setSyncOpen] = useState(false);
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
  // BACI-241: the canonical state-transition graph (display hint, not
  // enforcement). Loaded once on mount alongside promptConfig — the
  // graph is a server-side constant so we never refresh. Threaded down
  // through Board to KanbanCard so the follow-on popup can promote /
  // demote / tuck-away modes whose allowedStates overlap with the
  // card's primary / secondary / unusual next-states. `null` until the
  // mount-time Promise.all resolves; the helper handles that as a
  // graceful fallback (every prompt under unusual).
  const [stateGraph, setStateGraph] = useState(null);
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
  // BACI-192: the per-repo set of issue keys the user has pinned to the
  // activity tray via the kanban card's top-right corner button. Lives
  // in App state so both the board (corner state on each card) and the
  // tray (PINNED section) re-render on toggle. Seeded from localStorage
  // on mount + on every repo switch — the persisted shape is per-repo
  // because an issue key is only meaningful inside its repo.
  const [pinnedKeys, setPinnedKeys] = useState(() => readPinnedKeys(readActiveRepo()));
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
      // BACI-241: state-transition graph — constant on the server side,
      // so we fetch once on mount and never refresh. Failure here is
      // non-fatal: the promotePrompts helper falls back to "every prompt
      // under unusual" when the graph is null, which renders identically
      // to the pre-BACI-241 flat menu.
      api.getStateGraph().catch(() => null),
    ])
      .then(([bs, cols, tpls, displayPrefs, archivePrefs, audioPrefs, graph]) => {
        setBoards(bs);
        setColumns(cols);
        setPromptConfig(tpls);
        setShowArchived(displayPrefs.showArchived);
        setArchiveAutoEnabled(archivePrefs.autoEnabled);
        setArchiveRetentionDays(archivePrefs.retentionDays);
        setAudioEnabled(audioPrefs.shippedSfx);
        setStateGraph(graph);
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
  // The ShippedPopover reads `audioEnabled` from props and gates its
  // `play()` call accordingly, so a flip surfaces immediately without
  // a re-fetch. Same optimistic-then-confirmed shape as the other
  // preference handlers.
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

  const closeSettings = useCallback(() => setSettingsOpen(false), []);
  const closeSync = useCallback(() => setSyncOpen(false), []);
  const openSync = useCallback(() => {
    // Mutually exclusive with Settings — both are top-level overlays
    // mounted in the same body slot, only one renders at a time.
    setSettingsOpen(false);
    setSyncOpen(true);
  }, []);

  // Remember the selected repo so the app reopens on the same one.
  useEffect(() => {
    if (activeBoard) persistActiveRepo(activeBoard);
  }, [activeBoard]);

  // BACI-192: re-seed the pinned set on every repo switch (the storage
  // shape is per-repo). The mount-time read in useState's lazy
  // initialiser already covers the first paint; this covers subsequent
  // active-board changes so the new repo's pins land immediately.
  useEffect(() => {
    setPinnedKeys(readPinnedKeys(activeBoard));
  }, [activeBoard]);

  // togglePinKey flips an issue key's membership in the per-repo
  // pinned set and persists. Threaded to KanbanCard's corner button
  // and to ActivityTray's dismiss-pinned-row path so both surfaces
  // converge on the same setter. Pure-client mutation — no network.
  const togglePinKey = useCallback((key) => {
    if (!key) return;
    setPinnedKeys(prev => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      persistPinnedKeys(activeBoard, next);
      return next;
    });
  }, [activeBoard]);

  // BACI-197: cross-link cues between the Activity tray and the kanban
  // board. `hoveredKey` is the tray row the cursor is currently over —
  // the matching board card lights up via the `is-tray-hover` class.
  // `jumpKey` is the card that just got clicked in the tray — it plays
  // a brief CSS keyframe ("jump") so the user can see which row they
  // hit. Both are ephemeral interaction state; no persistence. The
  // hover handler is just setHoveredKey; the jump handler runs through
  // triggerJump so it can clear automatically after the animation.
  const [hoveredKey, setHoveredKey] = useState(null);
  const [jumpKey, setJumpKey] = useState(null);
  // JUMP_MS mirrors --dur-slow (240ms) in app.css — kept in sync with
  // the `mk-card-jump` keyframe duration. The timer ref lets us cancel
  // a still-running jump if a second click lands so the next jump
  // restarts cleanly rather than half-animating.
  const jumpTimerRef = useRef(null);
  const triggerJump = useCallback((key) => {
    if (!key) return;
    if (jumpTimerRef.current) {
      clearTimeout(jumpTimerRef.current);
      jumpTimerRef.current = null;
    }
    // Drop to null first so a second click on the same key restarts
    // the CSS animation; React's diff would otherwise skip the class
    // toggle when jumpKey is already that key.
    setJumpKey(null);
    // requestAnimationFrame lets the null land in the DOM before the
    // new key flips back in — without it React can batch both setStates
    // into one render and the animation is a no-op.
    requestAnimationFrame(() => {
      setJumpKey(key);
      jumpTimerRef.current = setTimeout(() => {
        setJumpKey(null);
        jumpTimerRef.current = null;
      }, 240);
    });
  }, []);
  // Drain the jump timer on unmount so we don't setState into a dead
  // component if the user navigates away mid-animation.
  useEffect(() => {
    return () => {
      if (jumpTimerRef.current) {
        clearTimeout(jumpTimerRef.current);
        jumpTimerRef.current = null;
      }
    };
  }, []);

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
    if (activeView === 'board') refreshCards();
    else if (activeView === 'agents') refreshAgents();
  }, [activeView, refreshCards, refreshAgents]);

  // Poll the Board / Agents screens every 10s so they don't go stale while
  // open. The cleanup clears the interval on navigation away, repo change,
  // or unmount — no leaks, no redundant fetches off-screen.
  useEffect(() => {
    if (!activeBoard) return;
    if (activeView !== 'board' && activeView !== 'agents') return;
    const id = setInterval(() => {
      if (activeView === 'board') refreshCards({ silent: true });
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
  // SettingsView and SyncView are App-owned overlays, not routes —
  // dismiss them so the workspace is what the user sees on arrival.
  const openCard = useCallback((card) => {
    if (!card?.key) return;
    setSettingsOpen(false);
    setSyncOpen(false);
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
    setSyncOpen(false);
    navigate(issuePath(key));
  }, [navigate]);

  // Close the workspace: navigate back. With BrowserRouter the browser's
  // back stack handles "back to the previous view"; navigate(-1) goes
  // one step back, falling through to /issues if there's nothing on the
  // back stack (e.g. the user landed directly via a deep link).
  const closeIssue = useCallback(() => {
    setOpenIssueBrief(null);
    setDescEditing(false);
    // history.state is null on the very first entry — fall back to the
    // board route so a deep-link refresh doesn't strand the user.
    if (window.history.state && window.history.length > 1) {
      navigate(-1);
    } else {
      navigate(viewPath('board'));
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
    setSyncOpen(false);
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
        // Palette + Settings + Sync are still hand-rolled; the workspace
        // closes here when nothing else is in front of it. The composer
        // closes via Radix's built-in onOpenChange so we don't need to
        // intercept Escape for it here.
        if (paletteOpen) {
          setPaletteOpen(false);
        } else if (openIssueKey && !isEditingTarget(e.target)) {
          closeIssue();
        } else if (settingsOpen) {
          setSettingsOpen(false);
        } else if (syncOpen) {
          setSyncOpen(false);
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
  }, [paletteOpen, settingsOpen, syncOpen, openIssueKey, activeBoard, closeIssue, navigate]);

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

  // BACI-209: compound dispatch — pick a primary mode AND a follow-on
  // in one action on a todo card. Optimistic flips for BOTH affordances
  // (the spinner for the primary, the follow-on chip for the follow-on)
  // so the user sees the chain land immediately; revert both on failure
  // so the visual doesn't lie about what got queued.
  const dispatchChainFromCard = useCallback((cardKey, mode, followOnMode) => {
    let prev = null;
    setCards(cs => cs.map(c => {
      if (c.key !== cardKey) return c;
      prev = { waitingState: c.waitingState ?? null, followOn: c.followOn ?? null };
      // actionLabel for the optimistic followOn comes from promptConfig
      // — same source the chain dropdown reads so the optimistic label
      // matches what the user just clicked.
      const tpl = (promptConfig || []).find(p => p.mode === followOnMode);
      const actionLabel = tpl ? (tpl.actionLabel || tpl.label || followOnMode) : followOnMode;
      return {
        ...c,
        waitingState: { kind: 'queued_no_agent', mode },
        followOn: { mode: followOnMode, actionLabel },
      };
    }));
    api.dispatchIssueChain(activeBoard, cardKey, mode, followOnMode)
      .catch(err => {
        // Revert both affordances together — the chain is atomic on the
        // backend, so the UI must mirror that: either both flips stick
        // or neither does.
        setCards(cs => cs.map(c => c.key === cardKey ? { ...c, waitingState: prev?.waitingState ?? null, followOn: prev?.followOn ?? null } : c));
        reportError(err, { headline: "Couldn't queue dispatch chain" });
      });
  }, [activeBoard, promptConfig]);

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

  // BACI-192: queue / change / cancel the dormant follow-on dispatch on
  // a card from the kanban footer. The backend single-slot rule means a
  // *change* of mode is a two-call round trip (cancel → queue). Both
  // handlers optimistically update card.followOn so the footer button
  // flips to its new visual state on click; the 10s refresh re-asserts
  // the authoritative shape (and corrects a stale mode label when the
  // controller has already promoted / cleared the follow-on).
  const setFollowOnFromCard = useCallback(async (cardKey, mode) => {
    // Snapshot the prior followOn so a failed change can revert. A
    // missing-followon (queue) starts from no snapshot — the optimistic
    // path just sets one.
    let prev = null;
    setCards(cs => cs.map(c => {
      if (c.key !== cardKey) return c;
      prev = c.followOn ?? null;
      // The actionLabel comes from promptConfig — same source the
      // dropdown menu items read so the optimistic label matches what
      // the user just clicked.
      const tpl = (promptConfig || []).find(p => p.mode === mode);
      const actionLabel = tpl ? (tpl.actionLabel || tpl.label || mode) : mode;
      return { ...c, followOn: { mode, actionLabel } };
    }));
    try {
      if (prev) {
        // Single-slot enforcement: must cancel before queuing the new
        // mode. The cancel is idempotent — a missing dormant row
        // returns the zero DTO with no error.
        await api.cancelFollowOnDispatch(activeBoard, cardKey);
      }
      await api.queueFollowOnDispatch(activeBoard, cardKey, mode);
    } catch (err) {
      // Revert to the prior shape so the optimistic flip doesn't lie.
      setCards(cs => cs.map(c => c.key === cardKey ? { ...c, followOn: prev } : c));
      reportError(err, { headline: "Couldn't queue follow-on" });
    }
  }, [activeBoard, promptConfig]);

  const cancelFollowOnFromCard = useCallback((cardKey) => {
    let prev = null;
    setCards(cs => cs.map(c => {
      if (c.key !== cardKey) return c;
      prev = c.followOn ?? null;
      return { ...c, followOn: null };
    }));
    api.cancelFollowOnDispatch(activeBoard, cardKey)
      .catch(err => {
        // Revert on failure — same shape as setFollowOnFromCard above.
        setCards(cs => cs.map(c => c.key === cardKey ? { ...c, followOn: prev } : c));
        reportError(err, { headline: "Couldn't cancel follow-on" });
      });
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

  // BACI-131: kanban quick-eval handler. Posts an eval-tagged comment
  // for the targeted card (which is "taken" by an agent — the only
  // surface that exposes the affordance) without leaving the board.
  // The author falls back through api.addComment's existing OS-user /
  // 'web' fallback — no per-card author input. We refresh the card
  // list silently so the spinner / claim badges stay current, and
  // re-pull the brief only when the eval was posted on the
  // currently-open issue (otherwise the brief belongs to a different
  // ticket and would do an unnecessary fetch).
  const quickEvalComment = useCallback(async (cardKey, body) => {
    try {
      await api.addComment(activeBoard, cardKey, '', body, { eval: true });
      refreshCards({ silent: true });
      if (openIssueKey === cardKey) refreshBrief({ silent: true });
    } catch (err) {
      reportError(err, { headline: "Couldn't add eval comment" });
      throw err;
    }
  }, [activeBoard, openIssueKey, refreshBrief, refreshCards]);

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
  useEffect(() => {
    if (!activeBoard || activeBoard === 'all') {
      setShippedCount(0);
      return;
    }
    // Reset to 0 on scope / repo change so a stale count doesn't sit
    // on the chip while the first fetch is in flight.
    setShippedCount(0);
    const sinceDays = scopeSinceDays(shippedScope);
    let cancelled = false;
    const refresh = () => {
      api.countShippedIssues(activeBoard, sinceDays)
        .then((n) => { if (!cancelled) setShippedCount(n); })
        .catch(() => { /* pill is best-effort; the popover surfaces failures */ });
    };
    refresh();
    const id = setInterval(refresh, POLL_INTERVAL_MS);
    return () => { cancelled = true; clearInterval(id); };
  }, [activeBoard, shippedScope]);

  // BACI-193 ship flourish: detect cards that just transitioned into
  // `done` from a non-terminal column, expose the flying key + flash
  // signal to Topbar / ShippedPopover. The hook diffs the `cards`
  // array against its own internal previous-columns snapshot so the
  // poll-driven re-render is the only thing it needs.
  const { flyingKey: flyingShipKey, flashing: shipFlashing, onFlightDone: onShipFlightDone } = useShipFlourish(cards);

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
    <div className="mk-app">
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
        onBeforeNavigate={() => { setSettingsOpen(false); setSyncOpen(false); }}
        onOpenPalette={() => setPaletteOpen(true)}
        onOpenSettings={() => { setSyncOpen(false); setSettingsOpen(true); }}
        onOpenSync={openSync}
        onOpenComposer={() => setComposerOpen(true)}
        leaderState={leaderState}
        agentCounts={agentCounts}
        shippedCount={shippedCount}
        shippedScope={shippedScope}
        onShippedScopeChange={changeShippedScope}
        onOpenIssue={navigateToIssue}
        flyingShipKey={flyingShipKey}
        shipFlashing={shipFlashing}
        onShipFlightDone={onShipFlightDone}
        audioEnabled={audioEnabled}
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
          />
        </ErrorBoundary>
      ) : syncOpen ? (
        <ErrorBoundary headline="Something went wrong in Sync" label="The Sync view crashed">
          <SyncView onClose={closeSync} />
        </ErrorBoundary>
      ) : (
        <Routes>
          {/* Redirect / to /issues so a bare hit lands on the kanban. */}
          <Route path="/" element={<Navigate to={viewPath('board')} replace />} />
          <Route
            path="/issues"
            element={
              <ErrorBoundary headline="Something went wrong on the board" label="The Board view crashed">
                <Board
                  activeBoard={activeBoard}
                  columns={columns}
                  cards={cards}
                  promptConfig={promptConfig}
                  stateGraph={stateGraph}
                  onMoveCard={moveCard}
                  onOpenCard={openCard}
                  onOpenIssue={navigateToIssue}
                  onDispatchFromCard={dispatchFromCard}
                  onDispatchChainFromCard={dispatchChainFromCard}
                  onCancelWaitingCard={cancelWaitingFromCard}
                  onQuickEval={quickEvalComment}
                  pinnedKeys={pinnedKeys}
                  onTogglePin={togglePinKey}
                  onSetFollowOn={setFollowOnFromCard}
                  onCancelFollowOn={cancelFollowOnFromCard}
                  hoveredKey={hoveredKey}
                  jumpKey={jumpKey}
                  flyingShipKey={flyingShipKey}
                />
              </ErrorBoundary>
            }
          />
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
                  promptConfig={promptConfig}
                  cards={cards}
                  onClose={closeIssue}
                  onSaveDescription={saveDescription}
                  onAddComment={addComment}
                  onDeleteComment={deleteComment}
                  onDispatch={(mode) => openIssueKey && dispatchFromCard(openIssueKey, mode)}
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
          {/* Unknown route lands on the board so refreshes / stray links
              don't strand the user on a 404 we don't render. */}
          <Route path="*" element={<Navigate to={viewPath('board')} replace />} />
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
      {/* BACI-171: bottom-right activity tray. Sibling of CommandPalette
          / ErrorModal so it overlays whatever view is current — Board,
          Agents, Features, Docs, History, Issue workspace, Settings,
          Sync — without reflowing the column layout. The tray is a
          pure-derived view over `cards`; the App's existing 10s
          refreshCards poll already drives the diff. */}
      <ErrorBoundary headline="Something went wrong in the activity tray" label="The activity tray crashed">
        <ActivityTray
          cards={cards}
          pinnedKeys={pinnedKeys}
          onTogglePin={togglePinKey}
          onOpenCard={openCard}
          onHoverCard={setHoveredKey}
          onCardClickFromTray={triggerJump}
        />
      </ErrorBoundary>
      <ErrorModal />
      </LayoutGroup>
    </div>
    </LazyMotion>
    </TooltipProvider>
  );
}
