import React, { useState, useEffect, useCallback, useRef } from 'react';
import { Events } from '@wailsio/runtime';
import Topbar, { NAV } from './components/Topbar.jsx';
import Board from './components/Board.jsx';
import DocsView from './components/DocsView.jsx';
import FeaturesView from './components/FeaturesView.jsx';
import AgentsView from './components/AgentsView.jsx';
import HistoryView from './components/HistoryView.jsx';
import IssueDrawer from './components/IssueDrawer.jsx';
import IssueEditModal from './components/IssueEditModal.jsx';
import CommandPalette from './components/CommandPalette.jsx';
import SettingsView from './components/SettingsView.jsx';
import ErrorBoundary from './components/ErrorBoundary.jsx';
import ErrorModal from './components/ErrorModal.jsx';
import { Provider as TooltipProvider } from '@radix-ui/react-tooltip';
import { reportError } from './errors';
import { WEB_MODE } from './env';
import * as api from './api';

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
  const [boards, setBoards] = useState([]);
  const [columns, setColumns] = useState([]);
  // The selected repo prefix. Starts from the persisted preference (or "" on
  // first run / before the repo list resolves); the mount effect lands it on
  // a real repo once boards load.
  const [activeBoard, setActiveBoard] = useState(readActiveRepo);
  const [activeView, setActiveView] = useState('board'); // 'board' | 'features' | 'docs' | 'agents' | 'history'
  const [cards, setCards] = useState([]);
  const [openIssue, setOpenIssue] = useState(null);
  const [editIssueOpen, setEditIssueOpen] = useState(false);
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [agents, setAgents] = useState([]);
  // promptConfig is the global (repo-independent) dispatch-prompt config:
  // one entry per stage with its label and the issue states it's valid
  // to run from. Board → KanbanCard reads it to gate the per-card action
  // button. Loaded on mount; reloaded when the Settings view closes.
  const [promptConfig, setPromptConfig] = useState([]);
  // hideEmptyColumns is the App-owned Board preference: when true, the
  // Board drops columns with zero cards. Loaded from app_settings on
  // mount; flipped live from the Settings screen. Passed to the
  // presentational Board alongside columns/cards.
  const [hideEmptyColumns, setHideEmptyColumns] = useState(false);
  // leaderState tracks the UI leader-election result from LeaderService.
  // amLeader = true means this desktop process holds the lease and may
  // dispatch. Standby processes show a chip and disable the per-card button.
  const [leaderState, setLeaderState] = useState({ amLeader: false, holderLabel: '' });
  const [theme, setTheme] = useState(readTheme);
  const [loading, setLoading] = useState(true);

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
    Promise.all([api.listBoards(), api.listColumns(), api.listPromptTemplates(), api.getBoardPreferences()])
      .then(([bs, cols, tpls, prefs]) => {
        setBoards(bs);
        setColumns(cols);
        setPromptConfig(tpls);
        setHideEmptyColumns(prefs.hideEmptyColumns);
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

  // changeHideEmptyColumns persists the Board preference, then updates
  // the App-owned flag on success so the Board reacts immediately —
  // optimistic-then-confirmed, the same shape as the theme handler. A
  // failed write reports through the modal and leaves the toggle where
  // it was.
  const changeHideEmptyColumns = useCallback((next) => {
    api.setBoardPreferences(next)
      .then(prefs => setHideEmptyColumns(prefs.hideEmptyColumns))
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
  // loaded regardless of the active view — CommandPalette reads cards and
  // IssueDrawer reads agents, and either can open from any screen.
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

  useEffect(() => {
    const onKey = (e) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault();
        setPaletteOpen(true);
      } else if (e.key === 'Escape') {
        // IssueDrawer / IssueEditModal / SettingsView modals are Radix
        // Dialogs and catch Escape themselves. Only the palette (still
        // hand-rolled) needs the window-level handler.
        setPaletteOpen(false);
      } else if (!e.metaKey && !e.ctrlKey && !e.altKey && e.key >= '1' && e.key <= '9') {
        // Digit keys jump between nav views, like the TUI's tab shortcuts —
        // unless the user is typing into a field or the doc editor.
        if (isEditingTarget(e.target)) return;
        const idx = Number(e.key) - 1;
        if (idx < NAV.length) setActiveView(NAV[idx].view);
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);

  // Open the issue drawer — fetch the full detail payload for the card.
  const openCard = useCallback((card) => {
    api.getIssue(activeBoard, card.key)
      .then(setOpenIssue)
      .catch(err => reportError(err, { headline: "Couldn't open issue" }));
  }, [activeBoard]);

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
  const moveCard = useCallback((key, toCol) => {
    let prevCol = null;
    setCards(cs => {
      const prev = cs.find(c => c.key === key);
      if (!prev || prev.column === toCol) return cs;
      prevCol = prev.column;
      return cs.map(c => c.key === key ? { ...c, column: toCol } : c);
    });
    if (prevCol === null) return;
    api.setIssueState(activeBoard, key, toCol)
      .catch(err => {
        reportError(err, { headline: "Couldn't move card" });
        setCards(cs => cs.map(c => c.key === key ? { ...c, column: prevCol } : c));
      });
  }, [activeBoard]);

  // Dispatch a prompt from a card's action button: the backend gates the
  // mode on the issue's state and enqueues a target-less dispatch the
  // matcher binds later — the caller names neither an agent nor a note.
  // Post-BACI-51 the call always succeeds (gate-aside); the waiting
  // spinner takes over until the matcher binds.
  //
  // Set waitingForClaim *before* the request so the spinner and drag-
  // disable kick in on the click, not when the request returns. The
  // backend sets the same flag on the issue row inside AddDispatch, so
  // the next refresh keeps it true until the matcher binds (then
  // waiting clears and taken takes over). Revert on failure.
  const dispatchFromCard = useCallback((cardKey, mode) => {
    setCards(cs => cs.map(c => c.key === cardKey ? { ...c, waitingForClaim: true } : c));
    api.dispatchIssue(activeBoard, cardKey, mode)
      .catch(err => {
        setCards(cs => cs.map(c => c.key === cardKey ? { ...c, waitingForClaim: false } : c));
        reportError(err, { headline: "Couldn't dispatch agent" });
      });
  }, [activeBoard]);

  // BACI-51 spinner-as-cancel-button handler: withdraw a card's queued
  // (or pending/delivered) dispatch. Optimistically clears the local
  // waitingForClaim flag so the spinner disappears immediately; the
  // refresh poll reads the authoritative state.
  const cancelWaitingFromCard = useCallback((cardKey) => {
    api.cancelWaitingDispatch(activeBoard, cardKey)
      .then(() => {
        setCards(cs => cs.map(c => c.key === cardKey ? { ...c, waitingForClaim: false } : c));
      })
      .catch(err => reportError(err, { headline: "Couldn't cancel queued dispatch" }));
  }, [activeBoard]);

  // Ship: close the drawer, optimistically flip the card to "done", and
  // persist via setIssueState so the change survives the next 10s poll.
  // Mirrors moveCard's shape; on failure, refresh from the source of
  // truth rather than try to restore the (already-discarded) old state.
  const ship = () => {
    if (!openIssue) return;
    const key = openIssue.key;
    setOpenIssue(null);
    setCards(cs => cs.map(c => c.key === key ? { ...c, column: 'done' } : c));
    api.setIssueState(activeBoard, key, 'done')
      .catch(err => {
        reportError(err, { headline: "Couldn't ship issue" });
        refreshCards();
      });
  };

  // The edit modal returns the refreshed IssueDetail after each write, so the
  // drawer behind it reflects the new description / comment immediately.
  const onIssueSaved = (updated) => {
    setOpenIssue(updated);
  };

  // Closing the drawer also dismisses the edit modal — otherwise its open
  // flag would survive and re-trigger when the next issue is opened.
  const closeDrawer = () => {
    setOpenIssue(null);
    setEditIssueOpen(false);
  };

  return (
    <TooltipProvider delayDuration={250} skipDelayDuration={150}>
    <div className="mk-app">
      <Topbar
        boards={boards}
        activeBoard={activeBoard}
        onPickBoard={setActiveBoard}
        onAddRepository={addRepository}
        activeView={activeView}
        onChangeView={(v) => { setSettingsOpen(false); setActiveView(v); }}
        onOpenPalette={() => setPaletteOpen(true)}
        onOpenSettings={() => setSettingsOpen(true)}
        leaderState={leaderState}
      />
      {loading ? (
        <div className="mk-app-state">Loading…</div>
      ) : settingsOpen ? (
        <ErrorBoundary headline="Something went wrong in Settings" label="The Settings view crashed">
          <SettingsView
            theme={theme}
            onChangeTheme={setTheme}
            hideEmptyColumns={hideEmptyColumns}
            onChangeHideEmptyColumns={changeHideEmptyColumns}
            columns={columns}
            onClose={closeSettings}
            onTemplatesChanged={refreshPromptConfig}
          />
        </ErrorBoundary>
      ) : activeView === 'docs' ? (
        <ErrorBoundary headline="Something went wrong in Docs" label="The Docs view crashed">
          <DocsView activeBoard={activeBoard} />
        </ErrorBoundary>
      ) : activeView === 'features' ? (
        <ErrorBoundary headline="Something went wrong in Features" label="The Features view crashed">
          <FeaturesView activeBoard={activeBoard} />
        </ErrorBoundary>
      ) : activeView === 'agents' ? (
        <ErrorBoundary headline="Something went wrong in Agents" label="The Agents view crashed">
          <AgentsView agents={agents} onRefresh={refreshAgents} />
        </ErrorBoundary>
      ) : activeView === 'history' ? (
        <ErrorBoundary headline="Something went wrong in History" label="The History view crashed">
          <HistoryView activeBoard={activeBoard} />
        </ErrorBoundary>
      ) : (
        <ErrorBoundary headline="Something went wrong on the board" label="The Board view crashed">
          <Board
            columns={columns}
            cards={cards}
            promptConfig={promptConfig}
            hideEmptyColumns={hideEmptyColumns}
            onMoveCard={moveCard}
            onOpenCard={openCard}
            onDispatchFromCard={dispatchFromCard}
            onCancelWaitingCard={cancelWaitingFromCard}
          />
        </ErrorBoundary>
      )}
      <ErrorBoundary headline="Something went wrong in the issue drawer" label="The issue drawer crashed">
        <IssueDrawer
          issue={openIssue}
          onClose={closeDrawer}
          onShip={ship}
          onEdit={() => setEditIssueOpen(true)}
        />
      </ErrorBoundary>
      {editIssueOpen && openIssue && (
        <ErrorBoundary headline="Something went wrong in the edit modal" label="The edit modal crashed">
          <IssueEditModal
            issue={openIssue}
            repoPrefix={activeBoard}
            onClose={() => setEditIssueOpen(false)}
            onSaved={onIssueSaved}
          />
        </ErrorBoundary>
      )}
      <ErrorBoundary headline="Something went wrong in the command palette" label="The command palette crashed">
        <CommandPalette
          open={paletteOpen}
          cards={cards}
          onClose={() => setPaletteOpen(false)}
          onPick={openCard}
        />
      </ErrorBoundary>
      <ErrorModal />
    </div>
    </TooltipProvider>
  );
}
