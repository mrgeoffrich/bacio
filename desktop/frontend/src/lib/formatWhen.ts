// formatWhen (BACI-187, extracted from HistoryView.tsx) mirrors the TUI's
// history view: a relative phrase for recent entries ("just now", "5m ago",
// "3h ago", "2d ago"), an absolute local-date string once entries are more
// than a week old. Used by HistoryView and the BACI-187 ShippedPopover.
export function formatWhen(iso: string): string {
  const then = new Date(iso);
  const m = Math.floor((Date.now() - then.getTime()) / 60000);
  if (m < 1) return 'just now';
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  const d = Math.floor(h / 24);
  if (d < 7) return `${d}d ago`;
  return then.toLocaleDateString();
}
