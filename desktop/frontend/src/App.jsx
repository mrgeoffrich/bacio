import React, { useState, useEffect } from 'react';
import Sidebar from './components/Sidebar.jsx';
import Topbar from './components/Topbar.jsx';
import Board from './components/Board.jsx';
import IssueDrawer from './components/IssueDrawer.jsx';
import CommandPalette from './components/CommandPalette.jsx';
import { boards, cards as seedCards } from './data.js';

export default function App() {
  const [collapsed, setCollapsed] = useState(false);
  const [activeBoard, setActiveBoard] = useState('b1');
  const [cards, setCards] = useState(seedCards);
  const [openCard, setOpenCard] = useState(null);
  const [paletteOpen, setPaletteOpen] = useState(false);

  const claudeRunning = cards.filter(c => c.claude && c.column !== 'done').length;

  useEffect(() => {
    const onKey = (e) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault();
        setPaletteOpen(true);
      } else if (e.key === 'Escape') {
        setPaletteOpen(false);
        setOpenCard(null);
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);

  const moveCard = (id, toCol) => {
    setCards(cs => cs.map(c => c.id === id ? { ...c, column: toCol } : c));
  };

  const handToClaude = () => {
    if (!openCard) return;
    setCards(cs => cs.map(c => c.id === openCard.id
      ? { ...c, claude: true, column: 'doing', assignees: ['claude', ...c.assignees.filter(a => a !== 'claude')] }
      : c));
    setOpenCard(c => c ? { ...c, claude: true, column: 'doing', assignees: ['claude', ...c.assignees.filter(a => a !== 'claude')] } : c);
  };

  const ship = () => {
    if (!openCard) return;
    setCards(cs => cs.map(c => c.id === openCard.id ? { ...c, column: 'done' } : c));
    setOpenCard(null);
  };

  const board = boards.find(b => b.id === activeBoard);

  return (
    <div className="mk-app">
      <Sidebar
        collapsed={collapsed}
        onToggle={() => setCollapsed(c => !c)}
        activeBoard={activeBoard}
        onPickBoard={setActiveBoard}
        agentRuns={claudeRunning}
      />
      <main className="mk-main">
        <Topbar
          boardName={board?.name || ''}
          onOpenPalette={() => setPaletteOpen(true)}
          onNewIssue={() => alert('+ New issue (demo)')}
          agentRuns={claudeRunning}
        />
        <Board
          cards={cards}
          onMoveCard={moveCard}
          onOpenCard={setOpenCard}
        />
      </main>
      <IssueDrawer
        card={openCard}
        onClose={() => setOpenCard(null)}
        onHandToClaude={handToClaude}
        onShip={ship}
      />
      <CommandPalette
        open={paletteOpen}
        onClose={() => setPaletteOpen(false)}
        onPick={setOpenCard}
      />
    </div>
  );
}
