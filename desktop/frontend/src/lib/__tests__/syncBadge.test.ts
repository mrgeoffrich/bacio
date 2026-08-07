import { describe, it, expect } from 'vitest';
import { syncBadgeState } from '../syncBadge';
import type { Board } from '../../api';

// BACI-376: the topbar badge's five variants. The case the ticket was
// filed over is a repo with no sync config of its own that is being
// mirrored anyway (the export is whole-DB) — it used to read "not
// configured" while Settings listed it as a linked member of the sync
// repo.

function board(over: Partial<Board> = {}): Board {
  return {
    prefix: 'OPER',
    name: 'operation-money-worries',
    kind: 'git',
    showAgentSurfaces: true,
    showKanban: false,
    issueCount: 0,
    syncEnabled: false,
    syncBackgroundEnabled: false,
    syncInProgress: false,
    ...over,
  };
}

describe('syncBadgeState', () => {
  it('reports a mirrored repo as enabled even with no sync config of its own', () => {
    const s = syncBadgeState(board({
      syncEnabled: false,
      syncMirroredBy: 'bacio-sync',
      syncBackgroundEnabled: true,
      syncLastAt: '2026-08-05T21:14:35Z',
    }));
    expect(s.variant).toBe('enabled');
    // Explains the mechanism, so it reconciles with Settings listing
    // OPER as a member of bacio-sync rather than as unsynced.
    expect(s.tooltip).toContain('no sync repo of its own');
    expect(s.tooltip).toContain('bacio-sync');
    expect(s.tooltip).toContain('last synced');
  });

  it('reports not-set-up only when nothing mirrors the repo', () => {
    const s = syncBadgeState(board({ syncEnabled: false, syncBackgroundEnabled: true }));
    expect(s.variant).toBe('unconfigured');
    expect(s.tooltip).toContain('Nothing on this machine mirrors OPER');
  });

  it('reports paused when the repo is mirrored but the global toggle is off', () => {
    const s = syncBadgeState(board({ syncEnabled: true, syncBackgroundEnabled: false }));
    expect(s.variant).toBe('paused');
    expect(s.label).toBe('Sync paused');
    expect(s.tooltip).toContain('turned off for every repo');
  });

  it('pauses a mirrored-only repo too — the ticker is what stopped', () => {
    const s = syncBadgeState(board({
      syncEnabled: false,
      syncMirroredBy: 'bacio-sync',
      syncBackgroundEnabled: false,
    }));
    expect(s.variant).toBe('paused');
    expect(s.tooltip).toContain('bacio-sync');
  });

  it('folds a stale error into the paused tooltip rather than showing failed', () => {
    const s = syncBadgeState(board({
      syncEnabled: true,
      syncBackgroundEnabled: false,
      syncLastError: 'push rejected',
    }));
    expect(s.variant).toBe('paused');
    expect(s.tooltip).toContain('push rejected');
  });

  it('reports enabled with the last-synced time when the ticker is live', () => {
    const s = syncBadgeState(board({
      syncEnabled: true,
      syncBackgroundEnabled: true,
      syncLastAt: '2026-08-05T21:14:35Z',
    }));
    expect(s.variant).toBe('enabled');
    expect(s.tooltip).toContain('last synced');
  });

  it('reports enabled-but-never-run when there is no last-sync time', () => {
    const s = syncBadgeState(board({ syncEnabled: true, syncBackgroundEnabled: true }));
    expect(s.variant).toBe('enabled');
    expect(s.tooltip).toContain('no run has completed yet');
  });

  it('surfaces a failure over the enabled state', () => {
    const s = syncBadgeState(board({
      syncEnabled: true,
      syncBackgroundEnabled: true,
      syncLastError: 'push rejected',
    }));
    expect(s.variant).toBe('error');
    expect(s.tooltip).toContain('push rejected');
  });

  it('in-progress outranks every other state', () => {
    const s = syncBadgeState(board({
      syncEnabled: true,
      syncBackgroundEnabled: true,
      syncInProgress: true,
      syncLastError: 'push rejected',
    }));
    expect(s.variant).toBe('syncing');
  });

  it('falls back to repo-neutral copy before the boards have loaded', () => {
    const s = syncBadgeState(undefined);
    expect(s.variant).toBe('unconfigured');
    expect(s.tooltip).toContain('this repo');
  });
});
