// Issue-domain HTTP transport (BACI-359). Fetch wrappers + reshapers over
// the `bacio api` REST surface; the public ./api surface is the same as the
// Wails seam's. See ./client.http for the shared plumbing.
import { call, readActor } from './client.http';
import { cardFromIssue, reshapeApiBrief, reshapeIssueView } from './wire/issue';
import type { ApiIssue, ApiIssueView, ApiIssueBrief, ApiBoardCard } from './wire/issue';
import type { BoardCard, PRDTO, IssueDetail, IssueBriefDTO } from './contract';

export async function getIssue(repoPrefix: string, key: string): Promise<IssueDetail> {
  if (!repoPrefix || repoPrefix === 'all') {
    // The REST API requires a concrete prefix in the path; canonical
    // issue keys (PREFIX-N) already carry it, so split it back out.
    const i = key.lastIndexOf('-');
    if (i <= 0) throw new Error(`invalid issue key: ${key}`);
    repoPrefix = key.slice(0, i);
  }
  const view = await call<ApiIssueView>(`/repos/${repoPrefix}/issues/${key}`);
  return reshapeIssueView(view);
}

export async function getIssueBrief(repoPrefix: string, key: string): Promise<IssueBriefDTO> {
  if (!repoPrefix || repoPrefix === 'all') {
    const i = key.lastIndexOf('-');
    if (i <= 0) throw new Error(`invalid issue key: ${key}`);
    repoPrefix = key.slice(0, i);
  }
  const view = await call<ApiIssueBrief>(`/repos/${repoPrefix}/issues/${key}/brief`);
  return reshapeApiBrief(view);
}

// attachPullRequest POSTs to /repos/{prefix}/issues/{key}/pull-requests.
// Validation failures (bad scheme / missing host) come back through the
// {error} envelope and surface to the caller as Error messages.
export async function attachPullRequest(
  repoPrefix: string,
  key: string,
  url: string,
): Promise<PRDTO> {
  if (!repoPrefix || repoPrefix === 'all') {
    const i = key.lastIndexOf('-');
    if (i <= 0) throw new Error(`invalid issue key: ${key}`);
    repoPrefix = key.slice(0, i);
  }
  const raw = await call<{ url: string }>(
    `/repos/${repoPrefix}/issues/${key}/pull-requests`,
    { method: 'POST', body: { url } },
  );
  return { url: raw.url };
}

// addIssue (BACI-166) creates a new issue via POST /repos/{prefix}/issues
// and reshapes the returned model.Issue into a BoardCard so the React
// composer can prepend it to the kanban without a second round-trip.
// Cross-transport name parity with api.ts so the composer doesn't
// branch on transport. Validation (empty title, invalid state, etc.)
// lives at the store boundary; the server surfaces it as the standard
// error envelope and call() throws an Error whose .message carries the
// human-readable text the composer renders inline.
export async function addIssue(
  repoPrefix: string,
  title: string,
  description: string,
  featureSlug = '',
): Promise<BoardCard> {
  if (!repoPrefix || repoPrefix === 'all') {
    throw new Error('addIssue: a repo prefix is required (cross-repo pseudo-board has no target)');
  }
  // feature_slug (Phase 4): empty defers to the repo default feature at
  // the store boundary (ResolveCreateIssueFeatureID). The handler decodes
  // the full IssueAddInput, so the field rides straight through.
  const body: { title: string; description: string; feature_slug?: string } = { title, description };
  if (featureSlug) body.feature_slug = featureSlug;
  const iss = await call<ApiIssue>(
    `/repos/${repoPrefix}/issues`,
    { method: 'POST', body },
  );
  return cardFromIssue(iss);
}

export async function setIssueState(
  repoPrefix: string,
  key: string,
  state: string,
): Promise<BoardCard> {
  if (!repoPrefix || repoPrefix === 'all') {
    const i = key.lastIndexOf('-');
    if (i <= 0) throw new Error(`invalid issue key: ${key}`);
    repoPrefix = key.slice(0, i);
  }
  const iss = await call<ApiBoardCard>(`/repos/${repoPrefix}/issues/${key}/state`, {
    method: 'PUT',
    body: { state },
  });
  return cardFromIssue(iss);
}

