// Sync-domain HTTP transport (BACI-359). Fetch wrappers + reshapers over
// the `bacio api` REST surface; the public ./api surface is the same as the
// Wails seam's. See ./client.http for the shared plumbing.
import { call, readActor, readToken, API_BASE, preferencePair } from './client.http';
import { reshapeSyncRegistry, reshapeSyncSetup, reshapeRepoLinkResult } from './wire/sync';
import type { SyncRegistryApi, SyncSetupApi, RepoLinkResultApi } from './wire/sync';
import type { SyncRegistry, SyncSetupPayload, CollisionPreviewDTO, SyncSetupDTO, SyncSetupResult, RepoLinkResult, SyncPreferences } from './contract';

export class SyncSetupCollisionError extends Error {
  previewCollisions: CollisionPreviewDTO;
  result: SyncSetupDTO;
  constructor(result: SyncSetupDTO) {
    super('sync setup: renumber collision');
    this.name = 'SyncSetupCollisionError';
    this.result = result;
    // Non-null assert: caller only constructs this on a 409 body
    // that has preview_collisions populated.
    this.previewCollisions = result.previewCollisions!;
  }
}

// ---------- Sync preference pair (BACI-89 / 108) ----------
// Collapsed behind preferencePair (BACI-359), same shape as the global pairs.

const syncPrefs = preferencePair<{ background_enabled: boolean }, SyncPreferences>(
  '/settings/sync-preferences', (w) => ({ backgroundEnabled: w.background_enabled }));
export const getSyncPreferences = (): Promise<SyncPreferences> => syncPrefs.get();
export const setSyncPreferences = (backgroundEnabled: boolean): Promise<SyncPreferences> =>
  syncPrefs.set({ background_enabled: backgroundEnabled });

// getSyncRegistry fetches BACI-107's GET /sync/repos and reshapes the
// snake-case wire payload to the camelCase SyncRegistry the React
// tree consumes (see reshapeSyncRegistry in ./api/wire/sync).
export async function getSyncRegistry(): Promise<SyncRegistry> {
  const wire = await call<SyncRegistryApi>('/sync/repos');
  return reshapeSyncRegistry(wire);
}

// SyncSetupApi mirrors the snake-case api.SyncSetupOut wire shape.
// Init/Clone are the engine result structs — only the per-mode field
// for the chosen mode is populated. preview_collisions is set on the
// 409 path only.
export async function setupSync(
  prefix: string,
  payload: SyncSetupPayload,
): Promise<SyncSetupResult> {
  if (!prefix) throw new Error('sync setup: repo prefix is required');
  // Snake-case the body inline rather than reuse call() — call() throws
  // on every non-2xx and we need to decode the 409 body verbatim.
  const body: Record<string, unknown> = { mode: payload.mode };
  if (payload.remote !== undefined) body.remote = payload.remote;
  if (payload.localPath !== undefined) body.local_path = payload.localPath;
  if (payload.allowRenumber) body.allow_renumber = true;

  const url = new URL(
    `${API_BASE}/repos/${encodeURIComponent(prefix)}/sync/setup`,
    window.location.origin,
  );
  const headers: Record<string, string> = {
    'X-Actor': readActor(),
    'Content-Type': 'application/json',
  };
  const token = readToken();
  if (token) headers['Authorization'] = `Bearer ${token}`;
  const res = await fetch(url.toString(), {
    method: 'POST',
    headers,
    body: JSON.stringify(body),
  });
  const text = await res.text();
  if (res.status === 200) {
    if (!text) throw new Error('sync setup: empty 200 body');
    const wire = JSON.parse(text) as SyncSetupApi;
    return reshapeSyncSetup(wire);
  }
  if (res.status === 409) {
    if (!text) throw new Error('sync setup: empty 409 body');
    const wire = JSON.parse(text) as SyncSetupApi;
    throw new SyncSetupCollisionError(reshapeSyncSetup(wire));
  }
  // Any other non-2xx — fall through to the same envelope handling
  // call() does, so the modal surfaces the server's human message.
  let msg = `${res.status} ${res.statusText}`;
  if (text) {
    try {
      const parsed = JSON.parse(text);
      if (parsed?.error) msg = parsed.error;
    } catch { msg = text; }
  }
  throw new Error(msg);
}

// linkPhantomRepo (BACI-112) — POST /repos/{prefix}/link with body
// {path: ...}. Mirrors the Wails-bound seam in api.ts. Errors come
// back as plain Error from call(); the caller renders the human
// message inline.
export async function linkPhantomRepo(
  prefix: string,
  path: string,
): Promise<RepoLinkResult> {
  if (!prefix) throw new Error('repo link: prefix is required');
  const res = await call<RepoLinkResultApi>(
    `/repos/${encodeURIComponent(prefix)}/link`,
    { method: 'POST', body: { path } },
  );
  return reshapeRepoLinkResult(res, prefix, path);
}
