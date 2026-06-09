// Pipeline-domain HTTP transport (BACI-359). Fetch wrappers + reshapers over
// the `bacio api` REST surface; the public ./api surface is the same as the
// Wails seam's. See ./client.http for the shared plumbing.
import { call } from './client.http';
import { cardFromIssue } from './wire/issue';
import type { ApiIssue } from './wire/issue';
import type { BoardCard, PipelineJob, ShippedListDTO } from './contract';

// ProcessSelection mirrors the lib/pipelineProcesses discriminated type
// (parallel shape — see the header note on why api.http.ts doesn't
// import). The cumulative-stepper picker sends an explicit stage list;
// the kept skip-Plan buttons send a preset slug.
type ProcessSelection = { stages: string[] } | { process: string };

export async function reorderCard(
  repoPrefix: string,
  key: string,
  position: number,
): Promise<BoardCard> {
  const iss = await call<ApiIssue>(`/repos/${repoPrefix}/issues/${key}/reorder`, {
    method: 'PUT',
    body: { key, position },
  });
  return cardFromIssue(iss);
}

// createRelation wires a `blocks` edge so `blocked` ends up blocked by
// `blocker` (the Pipeline drag-to-block gesture, BACI-342). A `blocks`
// edge is stored from = blocker, to = blocked — so the dragged card
// (which becomes blocked) is the `to`, and the drop target (the blocker)
// is the `from`. type is hard-coded to 'blocks'; the gesture only creates
// blocks/blocked-by, never relates-to/duplicate-of. The server's INSERT
// OR IGNORE makes a duplicate edge a silent no-op. The caller drives the
// optimistic badge update and re-asserts via a board refresh, so the
// created edge isn't returned.
export async function createRelation(
  repoPrefix: string,
  blocker: string,
  blocked: string,
): Promise<void> {
  await call<unknown>(`/repos/${repoPrefix}/relations`, {
    method: 'POST',
    body: { from: blocker, type: 'blocks', to: blocked },
  });
}

export async function setCardProcess(
  repoPrefix: string,
  key: string,
  selection: ProcessSelection,
): Promise<PipelineJob[]> {
  // Send exactly one of stages / process so the server's mutual-exclusion
  // guard sees the same shape the desktop seam does.
  const body: Record<string, unknown> = { key };
  if ('stages' in selection) body.stages = selection.stages;
  else body.process = selection.process;
  return await call<PipelineJob[]>(`/repos/${repoPrefix}/issues/${key}/process`, {
    method: 'POST',
    body,
  });
}

// editCardProcessTail edits the pending tail of an in_pipeline card's job
// chain (BACI-294). stages is the re-ordered pending tail only; the server
// reads the locked prefix (completed/running/cancelled jobs) from the
// store. Returns the refreshed chain.
export async function editCardProcessTail(
  repoPrefix: string,
  key: string,
  stages: string[],
): Promise<PipelineJob[]> {
  return await call<PipelineJob[]>(`/repos/${repoPrefix}/issues/${key}/process/tail`, {
    method: 'PUT',
    body: { key, stages },
  });
}

// resetCardProcess wipes an in_pipeline card's ENTIRE job chain (BACI-314)
// — including completed / cancelled history — so the card drops back to the
// from-scratch picker. Refused (409) by the server while a job is running.
// Returns the refreshed (empty) chain.
export async function resetCardProcess(
  repoPrefix: string,
  key: string,
): Promise<PipelineJob[]> {
  return await call<PipelineJob[]>(`/repos/${repoPrefix}/issues/${key}/process/reset`, {
    method: 'POST',
  });
}

export async function startCardJob(
  repoPrefix: string,
  key: string,
): Promise<PipelineJob[]> {
  return await call<PipelineJob[]>(`/repos/${repoPrefix}/issues/${key}/jobs/start`, {
    method: 'POST',
  });
}

export async function stopCardJob(
  repoPrefix: string,
  key: string,
): Promise<PipelineJob[]> {
  return await call<PipelineJob[]>(`/repos/${repoPrefix}/issues/${key}/jobs/stop`, {
    method: 'POST',
  });
}

export async function rerunCardJob(
  repoPrefix: string,
  key: string,
  seq: number,
): Promise<PipelineJob[]> {
  return await call<PipelineJob[]>(`/repos/${repoPrefix}/issues/${key}/jobs/${seq}/rerun`, {
    method: 'POST',
  });
}

export async function setEngineMode(
  repoPrefix: string,
  key: string,
  mode: string,
): Promise<BoardCard> {
  const iss = await call<ApiIssue>(`/repos/${repoPrefix}/issues/${key}/engine-mode`, {
    method: 'PUT',
    body: { key, mode },
  });
  return cardFromIssue(iss);
}

