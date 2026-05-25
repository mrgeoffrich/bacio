import React from 'react';
import { Search, Plus, Columns3, GitBranch, Settings, X, Zap, Lock, MessageSquare, AlertTriangle, Minimize2, Maximize2, SkipForward, Pin, Rows3 } from 'lucide-react';

// ClaudeMark is a brand glyph — no icon library carries Claude's logo, so it
// stays a hand-rolled SVG. It ignores any props passed by <Icon>.
function ClaudeMark() {
  return (
    <svg viewBox="0 0 16 16" fill="currentColor">
      <circle cx="3" cy="3" r="1.4" />
      <circle cx="3" cy="13" r="1.4" />
      <circle cx="13" cy="8" r="2.2" />
      <path d="M4.4 3.5 L11 7.3" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round" opacity=".5" fill="none" />
      <path d="M4.4 12.5 L11 8.7" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round" opacity=".5" fill="none" />
    </svg>
  );
}

// Standard glyphs come from lucide-react; the UI references them by short name
// via <Icon name="..." />. The wrapper span (.mk-icon) sizes them, so the SVG's
// own width/height attributes are overridden by CSS.
const ICONS = {
  search: Search,
  plus: Plus,
  board: Columns3,
  branch: GitBranch,
  settings: Settings,
  x: X,
  zap: Zap,
  lock: Lock,
  comment: MessageSquare,
  'alert-triangle': AlertTriangle,
  // BACI-188 / BACI-201: per-column collapse / expand glyphs on the
  // kanban board. Minimize2 (converging diagonals) sits in the
  // populated-column header; Maximize2 (diverging diagonals) sits at
  // the top of the collapsed strip. The window-manager pair reads as
  // shrink / grow regardless of which side the column lives on, where
  // the prior chevron pair leaned on direction alone.
  'minimize-2': Minimize2,
  'maximize-2': Maximize2,
  // BACI-192: SkipForward is the kanban footer follow-on dispatch
  // button glyph — "after this, do …". Pin is the activity-tray
  // PINNED section header glyph (the corner button itself is a
  // CSS-only clip-path triangle, no glyph).
  forward: SkipForward,
  pin: Pin,
  // BACI-191: per-column compact-cards toggle. Rows3 conveys the
  // "densify rows" metaphor, distinct from the collapse chevrons.
  'rows-3': Rows3,
  claude: ClaudeMark,
};

export default function Icon({ name }) {
  const Glyph = ICONS[name];
  return (
    <span className="mk-icon" aria-hidden="true">
      {Glyph ? <Glyph strokeWidth={1.75} /> : null}
    </span>
  );
}
