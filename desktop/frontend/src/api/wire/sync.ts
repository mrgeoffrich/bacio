// Sync-domain wire types + reshapers (BACI-358).
//
// The snake_case `Api*` shapes mirror api.SyncStatusOut / SyncRegistryOut /
// SyncSetupOut and the phantom-link result as the `bacio api` server
// serialises them; the reshapers map them into the camelCase DTOs the
// Sync / board surfaces consume. boardWithSync also lives here since it
// folds a SyncStatusApi into a Board. Moved out of api.http.ts so the
// reshapes are unit testable — see ./issue.ts for the pattern + the
// Phase 2b note.

import { normalizeRepoKind } from './repo';
import type {
  Board,
  SyncRegistry,
  SyncRepoEntry,
  MemberProject,
  UnsyncedProject,
  SyncSetupDTO,
  RepoLinkResult,
} from '../contract';

// SyncStatusApi mirrors api.SyncStatusOut — the wire shape of the
// BACI-89 GET /sync endpoint.
export interface SyncStatusApi {
  prefix: string;
  configured: boolean;
  mirrored_by?: string;
  background_enabled: boolean;
  in_progress: boolean;
  last_sync_at?: string;
  last_error?: string;
  remote?: string;
}

// SyncRegistryApi / SyncRepoApi / MemberProjectApi / UnsyncedProjectApi
// mirror api.SyncRegistryOut and friends — the wire shape of the
// BACI-107 GET /sync/repos endpoint. Snake-case on the wire matches
// every other wire DTO; reshapeSyncRegistry below reshapes to the
// camelCase SyncRegistry the React tree consumes.
export interface SyncRegistryApi {
  sync_repos: SyncRepoApi[];
  unsynced_projects: UnsyncedProjectApi[];
}

export interface SyncRepoApi {
  remote_url: string;
  label: string;
  local_path: string;
  cloned_at: string;
  last_sync_at?: string;
  last_error?: string;
  in_progress: boolean;
  projects: MemberProjectApi[];
}

export interface MemberProjectApi {
  prefix: string;
  name: string;
  uuid?: string;
  // 'workspace' joined the enum with the pivot: a workspace row in a sync
  // repo is pathless like a phantom, but it is fully present and being
  // mirrored rather than waiting to be linked, so the two must not be
  // conflated. See internal/sync/membership.go's MembershipStatus.
  status: 'linked' | 'phantom' | 'absent' | 'workspace';
}

export interface UnsyncedProjectApi {
  prefix: string;
  name: string;
  uuid: string;
  path: string;
}

// SyncSetupApi mirrors the snake-case api.SyncSetupOut wire shape.
// Init/Clone are the engine result structs — only the per-mode field
// for the chosen mode is populated. preview_collisions is set on the
// 409 path only.
export interface SyncSetupApi {
  mode: string;
  init?: {
    local_path?: string;
    remote?: string;
    commit_sha?: string;
    pushed?: boolean;
    attached?: boolean;
  };
  clone?: {
    local_path?: string;
    remote?: string;
  };
  preview_collisions?: {
    renumbered?: Array<{
      prefix: string;
      uuid: string;
      old_number: number;
      new_number: number;
    }>;
    renamed?: Array<{
      kind: string;
      prefix?: string;
      uuid: string;
      old: string;
      new: string;
    }>;
  };
}

export interface RepoLinkResultApi {
  repo: { prefix: string; path: string };
  sync_remote_url: string;
  already_linked?: boolean;
  would_link?: boolean;
}

// ApiRepo is the wire shape POST /repos, POST /workspaces and GET /repos
// return (model.Repo) — the bare repo row the web bundle's addRepository /
// addWorkspace / listBoards fold into a Board via boardWithSync. `kind`
// has no omitempty server-side, so it is always on the wire; it is typed
// as a plain string here and narrowed by normalizeRepoKind.
export interface ApiRepo {
  prefix: string;
  name: string;
  kind: string;
  path: string;
}

// boardWithSync folds a SyncStatusApi (possibly undefined) into a
// Board. Centralised so listBoards, addRepository and addWorkspace stay in
// lockstep. `kind` is the raw wire string; the narrowing to the RepoKind
// union happens here so no caller has to remember it.
export function boardWithSync(
  prefix: string,
  name: string,
  kind: string,
  issueCount: number,
  sync: SyncStatusApi | undefined,
): Board {
  return {
    prefix,
    name,
    kind: normalizeRepoKind(kind),
    issueCount,
    syncEnabled: sync?.configured ?? false,
    syncBackgroundEnabled: sync?.background_enabled ?? false,
    syncMirroredBy: sync?.mirrored_by,
    syncInProgress: sync?.in_progress ?? false,
    syncLastAt: sync?.last_sync_at,
    syncLastError: sync?.last_error,
  };
}

export function reshapeMemberProject(m: MemberProjectApi): MemberProject {
  return { prefix: m.prefix, name: m.name, uuid: m.uuid, status: m.status };
}

export function reshapeUnsyncedProject(u: UnsyncedProjectApi): UnsyncedProject {
  return { prefix: u.prefix, name: u.name, uuid: u.uuid, path: u.path };
}

export function reshapeSyncRepo(r: SyncRepoApi): SyncRepoEntry {
  return {
    remoteUrl: r.remote_url,
    label: r.label,
    localPath: r.local_path,
    clonedAt: r.cloned_at,
    lastSyncAt: r.last_sync_at,
    lastError: r.last_error,
    inProgress: r.in_progress,
    projects: r.projects.map(reshapeMemberProject),
  };
}

// reshapeSyncRegistry reshapes GET /sync/repos's snake-case payload into
// the camelCase SyncRegistry the React tree consumes. The reshape is
// mechanical — every field is renamed 1:1 — so it stays a thin map() over
// the two slices.
export function reshapeSyncRegistry(wire: SyncRegistryApi): SyncRegistry {
  return {
    syncRepos: wire.sync_repos.map(reshapeSyncRepo),
    unsyncedProjects: wire.unsynced_projects.map(reshapeUnsyncedProject),
  };
}

export function reshapeSyncSetup(wire: SyncSetupApi): SyncSetupDTO {
  const out: SyncSetupDTO = { mode: wire.mode };
  if (wire.init) {
    out.localPath = wire.init.local_path;
    out.remote = wire.init.remote;
    out.commitSHA = wire.init.commit_sha;
    out.pushed = wire.init.pushed;
    out.attached = wire.init.attached;
  } else if (wire.clone) {
    out.localPath = wire.clone.local_path;
    out.remote = wire.clone.remote;
  }
  if (wire.preview_collisions) {
    out.previewCollisions = {
      renumbered: (wire.preview_collisions.renumbered ?? []).map(r => ({
        prefix: r.prefix,
        oldNumber: r.old_number,
        newNumber: r.new_number,
        uuid: r.uuid,
      })),
      renamed: (wire.preview_collisions.renamed ?? []).map(r => ({
        kind: r.kind,
        old: r.old,
        new: r.new,
        uuid: r.uuid,
      })),
    };
  }
  return out;
}

// reshapeRepoLinkResult maps the POST /repos/{prefix}/link response into
// the camelCase RepoLinkResult. The fallback prefix / path are the call
// arguments — the server echoes them, but defend against a sparse body.
export function reshapeRepoLinkResult(
  res: RepoLinkResultApi,
  prefix: string,
  path: string,
): RepoLinkResult {
  return {
    prefix: res.repo?.prefix ?? prefix,
    path: res.repo?.path ?? path,
    syncRemoteUrl: res.sync_remote_url ?? '',
    alreadyLinked: !!res.already_linked,
  };
}
