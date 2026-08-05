// BACI-376: the topbar sync badge's state machine, lifted out of
// Topbar so the variants are unit-testable and the copy lives in one
// place.
//
// The badge answers one question — "is *this* repo being mirrored right
// now?" — and it takes three independent bits to answer:
//
//   - syncMirroredBy: a sync repo on this machine already carries this
//     repo's exported data. The export is whole-DB, so this is set for
//     plenty of repos that never ran `bacio sync init` themselves.
//   - syncEnabled: this repo has a sync remote in its own
//     .bacio/config.yaml, i.e. it can drive a tick itself.
//   - syncBackgroundEnabled: the global `sync.background_enabled`
//     toggle in Settings → Sync. App-wide.
//
// BACI-376 was filed because the badge read only the middle bit and
// called everything else "not configured" — including repos whose
// issues were being mirrored every five minutes, and which the Sync
// settings screen listed as linked members of the sync repo. Mirroring
// is what the user is actually asking about, so it leads; the tooltips
// name the repo and the sync repo so the two surfaces can be reconciled
// without guesswork.

import type { Board } from '../api';

// SyncBadgeVariant doubles as the CSS state class suffix — the
// component renders `mk-sync-btn is-${variant}`.
export type SyncBadgeVariant = 'syncing' | 'paused' | 'error' | 'enabled' | 'unconfigured';

export type SyncBadgeState = {
  variant: SyncBadgeVariant;
  // label is the accessible name (aria-label) — the icon carries no text.
  label: string;
  // tooltip is the hover copy, always ending in the click affordance.
  tooltip: string;
};

// formatSyncTime renders an ISO timestamp as a short local string for
// the badge's hover tooltip. Falls back to the raw string if the date
// can't be parsed.
export function formatSyncTime(iso: string) {
  if (!iso) return '';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleString();
}

const OPEN_HINT = 'click to open Sync settings';

// syncBadgeState maps a board's sync fields onto the badge's variant and
// copy. An absent board (boards still loading, or no repo resolved from
// the URL) reads as unconfigured with repo-neutral copy — the same muted
// affordance the badge has always shown in that window.
export function syncBadgeState(board: Board | undefined): SyncBadgeState {
  const repo = board?.prefix || 'this repo';
  const configured = !!board?.syncEnabled;
  const mirroredBy = board?.syncMirroredBy || '';
  const backgroundEnabled = !!board?.syncBackgroundEnabled;
  const inProgress = !!board?.syncInProgress;
  const lastError = board?.syncLastError || '';
  const lastAt = board?.syncLastAt || '';
  // Either route gets this repo's data into a sync repo, so either one
  // means "yes, you're covered".
  const synced = configured || !!mirroredBy;
  const into = mirroredBy ? ` into ${mirroredBy}` : '';
  const when = lastAt ? `last synced ${formatSyncTime(lastAt)}` : 'no run has completed yet';

  if (inProgress) {
    return {
      variant: 'syncing',
      label: 'Syncing…',
      tooltip: `Background sync in progress · ${OPEN_HINT}`,
    };
  }
  // Paused outranks a stale error: with the ticker off, "background
  // sync is off" is the live truth and the failure is history — so it
  // rides along in the tooltip rather than owning the badge.
  if (synced && !backgroundEnabled) {
    const trailer = lastError ? ` · last run failed: ${lastError}` : '';
    return {
      variant: 'paused',
      label: 'Sync paused',
      tooltip: `${repo} is mirrored${into}, but background sync is turned off for every repo${trailer} · ${OPEN_HINT}`,
    };
  }
  if (synced && lastError) {
    return {
      variant: 'error',
      label: 'Sync failed',
      tooltip: `Last sync of ${repo} failed: ${lastError} · ${OPEN_HINT}`,
    };
  }
  if (configured) {
    return {
      variant: 'enabled',
      label: 'Sync enabled',
      tooltip: `${repo} syncs to its own sync repo${into ? ` (${mirroredBy})` : ''} — ${when} · ${OPEN_HINT}`,
    };
  }
  if (mirroredBy) {
    // No sync config of its own, but the whole-DB export means it is
    // mirrored anyway. Say so plainly — this is the state that used to
    // read "not configured" and prompted BACI-376.
    return {
      variant: 'enabled',
      label: 'Sync enabled',
      tooltip: `${repo} has no sync repo of its own, but every project on this machine is mirrored${into} — ${when} · ${OPEN_HINT}`,
    };
  }
  return {
    variant: 'unconfigured',
    label: 'Sync not set up',
    tooltip: `Nothing on this machine mirrors ${repo} — set up a sync repo to start · ${OPEN_HINT}`,
  };
}
