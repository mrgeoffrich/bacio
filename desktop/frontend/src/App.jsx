import React, { useState, useEffect } from 'react';
import Sidebar from './components/Sidebar.jsx';
import Topbar from './components/Topbar.jsx';
import Board from './components/Board.jsx';
import IssueDrawer from './components/IssueDrawer.jsx';
import CommandPalette from './components/CommandPalette.jsx';
import * as api from './api';

export default function App() {
  const [collapsed, setCollapsed] = useState(false);
  const [boards, setBoards] = useState([]);
  const [columns, setColumns] = useState([]);
  const [activeBoard, setActiveBoard] = useState(null); // repo prefix
  const [cards, setCards] = useState([]);
  const [openIssue, setOpenIssue] = useState(null);
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);

  const claudeRunning = cards.filter(c => c.claude && c.column !== 'done').length;

  // Load boards + columns once on mount.
  useEffect(() => {
    Promise.all([api.listBoards(), api.listColumns()])
      .then(([bs, cols]) => {
        setBoards(bs);
        setColumns(cols);
        if (bs.length > 0) setActiveBoard(bs[0].prefix);
        setLoading(false);
      })
      .catch(err => { setError(err.message); setLoading(false); });
  }, []);

  // Load cards whenever the active board changes.
  useEffect(() => {
    if (!activeBoard) return;
    api.listCards(activeBoard)
      .then(setCards)
      .catch(err => setError(err.message));
  }, [activeBoard]);

  useEffect(() => {
    const onKey = (e) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault();
        setPaletteOpen(true);
      } else if (e.key === 'Escape') {
        setPaletteOpen(false);
        setOpenIssue(null);
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

  // Local-only, like moveCard — the drawer actions don't hit the backend yet.
  const handToClaude = () => {
    if (!openIssue) return;
    setCards(cs => cs.map(c => c.key === openIssue.key
      ? { ...c, claude: true, column: 'in_progress', assignees: ['claude', ...c.assignees.filter(a => a !== 'claude')] }
      : c));
    setOpenIssue(null);
  };

  const ship = () => {
    if (!openIssue) return;
    setCards(cs => cs.map(c => c.key === openIssue.key ? { ...c, column: 'done' } : c));
    setOpenIssue(null);
  };

  const board = boards.find(b => b.prefix === activeBoard);

  return (
    <div className="mk-app">
      <Sidebar
        collapsed={collapsed}
        onToggle={() => setCollapsed(c => !c)}
        boards={boards}
        activeBoard={activeBoard}
        onPickBoard={setActiveBoard}
        agentRuns={claudeRunning}
      />
      <main className="mk-main">
        <Topbar
          boardName={board?.name || ''}
          onOpenPalette={() => setPaletteOpen(true)}
          onNewIssue={() => alert('+ New issue (coming soon)')}
          agentRuns={claudeRunning}
        />
        {loading ? (
          <div className="mk-app-state">Loading…</div>
        ) : error ? (
          <div className="mk-app-state mk-app-error">Error: {error}</div>
        ) : (
          <Board
            columns={columns}
            cards={cards}
            onMoveCard={moveCard}
            onOpenCard={openCard}
          />
        )}
      </main>
      <IssueDrawer
        issue={openIssue}
        onClose={() => setOpenIssue(null)}
        onHandToClaude={handToClaude}
        onShip={ship}
      />
      <CommandPalette
        open={paletteOpen}
        cards={cards}
        onClose={() => setPaletteOpen(false)}
        onPick={openCard}
      />
    </div>
  );
}