export async function updateIssueDescription(
  repoPrefix: string,
  key: string,
  description: string,
): Promise<IssueDetail> {
  if (!repoPrefix || repoPrefix === 'all') {
    const i = key.lastIndexOf('-');
    if (i <= 0) throw new Error(`invalid issue key: ${key}`);
    repoPrefix = key.slice(0, i);
  }
  await call<unknown>(`/repos/${repoPrefix}/issues/${key}`, {
    method: 'PATCH',
    body: { description },
  });
  return getIssue(repoPrefix, key);
}

export async function updateIssueTitle(
  repoPrefix: string,
  key: string,
  title: string,
): Promise<IssueDetail> {
  if (!repoPrefix || repoPrefix === 'all') {
    const i = key.lastIndexOf('-');
    if (i <= 0) throw new Error(`invalid issue key: ${key}`);
    repoPrefix = key.slice(0, i);
  }
  await call<unknown>(`/repos/${repoPrefix}/issues/${key}`, {
    method: 'PATCH',
    body: { title },
  });
  return getIssue(repoPrefix, key);
}

// updateIssueCustomerImpact (BACI-349) PATCHes the issue's one-line
// customer impact. Unlike the title an empty value is legitimate — it
// clears the field back to the "no impact" state — so it's always sent
// as a present `customer_impact` key (empty = clear).
export async function updateIssueCustomerImpact(
  repoPrefix: string,
  key: string,
  customerImpact: string,
): Promise<IssueDetail> {
  if (!repoPrefix || repoPrefix === 'all') {
    const i = key.lastIndexOf('-');
    if (i <= 0) throw new Error(`invalid issue key: ${key}`);
    repoPrefix = key.slice(0, i);
  }
  await call<unknown>(`/repos/${repoPrefix}/issues/${key}`, {
    method: 'PATCH',
    body: { customer_impact: customerImpact },
  });
  return getIssue(repoPrefix, key);
}

export async function addComment(
  repoPrefix: string,
  key: string,
  author: string,
  body: string,
  opts?: { eval?: boolean; transcriptEventRef?: string },
): Promise<IssueDetail> {
  if (!repoPrefix || repoPrefix === 'all') {
    const i = key.lastIndexOf('-');
    if (i <= 0) throw new Error(`invalid issue key: ${key}`);
    repoPrefix = key.slice(0, i);
  }
  const effectiveAuthor = author?.trim() || readActor() || 'web';
  const reqBody: {
    author: string;
    body: string;
    eval?: boolean;
    transcript_event_ref?: string;
  } = {
    author: effectiveAuthor,
    body,
  };
  if (opts?.eval) reqBody.eval = true;
  if (opts?.transcriptEventRef) reqBody.transcript_event_ref = opts.transcriptEventRef;
  await call<unknown>(`/repos/${repoPrefix}/issues/${key}/comments`, {
    method: 'POST',
    body: reqBody,
  });
  return getIssue(repoPrefix, key);
}

export async function deleteComment(
  repoPrefix: string,
  key: string,
  commentUUID: string,
): Promise<IssueDetail> {
  if (!repoPrefix || repoPrefix === 'all') {
    const i = key.lastIndexOf('-');
    if (i <= 0) throw new Error(`invalid issue key: ${key}`);
    repoPrefix = key.slice(0, i);
  }
  await call<unknown>(
    `/repos/${repoPrefix}/issues/${key}/comments/${commentUUID}`,
    { method: 'DELETE' },
  );
  return getIssue(repoPrefix, key);
}

export async function archiveIssue(prefix: string, key: string): Promise<unknown> {
  return call(`/repos/${encodeURIComponent(prefix)}/issues/${encodeURIComponent(key)}/archive`, { method: 'POST' });
}

export async function unarchiveIssue(prefix: string, key: string): Promise<unknown> {
  return call(`/repos/${encodeURIComponent(prefix)}/issues/${encodeURIComponent(key)}/unarchive`, { method: 'POST' });
}
