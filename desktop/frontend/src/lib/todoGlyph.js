// todoGlyph maps a TodoWrite status to the leading glyph rendered in
// the per-agent drill-down and the per-card expanded Tasks list.
// Mirrors the TUI's vocabulary so an operator can switch between
// surfaces without re-learning. Shared by AgentsView (full panel) and
// KanbanCard (inline pill expansion) so the glyph table has one
// source of truth.
export function todoGlyph(status) {
  switch (status) {
    case 'completed':
      return '●';
    case 'in_progress':
      return '◐';
    default:
      return '○';
  }
}
