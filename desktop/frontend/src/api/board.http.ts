// Board-domain HTTP transport (BACI-359). Fetch wrappers + reshapers over
// the `bacio api` REST surface; the public ./api surface is the same as the
// Wails seam's. See ./client.http for the shared plumbing.
import { call, WebModeUnavailableError } from './client.http';
import { STATE_LABELS } from './wire/common';
import { reshapeHistoryEntry } from './wire/history';
import { boardWithSync } from './wire/sync';
import type { ApiIssue } from './wire/issue';
import type { ApiHistoryEntry } from './wire/history';
import type { ApiRepo, SyncStatusApi } from './wire/sync';
import type { Board, BoardColumn, BoardCard, HistoryPage, AddRepositoryPayload } from './contract';

export async function listBoards(): Promise<Board[]> {
  // GET /repos returns the bare repo rows; the desktop's Board carries
  // an issue count and the BACI-89 background-sync status. Issue count
  // comes from a per-repo issue-list query; sync status comes from
  // GET /sync (one call, every repo) — no longer hardcoded false.
  const repos = await call<Array<{ prefix: string; name: string }>>('/repos');
  // Sync status is badge polish — a failure here must not break the
  // board picker, so fall back to an empty map.
  let syncByPrefix = new Map<string, SyncStatusApi>();
  try {
    const statuses = await call<SyncStatusApi[]>('/sync');
    syncByPrefix = new Map(statuses.map((s) => [s.prefix, s]));
  } catch {
    // ignore — boards still render, just without sync badges
  }
  const boards: Board[] = [];
  for (const r of repos) {
    const issues = await call<ApiIssue[]>(`/repos/${r.prefix}/issues`);
    boards.push(boardWithSync(r.prefix, r.name, issues.length, syncByPrefix.get(r.prefix)));
  }
  return boards;
}

export async function listColumns(): Promise<BoardColumn[]> {
  // Static — every state, in canonical order. No fetch.
  return Object.entries(STATE_LABELS).map(([state, label]) => ({ state, label }));
}

// getLaunchRepo returns the prefix of the repo the *server* process was
// launched from (BACI-368), or '' when it wasn't started inside one. In a
// cross-origin deployment that's the server's cwd repo, not the browser's —
// the only sensible reading.
export async function getLaunchRepo(): Promise<string> {
  return (await call<{ prefix: string }>('/launch-repo')).prefix;
}

export async function listCards(repoPrefix: string): Promise<BoardCard[]> {
  // The "all repos" pseudo-board isn't directly addressable over REST;
  // a v2 follow-up could add `GET /cards`. For now require a
  // concrete prefix in web mode.
  if (!repoPrefix || repoPrefix === 'all') {
    throw new Error('select a repository to view its board');
  }
  // BACI-60: the composite kanban endpoint emits BoardCard directly,
  // including the ActiveVerb / TodosDone / TodosTotal fields the
  // client-side cardFromIssue reshape couldn't see (they don't live
  // on model.Issue). Same wire shape as the Wails BoardService.ListCards.
  return await call<BoardCard[]>(`/repos/${repoPrefix}/cards`);
}

export async function addRepository(payload?: AddRepositoryPayload): Promise<Board> {
  // BACI-50: no browser folder picker, so the web bundle pops a
  // path-input modal and POSTs to /repos. An empty payload means the
  // caller forgot to gate this against WEB_MODE — surface clearly
  // rather than send a malformed body.
  if (!payload) {
    throw new WebModeUnavailableError('Add repository (no path provided)');
  }
  const body: Record<string, string> = {
    path: payload.path,
    name: payload.name,
  };
  if (payload.prefix) body.prefix = payload.prefix.toUpperCase();
  const repo = await call<ApiRepo>('/repos', { method: 'POST', body });
  // Match listBoards: issueCount=0 on a freshly-created repo. A
  // freshly-added repo almost never has sync configured yet — the
  // zero SyncStatusApi gives syncEnabled=false. The next listBoards
  // refresh picks up real sync status from GET /sync.
  return boardWithSync(repo.prefix, repo.name, 0, undefined);
}

// ApiProxyFQDNStat is the snake_case wire shape GET /proxy/stats returns
// (model.ProxyFQDNStat's JSON tags). listProxyStats reshapes it into the
// camelCase ProxyFQDNStat the Monitor screen consumes.
export async function listHistory(
  repoPrefix: string,
  page: number,
  pageSize: number,
): Promise<HistoryPage> {
  if (!repoPrefix || repoPrefix === 'all') {
    throw new Error('select a repository to view its history');
  }
  // Over-fetch by one so HasMore is derivable client-side — same trick
  // the Wails HistoryService uses.
  const rows = await call<ApiHistoryEntry[]>(`/repos/${repoPrefix}/history`, {
    query: { limit: pageSize + 1, offset: page * pageSize },
  });
  const hasMore = rows.length > pageSize;
  const entries = (hasMore ? rows.slice(0, pageSize) : rows).map(reshapeHistoryEntry);
  return { entries, page, pageSize, hasMore };
}
