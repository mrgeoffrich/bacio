import React, { useState, useEffect, useCallback } from 'react';
import Topbar, { NAV } from './components/Topbar.jsx';
import Board from './components/Board.jsx';
import DocsView from './components/DocsView.jsx';
import FeaturesView from './components/FeaturesView.jsx';
import AgentsView from './components/AgentsView.jsx';
import HistoryView from './components/HistoryView.jsx';
import IssueDrawer from './components/IssueDrawer.jsx';
import IssueEditModal from './components/IssueEditModal.jsx';
import CommandPalette from './components/CommandPalette.jsx';
import SettingsPanel from './components/SettingsPanel.jsx';
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
  const [theme, setTheme] = useState(readTheme);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);

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

  // Load the repository list + columns once on mount. Once boards resolve,
  // land activeBoard on the persisted repo if it still exists, otherwise the
  // first repo — every screen needs a concrete repo, there's no "all" option.
  useEffect(() => {
    Promise.all([api.listBoards(), api.listColumns()])
      .then(([bs, cols]) => {
        setBoards(bs);
        setColumns(cols);
        setActiveBoard(prev => bs.some(b => b.prefix === prev) ? prev : (bs[0]?.prefix ?? ''));
        setLoading(false);
      })
      .catch(err => { setError(err.message); setLoading(false); });
  }, []);

  // Remember the selected repo so the app reopens on the same one.
  useEffect(() => {
    if (activeBoard) persistActiveRepo(activeBoard);
  }, [activeBoard]);

  // refreshCards / refreshAgents reload the App-owned card and agent lists
  // for the active repo. Used by the repo-change effect, the screen-switch
  // effect, the 10s poll, the Agents panel's refresh button, and after a
  // dispatch so the counts move. Pass { silent: true } on the poll path so a
  // transient failure logs instead of kicking the app to the error screen.
  const refreshCards = useCallback((opts = {}) => {
    if (!activeBoard) return;
    api.listCards(activeBoard)
      .then(setCards)
      .catch(err => {
        if (opts.silent) console.warn('card refresh failed:', err);
        else setError(err.message);
      });
  }, [activeBoard]);

  const refreshAgents = useCallback((opts = {}) => {
    if (!activeBoard) return;
    api.listAgents(activeBoard)
      .then(setAgents)
      .catch(err => {
        if (opts.silent) console.warn('agent refresh failed:', err);
        else setError(err.message);
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
        setPaletteOpen(false);
        setOpenIssue(null);
        setSettingsOpen(false);
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
  const openCard = (card) => {
    api.getIssue(activeBoard, card.key)
      .then(setOpenIssue)
      .catch(err => setError(err.message));
  };

  // Add a repository: the backend opens a native folder picker and registers
  // the chosen git working tree. On success, refresh the board list and jump
  // to the new repo; an empty prefix means the user cancelled the dialog.
  const addRepository = () => {
    api.addRepository()
      .then(board => {
        if (!board.prefix) return;
        return api.listBoards().then(bs => {
          setBoards(bs);
          setActiveBoard(board.prefix);
        });
      })
      .catch(err => setError(err.message));
  };

  // Drag-to-move is visual-only for now: update local state, don't persist.
  // Persisting via SetIssueState is the follow-up write pass.
  const moveCard = (key, toCol) => {
    setCards(cs => cs.map(c => c.key === key ? { ...c, column: toCol } : c));
  };

  // Queue a dispatch for an agent: pick the agent + mode (plan/implement)
  // + an optional note in the drawer, then write it through the backend.
  const sendToAgent = (agentName, mode, note) => {
    if (!openIssue) return;
    api.dispatchIssue(activeBoard, openIssue.key, agentName, mode, note)
      .then(() => {
        // Optimistically flag the card as claimed-by-an-agent so the
        // breathing-pulse treatment kicks in; refresh the agent counts.
        setCards(cs => cs.map(c => c.key === openIssue.key ? { ...c, claude: true } : c));
        refreshAgents();
        setOpenIssue(null);
      })
      .catch(err => setError(err.message));
  };

  const ship = () => {
    if (!openIssue) return;
    setCards(cs => cs.map(c => c.key === openIssue.key ? { ...c, column: 'done' } : c));
    setOpenIssue(null);
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
    <div className="mk-app">
      <Topbar
        boards={boards}
        activeBoard={activeBoard}
        onPickBoard={setActiveBoard}
        onAddRepository={addRepository}
        activeView={activeView}
        onChangeView={setActiveView}
        onOpenPalette={() => setPaletteOpen(true)}
        onOpenSettings={() => setSettingsOpen(true)}
      />
      {loading ? (
        <div className="mk-app-state">Loading…</div>
      ) : error ? (
        <div className="mk-app-state mk-app-error">Error: {error}</div>
      ) : activeView === 'docs' ? (
        <DocsView activeBoard={activeBoard} />
      ) : activeView === 'features' ? (
        <FeaturesView activeBoard={activeBoard} />
      ) : activeView === 'agents' ? (
        <AgentsView agents={agents} onRefresh={refreshAgents} />
      ) : activeView === 'history' ? (
        <HistoryView activeBoard={activeBoard} />
      ) : (
        <Board
          columns={columns}
          cards={cards}
          onMoveCard={moveCard}
          onOpenCard={openCard}
        />
      )}
      <IssueDrawer
        issue={openIssue}
        agents={agents}
        onClose={closeDrawer}
        onSendToAgent={sendToAgent}
        onShip={ship}
        onEdit={() => setEditIssueOpen(true)}
      />
      {editIssueOpen && openIssue && (
        <IssueEditModal
          issue={openIssue}
          repoPrefix={activeBoard}
          onClose={() => setEditIssueOpen(false)}
          onSaved={onIssueSaved}
        />
      )}
      <CommandPalette
        open={paletteOpen}
        cards={cards}
        onClose={() => setPaletteOpen(false)}
        onPick={openCard}
      />
      <SettingsPanel
        open={settingsOpen}
        theme={theme}
        onChangeTheme={setTheme}
        onClose={() => setSettingsOpen(false)}
      />
    </div>
  );
}
