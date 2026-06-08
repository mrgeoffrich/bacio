import { describe, it, expect } from 'vitest';
import {
  boardWithSync,
  reshapeSyncRegistry,
  reshapeSyncSetup,
  reshapeRepoLinkResult,
} from '../sync';

// BACI-358: sync-domain reshapers, testable now they live outside
// api.http.ts. Cover the SyncStatus→Board fold, the registry snake→camel
// rename, the sync-setup init/clone/collision branches, and the
// phantom-link fallback to the call arguments.

describe('boardWithSync', () => {
  it('folds a present sync status into the Board badge fields', () => {
    const b = boardWithSync('BACI', 'bacio', 12, {
      prefix: 'BACI', configured: true, background_enabled: true, in_progress: true,
      last_sync_at: 's', last_error: 'e',
    });
    expect(b).toEqual({
      prefix: 'BACI',
      name: 'bacio',
      issueCount: 12,
      syncEnabled: true,
      syncInProgress: true,
      syncLastAt: 's',
      syncLastError: 'e',
    });
  });

  it('defaults the badges off when sync status is undefined', () => {
    const b = boardWithSync('X', 'x', 0, undefined);
    expect(b.syncEnabled).toBe(false);
    expect(b.syncInProgress).toBe(false);
    expect(b.syncLastAt).toBeUndefined();
  });
});

describe('reshapeSyncRegistry', () => {
  it('renames every field 1:1 across both slices', () => {
    const r = reshapeSyncRegistry({
      sync_repos: [{
        remote_url: 'git@x', label: 'L', local_path: '/p', cloned_at: 'c',
        last_sync_at: 's', last_error: 'e', in_progress: false,
        projects: [{ prefix: 'BACI', name: 'bacio', uuid: 'u', status: 'linked' }],
      }],
      unsynced_projects: [{ prefix: 'OTH', name: 'other', uuid: 'u2', path: '/o' }],
    });
    expect(r.syncRepos[0].remoteUrl).toBe('git@x');
    expect(r.syncRepos[0].localPath).toBe('/p');
    expect(r.syncRepos[0].projects[0]).toEqual({ prefix: 'BACI', name: 'bacio', uuid: 'u', status: 'linked' });
    expect(r.unsyncedProjects[0]).toEqual({ prefix: 'OTH', name: 'other', uuid: 'u2', path: '/o' });
  });
});

describe('reshapeSyncSetup', () => {
  it('maps the init branch fields', () => {
    const out = reshapeSyncSetup({
      mode: 'init',
      init: { local_path: '/p', remote: 'r', commit_sha: 'sha', pushed: true, attached: false },
    });
    expect(out).toEqual({ mode: 'init', localPath: '/p', remote: 'r', commitSHA: 'sha', pushed: true, attached: false });
  });

  it('maps the clone branch and ignores init', () => {
    const out = reshapeSyncSetup({ mode: 'clone', clone: { local_path: '/c', remote: 'r' } });
    expect(out).toEqual({ mode: 'clone', localPath: '/c', remote: 'r' });
  });

  it('maps the 409 collision preview', () => {
    const out = reshapeSyncSetup({
      mode: 'init',
      preview_collisions: {
        renumbered: [{ prefix: 'BACI', uuid: 'u', old_number: 1, new_number: 9 }],
        renamed: [{ kind: 'feature', uuid: 'u2', old: 'a', new: 'b' }],
      },
    });
    expect(out.previewCollisions?.renumbered).toEqual([{ prefix: 'BACI', oldNumber: 1, newNumber: 9, uuid: 'u' }]);
    expect(out.previewCollisions?.renamed).toEqual([{ kind: 'feature', old: 'a', new: 'b', uuid: 'u2' }]);
  });
});

describe('reshapeRepoLinkResult', () => {
  it('uses the server echo when present', () => {
    const r = reshapeRepoLinkResult(
      { repo: { prefix: 'BACI', path: '/srv' }, sync_remote_url: 'git@x', already_linked: true },
      'FALLBACK', '/fallback',
    );
    expect(r).toEqual({ prefix: 'BACI', path: '/srv', syncRemoteUrl: 'git@x', alreadyLinked: true });
  });

  it('falls back to the call arguments on a sparse body', () => {
    const r = reshapeRepoLinkResult(
      { repo: undefined as unknown as { prefix: string; path: string }, sync_remote_url: '' },
      'BACI', '/path',
    );
    expect(r.prefix).toBe('BACI');
    expect(r.path).toBe('/path');
    expect(r.syncRemoteUrl).toBe('');
    expect(r.alreadyLinked).toBe(false);
  });
});