export async function shipCard(
  repoPrefix: string,
  key: string,
): Promise<BoardCard> {
  const iss = await call<ApiIssue>(`/repos/${repoPrefix}/issues/${key}/ship`, {
    method: 'POST',
  });
  return cardFromIssue(iss);
}

export async function markDoneCard(
  repoPrefix: string,
  key: string,
): Promise<BoardCard> {
  const iss = await call<ApiIssue>(`/repos/${repoPrefix}/issues/${key}/mark-done`, {
    method: 'POST',
  });
  return cardFromIssue(iss);
}

export async function setAutoShip(
  repoPrefix: string,
  enabled: boolean,
): Promise<boolean> {
  const out = await call<{ auto_ship: boolean }>(`/repos/${repoPrefix}/auto-ship`, {
    method: 'PUT',
    body: { enabled },
  });
  return !!out.auto_ship;
}

export async function getAutoShip(repoPrefix: string): Promise<boolean> {
  const out = await call<{ auto_ship: boolean }>(`/repos/${repoPrefix}/auto-ship`);
  return !!out.auto_ship;
}

export async function setBacklogCollapsed(
  repoPrefix: string,
  collapsed: boolean,
): Promise<boolean> {
  const out = await call<{ backlog_collapsed: boolean }>(`/repos/${repoPrefix}/backlog-collapsed`, {
    method: 'PUT',
    body: { collapsed },
  });
  return !!out.backlog_collapsed;
}

export async function getBacklogCollapsed(repoPrefix: string): Promise<boolean> {
  const out = await call<{ backlog_collapsed: boolean }>(`/repos/${repoPrefix}/backlog-collapsed`);
  return !!out.backlog_collapsed;
}

// setImpactPrimary / getImpactPrimary (BACI-349) front the per-repo
// Pipeline impact-primary display preference — clone of the
// backlog-collapsed pair above.
export async function setImpactPrimary(
  repoPrefix: string,
  impactPrimary: boolean,
): Promise<boolean> {
  const out = await call<{ impact_primary: boolean }>(`/repos/${repoPrefix}/impact-primary`, {
    method: 'PUT',
    body: { impact_primary: impactPrimary },
  });
  return !!out.impact_primary;
}

export async function getImpactPrimary(repoPrefix: string): Promise<boolean> {
  const out = await call<{ impact_primary: boolean }>(`/repos/${repoPrefix}/impact-primary`);
  return !!out.impact_primary;
}

// listShippedIssues (BACI-187, reshaped for BACI-221) is the HTTP
// twin of api.ts's listShippedIssues. Keep the parameter list and
// return type in lockstep with the desktop binding — the React-side
// ShippedPopover imports the same name from `./api` in both modes.
// `sinceDays === 0` means "Forever" (no ?since= parameter), so the
// server returns the unbounded count; a non-empty `sinceTs` (BACI-312) is
// the absolute local-midnight "Today" cutoff and wins over sinceDays,
// emitted as ?since_ts= (the two are mutually exclusive server-side).
export async function listShippedIssues(
  repoPrefix: string,
  sinceDays: number,
  sinceTs: string,
  limit: number,
): Promise<ShippedListDTO> {
  if (!repoPrefix || repoPrefix === 'all') {
    throw new Error('select a repository to view its shipping log');
  }
  const query: Record<string, string | number> = {};
  if (sinceTs) query.since_ts = sinceTs;
  else if (sinceDays > 0) query.since = `${sinceDays}d`;
  if (limit > 0) query.limit = limit;
  const body = await call<ShippedListDTO>(`/repos/${repoPrefix}/shipped`, { query });
  // Defensive defaults: the server always returns the wrapper, but on
  // an oddball 204 the call helper hands us undefined. Treat it as an
  // empty list with zero total so the popover's "showing N of TOTAL"
  // header always has something to render.
  return body ?? { rows: [], total: 0 };
}

// countShippedIssues (BACI-221) — HTTP twin of api.ts's
// countShippedIssues, polled on the same 10s cadence as the other live
// read endpoints so the Pipeline Shipping-column pill always reflects the active scope.
// `sinceTs` (BACI-312) is the absolute "Today" cutoff; it wins over the
// relative `sinceDays` window when present (mutually exclusive server-side).
export async function countShippedIssues(
  repoPrefix: string,
  sinceDays: number,
  sinceTs: string,
): Promise<number> {
  if (!repoPrefix || repoPrefix === 'all') {
    throw new Error('select a repository to view its shipping count');
  }
  const query: Record<string, string | number> = {};
  if (sinceTs) query.since_ts = sinceTs;
  else if (sinceDays > 0) query.since = `${sinceDays}d`;
  const body = await call<{ total: number }>(`/repos/${repoPrefix}/shipped/count`, { query });
  return body?.total ?? 0;
}
