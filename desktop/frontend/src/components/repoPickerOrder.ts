import type { Board, RepoActivity } from '../api';

// BACI-369: the repo picker's activity ordering, kept pure so it can be
// tested without a DOM. Applied inside RepoPicker only — the provider's
// `boards` array stays in prefix order, because RepoProvider.fallbackPrefix
// (which repo a bare `/` lands on) and the Settings / RepoNotFound lists
// read the same array.

// activityByPrefix indexes a polled activity list for lookup by repo.
export function activityByPrefix(activity: RepoActivity[]): Map<string, RepoActivity> {
  return new Map(activity.map((a) => [a.prefix, a]));
}

// rankRepos returns a new array ordered by:
//   1. repos with agent jobs in flight first — a boolean tier, not a
//      magnitude (three running jobs isn't more urgent than one);
//   2. most recent activity first, repos with no timestamp last;
//   3. prefix ascending — today's order, so a store with no activity
//      signal at all renders exactly as it always has.
// Never mutates its input.
export function rankRepos(boards: Board[], byPrefix: Map<string, RepoActivity>): Board[] {
  return [...boards].sort((a, b) => {
    const aa = byPrefix.get(a.prefix);
    const ba = byPrefix.get(b.prefix);
    const aBusy = (aa?.activeJobs ?? 0) > 0;
    const bBusy = (ba?.activeJobs ?? 0) > 0;
    if (aBusy !== bBusy) return aBusy ? -1 : 1;
    const aWhen = activityTime(aa);
    const bWhen = activityTime(ba);
    if (aWhen !== bWhen) return bWhen - aWhen;
    return a.prefix.localeCompare(b.prefix);
  });
}

// activityTime is the sortable epoch-ms form of a repo's last activity.
// A missing entry, a missing timestamp, and an unparseable one all
// collapse to -Infinity so those repos sort last rather than jumping to
// the top on a NaN comparison.
function activityTime(a: RepoActivity | undefined): number {
  if (!a?.lastActivityAt) return -Infinity;
  const ms = Date.parse(a.lastActivityAt);
  return Number.isNaN(ms) ? -Infinity : ms;
}
