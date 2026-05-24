import React from 'react';
import { Search, Plus, Columns3, GitBranch, Settings, X, Zap, Lock, MessageSquare } from 'lucide-react';

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
