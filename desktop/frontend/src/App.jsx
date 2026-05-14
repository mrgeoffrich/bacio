import React, { useState, useEffect, useCallback } from 'react';
import Topbar, { NAV } from './components/Topbar.jsx';
import Board from './components/Board.jsx';
import DocsView from './components/DocsView.jsx';
import FeaturesView from './components/FeaturesView.jsx';
import AgentsView from './components/AgentsView.jsx';
import HistoryView from './components/HistoryView.jsx';
import IssueDrawer from './components/IssueDrawer.jsx';
import CommandPalette from './components/CommandPalette.jsx';
import SettingsPanel from './components/SettingsPanel.jsx';
import * as api from './api';

const THEME_KEY = 'bacio-theme'; // persisted preference: 'system' | 'light' | 'dark'
const REPO_KEY = 'bacio-active-repo'; // persisted preference: last-selected repo prefix

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

  // refreshAgents reloads the agent list for the active repo. Used by the
  // board-change effect, the Agents panel's refresh button, and after a
  // dispatch so the counts move.
  const refreshAgents = useCallback(() => {
    if (!activeBoard) return;
    api.listAgents(activeBoard)
      .then(setAgents)
      .catch(err => setError(err.message));
  }, [activeBoard]);

  // Load cards + agents whenever the selected repository changes.
  useEffect(() => {
    if (!activeBoard) return;
    api.listCards(activeBoard)
      .then(setCards)
      .catch(err => setError(err.message));
    refreshAgents();
  }, [activeBoard, refreshAgents]);

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

  return (
    <div className="mk-app">
      <Topbar
        boards={boards}
        activeBoard={activeBoard}
        onPickBoard={setActiveBoard}
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
        onClose={() => setOpenIssue(null)}
        onSendToAgent={sendToAgent}
        onShip={ship}
      />
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
