// relTime renders a coarse "time since" for the last-seen line and the
// open-question rows. Unlike lib/formatWhen it stays in "Nd ago" form
// indefinitely rather than flipping to an absolute date past a week — the
// agent card is a live snapshot where staleness, not the exact date, is
// what reads.
export function relTime(iso: string): string {
  const ms = Date.now() - new Date(iso).getTime();
  const m = Math.floor(ms / 60000);
  if (m < 1) return 'just now';
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  return `${Math.floor(h / 24)}d ago`;
}
