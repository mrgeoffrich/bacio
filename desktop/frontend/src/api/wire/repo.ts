// Repo-domain wire types + reshapers (BACI-369).
//
// ApiRepo itself still lives in ./sync.ts (a pre-existing misfiling —
// moving it would churn unrelated transports), so this module starts
// with the repo-activity shape GET /repos/activity serves.

import type { RepoActivity } from '../contract';

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
