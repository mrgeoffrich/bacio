// Repo-domain wire types + reshapers (BACI-369).
//
// ApiRepo itself still lives in ./sync.ts (a pre-existing misfiling —
// moving it would churn unrelated transports), so this module starts
// with the repo-activity shape GET /repos/activity serves.

import type { RepoActivity, RepoKind } from '../contract';

// normalizeRepoKind narrows the wire's free-form `kind` string onto the
// contract's RepoKind union. Both transports carry it as a plain string
// (the Wails binding types it `string`; the HTTP wire is model.Repo's
// `kind`), and legacy rows written before the pivot can still hold ''.
// Anything that isn't the workspace discriminator reads as 'git' — the
// historical default and the safe fallback, since a mis-typed kind must
// never hide the Agentic Pipeline nav on a real git repo.
//
// Both transports funnel through here so the narrowing decision exists
// once. It is deliberately a literal comparison, not a Wails enum member:
// enum members are erased in the web build.
export function normalizeRepoKind(kind: string | undefined): RepoKind {
  return kind === 'workspace' ? 'workspace' : 'git';
}

// ApiRepoActivity mirrors api.RepoActivityOut — the snake_case wire
// shape of the BACI-369 GET /repos/activity endpoint.
export interface ApiRepoActivity {
  prefix: string;
  last_activity_at?: string;
  active_jobs: number;
}

// reshapeRepoActivity maps one wire row into the camelCase DTO. Absent
// last_activity_at stays absent — the picker's comparator sorts those
// repos last rather than treating them as epoch-old.
export function reshapeRepoActivity(a: ApiRepoActivity): RepoActivity {
  return {
    prefix: a.prefix,
    lastActivityAt: a.last_activity_at,
    activeJobs: a.active_jobs,
  };
}
