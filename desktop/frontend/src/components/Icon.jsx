import React from 'react';
import { Search, Plus, Columns3, GitBranch, Settings, X, Zap, Lock, MessageSquare, AlertTriangle, Minimize2, Maximize2, SkipForward, Pin, ChevronsDownUp, ChevronsUpDown, RefreshCw } from 'lucide-react';

// Glyphs come from lucide-react; the UI references them by short name
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
  // BACI-191 / BACI-207: per-column compact-cards toggle. The paired
  // chevron glyphs mirror the Minimize2 / Maximize2 column-collapse
  // pair landed in BACI-201 — converging chevrons (ChevronsDownUp)
  // read as "squeeze the cards together", diverging chevrons
  // (ChevronsUpDown) as "let them breathe again". Rows3 (the prior
  // glyph) leaned on a "denser rows" metaphor that didn't land in
  // context next to the count and the collapse glyph.
  'chevrons-down-up': ChevronsDownUp,
  'chevrons-up-down': ChevronsUpDown,
  refresh: RefreshCw,
};

export default function Icon({ name }) {
  const Glyph = ICONS[name];
  return (
    <span className="mk-icon" aria-hidden="true">
      {Glyph ? <Glyph strokeWidth={1.75} /> : null}
    </span>
  );
}
