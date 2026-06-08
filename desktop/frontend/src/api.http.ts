// Fetch-based variant of api.ts that talks to a remote `bacio api`
// server instead of Wails-bound services. Vite swaps this in for the
// real api.ts when MODE === 'web' — see vite.config.ts.
//
// The exported surface mirrors api.ts one-for-one. Each function
// either (a) wraps a REST call and reshapes the API's record types
// into the desktop DTOs (BoardCard, IssueDetail, ...) the React
// components expect, or (b) is a hide-in-v1 stub that throws
// WebModeUnavailableError so callers that slip through component
// gating get a clear, surfaceable failure.
//
// Type declarations are *parallel* to the Wails-generated bindings
// rather than imported — the bindings live under `frontend/bindings/`
// and reach for `@wailsio/runtime` at module load, which doesn't exist
// in a browser bundle. We can't lean on `import type` either because
// the source files themselves contain runtime code. The shapes here
// are the contract; if they drift from boardservice.go / docservice.go
// / featureservice.go / historyservice.go / settingsservice.go, the
// component layer will break in a way TypeScript can't catch in this
// directory. Keep them in sync.

// ---------- DTO shapes ----------
//
// The camelCase DTOs both transports expose now live in the shared,
// runtime-free ./api/contract module (BACI-359). They're imported here for
// the `call<T>` generics + function return annotations and re-exported so
// the public ./api surface stays byte-identical. The snake_case `Api*`
// wire shapes + the reshapers live under ./api/wire/* (BACI-358).

import type {
  Board,
  BoardCard,
  BoardColumn,
  IssueDetail,
  IssueBriefDTO,
  PRDTO,
  AgentCard,
  DispatchDTO,
  DocSummary,
  DocContent,
  FeatureSummary,
  FeatureDetail,
  FeaturePlan,
  HistoryPage,
  LeaderStatusDTO,
  PromptTemplateDTO,
  Notification,
  PipelineJob,
  ShippedListDTO,
  SyncRegistry,
  SyncPreferences,
  SyncSetupPayload,
  SyncSetupResult,
  SyncSetupDTO,
  CollisionPreviewDTO,
  RepoLinkResult,
  AddRepositoryPayload,
  SessionQuestionRow,
  DisplayPreferencesDTO,
  ArchivePreferencesDTO,
  AudioPreferencesDTO,
  TimezonePreferencesDTO,
  DefaultFeatureDTO,
  BoardHiddenStatesDTO,
} from './api/contract';

export type {
  Board,
  SyncRegistry,
  SyncRegistryDTO,
  SyncRepoEntry,
  SyncRepoDTO,
  MemberProject,
  MemberProjectDTO,
  UnsyncedProject,
  UnsyncedProjectDTO,
  BoardColumn,
  BoardCardTodo,
  BoardCardBlocker,
  WaitingKind,
  WaitingState,
  LatestPlan,
  LatestPlanDTO,
  BoardCard,
  BoardCardLatestPR,
  BoardCardQuestion,
  BoardCardJob,
  PipelineJob,
  ShippedIssueDTO,
  ShippedListDTO,
  CommentDTO,
  PRDTO,
  DocLinkDTO,
  ClaimantDTO,
  IssueDetail,
  IssueMetaDTO,
  LinkedDocDTO,
  FeatureRefDTO,
  RelationDTO,
  RelationsDTO,
  IssueBriefDTO,
  ClaimDTO,
  DispatchDTO,
  SessionTodoDTO,
  QuestionDTO,
  SessionQuestionPayload,
  SessionQuestionItem,
  SessionQuestionOption,
  SessionQuestionRow,
  Notification,
  AgentCard,
  DocSummaryLink,
  DocSummary,
  DocContent,
  FeatureSummary,
  FeatureLinkedIssue,
  FeatureCommentDTO,
  FeatureLinkedDoc,
  FeaturePlanEntry,
  FeaturePlan,
  FeatureDetail,
  HistoryEntryDTO,
  HistoryPage,
  LeaderStatusDTO,
  PromptTemplateDTO,
  AddRepositoryPayload,
  SyncSetupPayload,
  RenumberEntryDTO,
  RenameEntryDTO,
  CollisionPreviewDTO,
  SyncSetupDTO,
  SyncSetupResult,
  RepoLinkResult,
  RepoLinkResultDTO,
  SyncPreferences,
  DisplayPreferencesDTO,
  ArchivePreferencesDTO,
  AudioPreferencesDTO,
  TimezonePreferencesDTO,
  DefaultFeatureDTO,
  BoardHiddenStatesDTO,
} from './api/contract';

// BACI-304: re-export the Monitor screen's per-FQDN stats shape from the
// shared (browser-safe, runtime-free) lib so MonitorView imports the same
// `ProxyFQDNStat` name from `./api` in both transports. api.ts re-exports
// the Wails-binding equivalent under the same name (the BACI-108 split).
// Imported (not just re-exported) so listProxyStats below can annotate
// its reshape return with the type.
import type { ProxyFQDNStat } from './lib/proxyStats';
export type { ProxyFQDNStat };

// BACI-308: the same cross-transport split for the Monitor capture drill-down
// shapes — the list row, the parsed capture detail, and the job transcript.
// api.ts re-exports the Wails-binding equivalents under the same names.
import type { ProxyCaptureRow, ProxyMessage, AnthropicTranscript } from './lib/proxyCaptures';
export type { ProxyCaptureRow, ProxyMessage, AnthropicTranscript };
// BACI-322: the transcript browser's row-per-dispatch shape — same
// cross-transport split. api.ts re-exports the Wails JobTranscriptRowDTO under
// this name; this twin reshapes the snake_case wire row into it.
import type { JobTranscriptRow } from './lib/proxyCaptures';
export type { JobTranscriptRow };

// BACI-358: the snake_case `Api*` wire shapes and the pure reshapers that
// map them into the camelCase DTOs above now live in per-domain modules
// under ./api/wire/. We import the types (for the `call<T>` generics) and
// the reshapers (for the fetch wrappers) from there; the inline reshapes
// left in this file lean on the shared label/assignee helpers in
// ./api/wire/common.
import { STATE_LABELS } from './api/wire/common';
import {
  cardFromIssue,
  reshapeApiBrief,
  reshapeIssueView,
} from './api/wire/issue';
import type {
  ApiIssue,
  ApiIssueView,
  ApiIssueBrief,
  ApiBoardCard,
} from './api/wire/issue';
import {
  reshapeFeatureSummary,
  reshapeFeatureView,
  reshapePlanView,
} from './api/wire/feature';
import type {
  ApiFeature,
  ApiFeatureView,
  ApiPlanView,
} from './api/wire/feature';
import { reshapeDispatch } from './api/wire/dispatch';
import type { ApiDispatch, ApiUserMessage } from './api/wire/dispatch';
import { reshapeDocSummary, reshapeDocContent } from './api/wire/doc';
import type { ApiDocument, ApiDocView } from './api/wire/doc';
import { reshapeHistoryEntry } from './api/wire/history';
import type { ApiHistoryEntry } from './api/wire/history';
import {
  reshapeProxyStat,
  reshapeProxyCapture,
  reshapeJobTranscript,
} from './api/wire/proxy';
import type {
  ApiProxyFQDNStat,
  ApiProxyCaptureRow,
  ApiJobTranscriptRow,
} from './api/wire/proxy';
import { reshapeTemplate } from './api/wire/template';
import type { ApiPromptTemplate, ApiRestoreResponse } from './api/wire/template';
import {
  boardWithSync,
  reshapeSyncRegistry,
  reshapeSyncSetup,
  reshapeRepoLinkResult,
} from './api/wire/sync';
import type {
  ApiRepo,
  SyncStatusApi,
  SyncRegistryApi,
  SyncSetupApi,
  RepoLinkResultApi,
} from './api/wire/sync';


// ---------- WebModeUnavailableError ----------

// Thrown by stubs for surfaces the web build deliberately doesn't
// implement (agent registry, dispatch auto-pick, prompt-template CRUD,
// app_settings, native folder picker, leader-election events).
// Components SHOULD gate on WEB_MODE before calling these — this is
// the belt-and-braces second line, surfaced to the user via the
// reportError modal if they ever do slip through.
export class WebModeUnavailableError extends Error {
  constructor(feature: string) {
    super(`${feature} is not available in the web build of bacio`);
    this.name = 'WebModeUnavailableError';
  }
}

// ---------- HTTP plumbing ----------

// API base URL. Empty string => same-origin (the recommended deployment,
// where `bacio api` serves both /ui/ and /repos/...). Set VITE_BACIO_API
// at build time to point cross-origin at a different host.
const API_BASE = (import.meta.env.VITE_BACIO_API ?? '').replace(/\/+$/, '');

// Read the bearer token from localStorage so the user can paste it in
// from a Settings field (or a dev console) without rebuilding.
function readToken(): string {
  try { return localStorage.getItem('bacio.token') || ''; }
  catch { return ''; }
}

// Actor identifier stamped onto the X-Actor header. Defaults to "web"
// so audit rows are at least distinguishable from CLI / API direct
// usage; the user can override via localStorage['bacio.actor'].
function readActor(): string {
  try { return localStorage.getItem('bacio.actor') || 'web'; }
  catch { return 'web'; }
}

interface FetchOpts {
  method?: string;
  body?: unknown;
  query?: Record<string, string | number | boolean | undefined>;
}

async function call<T>(path: string, opts: FetchOpts = {}): Promise<T> {
  const method = opts.method ?? 'GET';
  const url = new URL(API_BASE + path, window.location.origin);
  if (opts.query) {
    for (const [k, v] of Object.entries(opts.query)) {
      if (v === undefined || v === '') continue;
      url.searchParams.set(k, String(v));
    }
  }
  const headers: Record<string, string> = { 'X-Actor': readActor() };
  const token = readToken();
  if (token) headers['Authorization'] = `Bearer ${token}`;
  let body: BodyInit | undefined;
  if (opts.body !== undefined) {
    headers['Content-Type'] = 'application/json';
    body = JSON.stringify(opts.body);
  }
  const res = await fetch(url.toString(), { method, headers, body });
  // 204 No Content paths (e.g. DELETE) come back without a body.
  if (res.status === 204) return undefined as T;
  const text = await res.text();
  if (!res.ok) {
    // The server uses a {error, code, details} envelope; surface the
    // human message preferentially, falling back to status text.
    let msg = `${res.status} ${res.statusText}`;
    if (text) {
      try {
        const parsed = JSON.parse(text);
        if (parsed?.error) msg = parsed.error;
      } catch { msg = text; }
    }
    throw new Error(msg);
  }
  if (!text) return undefined as T;
  return JSON.parse(text) as T;
}

// callText is the text/plain twin of call<T> for endpoints that return a
// non-JSON body (BACI-308's raw .http capture passthrough). Same auth /
// error-envelope handling, but the 2xx body is returned verbatim rather than
// JSON-parsed.
async function callText(path: string, opts: FetchOpts = {}): Promise<string> {
  const method = opts.method ?? 'GET';
  const url = new URL(API_BASE + path, window.location.origin);
  if (opts.query) {
    for (const [k, v] of Object.entries(opts.query)) {
      if (v === undefined || v === '') continue;
      url.searchParams.set(k, String(v));
    }
  }
  const headers: Record<string, string> = { 'X-Actor': readActor() };
  const token = readToken();
  if (token) headers['Authorization'] = `Bearer ${token}`;
  const res = await fetch(url.toString(), { method, headers });
  const text = await res.text();
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    if (text) {
      try {
        const parsed = JSON.parse(text);
        if (parsed?.error) msg = parsed.error;
      } catch { msg = text; }
    }
    throw new Error(msg);
  }
  return text;
}

// ---------- API surface ----------

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

// getSyncRegistry fetches BACI-107's GET /sync/repos and reshapes the
// snake-case wire payload to the camelCase SyncRegistry the React
// tree consumes (see reshapeSyncRegistry in ./api/wire/sync).
export async function getSyncRegistry(): Promise<SyncRegistry> {
  const wire = await call<SyncRegistryApi>('/sync/repos');
  return reshapeSyncRegistry(wire);
}

export async function listColumns(): Promise<BoardColumn[]> {
  // Static — every state, in canonical order. No fetch.
  return Object.entries(STATE_LABELS).map(([state, label]) => ({ state, label }));
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

// AddRepositoryPayload is the shape the web bundle passes through to
// POST /repos. Desktop mode ignores it (it pops a native folder
// picker and resolves path/name itself). Both surfaces share the same
// addRepository(payload?) signature — see api.ts for the desktop
// wrapper.

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

export async function listAgents(repoPrefix: string): Promise<AgentCard[]> {
  // BACI-50: bacio api now ships the composite endpoint that the
  // desktop's BoardService.ListAgents used to build locally. The
  // assembled card shape uses camelCase JSON tags matching the
  // existing AgentCard TS type, so no per-field reshape is needed —
  // the API response IS the AgentCard.
  const path = (!repoPrefix || repoPrefix === 'all')
    ? '/agents/cards'
    : `/repos/${repoPrefix}/agents/cards`;
  const cards = await call<AgentCard[]>(path);
  // Normalise nullable arrays so consumers can iterate without
  // defensive checks — matches what the Wails binding returns.
  return (cards ?? []).map(c => ({
    ...c,
    claims: c.claims ?? [],
    dispatches: c.dispatches ?? [],
    todos: c.todos ?? [],
    openQuestions: c.openQuestions ?? [],
  }));
}

// BACI-53 ask_user_question endpoints.

export async function getSessionQuestion(id: number): Promise<SessionQuestionRow> {
  return await call<SessionQuestionRow>(`/agents/questions/${id}`);
}

export async function answerSessionQuestion(
  id: number,
  answers: Record<string, unknown>,
): Promise<SessionQuestionRow> {
  return await call<SessionQuestionRow>(`/agents/questions/${id}/answer`, {
    method: 'POST',
    body: { answers },
  });
}

export async function cancelSessionQuestion(id: number): Promise<SessionQuestionRow> {
  return await call<SessionQuestionRow>(`/agents/questions/${id}/cancel`, {
    method: 'POST',
  });
}

// BACI-287 notification bell endpoints — HTTP twins of api.ts's wrappers.
// Keep the names + return types in lockstep so <NotificationBell> imports
// the same names from `./api` in both modes. The list/count/read-all are
// cross-repo (the global bell); limit <= 0 omits the ?limit= parameter.
export async function listNotifications(unreadOnly = true, limit = 0): Promise<Notification[]> {
  const query: Record<string, string | number> = { state: unreadOnly ? 'unread' : 'all' };
  if (limit > 0) query.limit = limit;
  const body = await call<Notification[]>(`/notifications`, { query });
  return body ?? [];
}

export async function countUnreadNotifications(): Promise<number> {
  const body = await call<{ count: number }>(`/notifications/count`);
  return body?.count ?? 0;
}

export async function markNotificationRead(id: number): Promise<Notification | null> {
  return await call<Notification>(`/notifications/${id}/read`, { method: 'POST' });
}

export async function markAllNotificationsRead(): Promise<number> {
  const body = await call<{ count: number }>(`/notifications/read-all`, { method: 'POST' });
  return body?.count ?? 0;
}

// ApiDocumentLink is the raw wire shape for one document_link row
// returned alongside a doc in the list response. Field naming follows
// the Go-side snake_case the API serialises (links carry the issue
// key / feature slug pre-formatted, so the client doesn't need to
// resolve them).
export async function listDocs(repoPrefix: string, typeFilter = ''): Promise<DocSummary[]> {
  if (!repoPrefix || repoPrefix === 'all') {
    throw new Error('select a repository to view its documents');
  }
  const docs = await call<ApiDocument[]>(`/repos/${repoPrefix}/documents`, {
    query: { type: typeFilter || undefined },
  });
  return docs.map(reshapeDocSummary);
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

// dispatchIssue queues a state-gated auto-pick dispatch (BACI-40). The
// server re-checks the stage's state-gate against the issue's current
// state and picks the most-recently-active free agent — the caller
// names neither an agent nor a note. Errors from the server (no free
// agent / state-gate mismatch) come back as the error envelope and
// surface through reportError() up in App.tsx.
export async function dispatchIssue(
  repoPrefix: string,
  issueKey: string,
  mode: string,
): Promise<DispatchDTO> {
  if (!repoPrefix || repoPrefix === 'all') {
    const i = issueKey.lastIndexOf('-');
    if (i <= 0) throw new Error(`invalid issue key: ${issueKey}`);
    repoPrefix = issueKey.slice(0, i);
  }
  const raw = await call<ApiDispatch>(
    `/repos/${repoPrefix}/issues/${issueKey}/dispatch`,
    { method: 'POST', body: { mode } },
  );
  return reshapeDispatch(raw);
}

// cancelWaitingDispatch (BACI-51) is the spinner-as-cancel-button
// handler in web mode: two round-trips — GET the active dispatch on
// the issue, POST cancel against its id. A 404 from the GET means the
// dispatch cleared between the click and the call landing (e.g. the
// matcher bound it) — that's a no-op success, not an error.
//
// BACI-130: the POST can also race a delivery — the matcher binds a
// queued row to a worker and immediately delivers, between the
// spinner-button render and the click landing. The server now
// rejects cancel-after-delivery with a 409 ("dispatch N has been
// delivered; cannot cancel"). Swallow that the same way as the 404
// — the UI's spinner-cancel button is best-effort, not a "did you
// really want to do this" confirm.
export async function cancelWaitingDispatch(
  repoPrefix: string,
  issueKey: string,
): Promise<void> {
  if (!repoPrefix || repoPrefix === 'all') {
    const i = issueKey.lastIndexOf('-');
    if (i <= 0) throw new Error(`invalid issue key: ${issueKey}`);
    repoPrefix = issueKey.slice(0, i);
  }
  let dsp: ApiDispatch;
  try {
    dsp = await call<ApiDispatch>(
      `/repos/${repoPrefix}/issues/${issueKey}/waiting-dispatch`,
      { method: 'GET' },
    );
  } catch (err: unknown) {
    // call() throws an Error whose message is the server envelope's
    // `error` field — the 404 body says "no waiting dispatch for <key>".
    // Match on that prefix to treat the race-cleared case as a no-op
    // (the matcher bound or another UI cancelled between click + call).
    const msg = err instanceof Error ? err.message : String(err);
    if (msg.startsWith('no waiting dispatch')) return;
    throw err;
  }
  try {
    await call<unknown>(`/agents/dispatches/${dsp.id}/cancel`, { method: 'POST' });
  } catch (err: unknown) {
    // BACI-130: a 409 "delivered, cannot cancel" race is a no-op
    // success — the worker took the Task between render and click.
    const msg = err instanceof Error ? err.message : String(err);
    if (msg.includes('has been delivered')) return;
    throw err;
  }
}

// rescueDispatch (BACI-190) posts a `from="bacio-rescue"` channel event
// to an idle supervisor session asking it to handle a dead worker's
// stranded worktree INLINE. Backend re-checks eligibility (status,
// creator, dead session, idle supervisor) and returns 409 when the
// rescue can't be queued — surfaced via the usual error envelope.
export async function rescueDispatch(dispatchID: number): Promise<DispatchDTO> {
  const raw = await call<ApiDispatch>(
    `/agents/dispatches/${dispatchID}/rescue`,
    { method: 'POST' },
  );
  return reshapeDispatch(raw);
}

// sendSessionMessage (BACI-286) POSTs a user→agent steer message at a
// busy session. The channel serving that session pushes it as a
// `<channel kind="message">` tag at the worker's next turn boundary —
// NOT a dispatch. Unknown session → 404, empty/over-cap body → 400, both
// surfaced via the usual error envelope.
export async function sendSessionMessage(
  sessionID: string,
  body: string,
): Promise<ApiUserMessage> {
  return await call<ApiUserMessage>(
    `/agents/sessions/${encodeURIComponent(sessionID)}/messages`,
    { method: 'POST', body: { body } },
  );
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

export async function listFeatures(repoPrefix: string): Promise<FeatureSummary[]> {
  if (!repoPrefix || repoPrefix === 'all') {
    throw new Error('select a repository to view its features');
  }
  const feats = await call<ApiFeature[]>(`/repos/${repoPrefix}/features`);
  return feats.map(reshapeFeatureSummary);
}

export async function getFeature(repoPrefix: string, slug: string): Promise<FeatureDetail> {
  if (!repoPrefix || repoPrefix === 'all') {
    throw new Error('select a repository to view its features');
  }
  const view = await call<ApiFeatureView>(`/repos/${repoPrefix}/features/${slug}`);
  return reshapeFeatureView(view);
}

// getFeaturePlan (BACI-236) returns the topo-sorted dependency-graph
// payload for a feature. includeClosed=false matches today's open-only
// shape; true widens to include done/cancelled issues plus every
// `blocks` edge whose endpoints are both in the feature. The web
// bundle reshapes blocked_by → blockedBy so callers can share a single
// camelCase shape with the Wails binding.
export async function getFeaturePlan(
  repoPrefix: string,
  slug: string,
  includeClosed: boolean,
): Promise<FeaturePlan> {
  if (!repoPrefix || repoPrefix === 'all') {
    throw new Error('select a repository to view its features');
  }
  const view = await call<ApiPlanView>(
    `/repos/${repoPrefix}/features/${slug}/plan`,
    { query: includeClosed ? { include_closed: '1' } : undefined },
  );
  return reshapePlanView(view);
}

// setFeatureEmoji (BACI-172) updates the per-feature emoji glyph
// surfaced on every kanban card under this feature. Empty string
// clears the emoji. The store-side validator enforces exactly one
// grapheme cluster (or empty) so malformed input surfaces as a
// {error} envelope. Returns the refreshed FeatureDetail so the
// caller can drop it straight into its panel state.
export async function setFeatureEmoji(
  repoPrefix: string,
  slug: string,
  emoji: string,
): Promise<FeatureDetail> {
  if (!repoPrefix || repoPrefix === 'all') {
    throw new Error('select a repository to edit a feature');
  }
  await call<ApiFeature>(`/repos/${repoPrefix}/features/${slug}`, {
    method: 'PATCH',
    body: { slug, emoji },
  });
  return getFeature(repoPrefix, slug);
}

// setFeatureBranchName (BACI-231) updates the per-feature integration
// branch every kanban card under this feature is decorated with and
// every dispatch is routed against. Empty string clears the branch
// (the feature ships straight to main again). The store-side
// ValidateBranchName enforces git's refname rules so a malformed
// input surfaces as a {error} envelope. Returns the refreshed
// FeatureDetail so the caller can drop it straight into its panel
// state. Parallel to setFeatureEmoji.
export async function setFeatureBranchName(
  repoPrefix: string,
  slug: string,
  branchName: string,
): Promise<FeatureDetail> {
  if (!repoPrefix || repoPrefix === 'all') {
    throw new Error('select a repository to edit a feature');
  }
  await call<ApiFeature>(`/repos/${repoPrefix}/features/${slug}`, {
    method: 'PATCH',
    body: { slug, branch_name: branchName },
  });
  return getFeature(repoPrefix, slug);
}

// setFeatureDescription (BACI-341) updates the per-feature description
// edited inline from the Features detail pane. Description is free text;
// empty clears it. Returns the refreshed FeatureDetail so the caller can
// drop it straight into its panel state. Parallel to setFeatureBranchName.
export async function setFeatureDescription(
  repoPrefix: string,
  slug: string,
  description: string,
): Promise<FeatureDetail> {
  if (!repoPrefix || repoPrefix === 'all') {
    throw new Error('select a repository to edit a feature');
  }
  await call<ApiFeature>(`/repos/${repoPrefix}/features/${slug}`, {
    method: 'PATCH',
    body: { slug, description },
  });
  return getFeature(repoPrefix, slug);
}

// setFeatureState (BACI-199) flips the feature's three-state column
// and returns the refreshed FeatureDetail. BACI-250 decoupled this
// from the auto-close pin — call setFeatureAutoClose to flip
// `state_manual` independently.
export async function setFeatureState(
  repoPrefix: string,
  slug: string,
  state: string,
): Promise<FeatureDetail> {
  if (!repoPrefix || repoPrefix === 'all') {
    throw new Error('select a repository to edit a feature');
  }
  await call<ApiFeature>(
    `/repos/${repoPrefix}/features/${slug}/state`,
    { method: 'PUT', body: { slug, state } },
  );
  return getFeature(repoPrefix, slug);
}

// setFeatureAutoClose (BACI-250) flips the per-feature auto-close
// toggle — the sticky-bit `state_manual` column that gates the
// BACI-199 archive-sweep's auto-completion pass — and returns the
// refreshed FeatureDetail. enabled=true clears the bit; enabled=false
// sets it (pin long-lived catch-alls so they stay `active`).
export async function setFeatureAutoClose(
  repoPrefix: string,
  slug: string,
  enabled: boolean,
): Promise<FeatureDetail> {
  if (!repoPrefix || repoPrefix === 'all') {
    throw new Error('select a repository to edit a feature');
  }
  await call<ApiFeature>(
    `/repos/${repoPrefix}/features/${slug}/auto-close`,
    { method: 'PUT', body: { enabled } },
  );
  return getFeature(repoPrefix, slug);
}

// setFeatureCollectHandoffs (BACI-333) flips the per-feature
// collect-handoffs toggle that gates whether worker close-outs append
// handoff comments to this feature, and returns the refreshed
// FeatureDetail. enabled=true collects handoffs; enabled=false silences a
// standing bucket like `bugs`/`maintenance`.
export async function setFeatureCollectHandoffs(
  repoPrefix: string,
  slug: string,
  enabled: boolean,
): Promise<FeatureDetail> {
  if (!repoPrefix || repoPrefix === 'all') {
    throw new Error('select a repository to edit a feature');
  }
  await call<ApiFeature>(
    `/repos/${repoPrefix}/features/${slug}/handoffs`,
    { method: 'PUT', body: { enabled } },
  );
  return getFeature(repoPrefix, slug);
}

// setFeatureHiddenOnBoard (BACI-177) flips the per-feature "Show on
// board" toggle and returns the refreshed FeatureDetail. true hides
// every kanban card belonging to this feature on this machine; false
// makes them visible again. Idempotent — flipping to the same state
// is a no-op write.
export async function setFeatureHiddenOnBoard(
  repoPrefix: string,
  slug: string,
  hidden: boolean,
): Promise<FeatureDetail> {
  if (!repoPrefix || repoPrefix === 'all') {
    throw new Error('select a repository to edit a feature');
  }
  await call<{ slug: string; hidden: boolean }>(
    `/repos/${repoPrefix}/features/${slug}/hide`,
    { method: 'PUT', body: { hidden } },
  );
  return getFeature(repoPrefix, slug);
}

// addFeatureComment posts a chronological handoff comment to a feature
// (BACI-124). Returns the refreshed FeatureDetail so the caller can
// drop the new row straight into its panel state.
export async function addFeatureComment(
  repoPrefix: string,
  slug: string,
  author: string,
  body: string,
): Promise<FeatureDetail> {
  if (!repoPrefix || repoPrefix === 'all') {
    throw new Error('select a repository to comment on a feature');
  }
  const effectiveAuthor = author?.trim() || readActor() || 'web';
  await call<unknown>(`/repos/${repoPrefix}/features/${slug}/comments`, {
    method: 'POST',
    body: { author: effectiveAuthor, body },
  });
  return getFeature(repoPrefix, slug);
}

// deleteFeatureComment removes a feature comment by uuid (BACI-124).
export async function deleteFeatureComment(
  repoPrefix: string,
  slug: string,
  commentUUID: string,
): Promise<FeatureDetail> {
  if (!repoPrefix || repoPrefix === 'all') {
    throw new Error('select a repository to comment on a feature');
  }
  await call<unknown>(`/repos/${repoPrefix}/features/${slug}/comments/${commentUUID}`, {
    method: 'DELETE',
  });
  return getFeature(repoPrefix, slug);
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

// listProxyStats (BACI-304) is the HTTP twin of api.ts's listProxyStats —
// the Monitor screen's per-FQDN reverse-proxy rollup. GET /proxy/stats is
// cross-cutting (no repo_id), so it takes no repo prefix. `sinceDays === 0`
// is the "All-time" sentinel (no ?since= parameter); a positive value maps
// onto the server's rolling-duration `?since=Nd` lookback, mirroring the
// Shipped pill's convention. Reshapes the snake_case wire rows into the
// camelCase ProxyFQDNStat shape api.ts re-exports.
export async function listProxyStats(sinceDays = 0): Promise<ProxyFQDNStat[]> {
  const query: Record<string, string | number> = {};
  if (sinceDays > 0) query.since = `${sinceDays}d`;
  const rows = await call<ApiProxyFQDNStat[]>('/proxy/stats', { query });
  return (rows ?? []).map(reshapeProxyStat);
}

// listProxyCaptures (BACI-308) is the HTTP twin of api.ts's listProxyCaptures —
// the Monitor drill-down's filtered capture list. GET /proxy/captures is
// cross-cutting (no repo prefix). Reshapes the snake_case wire rows into the
// camelCase ProxyCaptureRow shape api.ts re-exports.
export async function listProxyCaptures(
  host: string,
  dispatchId = 0,
  anthropicOnly = false,
  sinceDays = 0,
): Promise<ProxyCaptureRow[]> {
  const query: Record<string, string | number | boolean> = {};
  if (host) query.host = host;
  if (dispatchId > 0) query.dispatch_id = dispatchId;
  if (anthropicOnly) query.is_anthropic = true;
  if (sinceDays > 0) query.since = `${sinceDays}d`;
  const rows = await call<ApiProxyCaptureRow[]>('/proxy/captures', { query });
  return (rows ?? []).map(reshapeProxyCapture);
}

// getProxyCaptureRaw (BACI-308) is the HTTP twin of api.ts's getProxyCaptureRaw —
// the raw .http capture text for one proxy_requests id, served as text/plain.
export async function getProxyCaptureRaw(id: number): Promise<string> {
  return callText(`/proxy/captures/${id}/raw`);
}

// anthropicCapture (BACI-308) is the HTTP twin — the parsed detail of one
// captured Anthropic SSE turn. The wire shape is already the snake_case
// ProxyMessage the sheet consumes, so no reshape is needed.
export async function anthropicCapture(id: number): Promise<ProxyMessage> {
  return call<ProxyMessage>(`/proxy/captures/${id}`);
}

// jobTranscript (BACI-308) is the HTTP twin — a dispatch's assembled per-job
// transcript. The wire shape is the snake_case AnthropicTranscript the adapter
// feeds the viewer, so no reshape is needed.
export async function jobTranscript(dispatchId: number): Promise<AnthropicTranscript> {
  return call<AnthropicTranscript>(`/proxy/jobs/${dispatchId}/transcript`);
}

// listJobTranscripts (BACI-322) is the HTTP twin of api.ts's
// listJobTranscripts — the Monitor Transcript page's row-per-dispatch list.
// GET /proxy/jobs is cross-cutting (no repo prefix in the path); `repo` scopes
// to the active board, `issue` / `mode` narrow, `session` / `agent` (BACI-348)
// narrow to one supervisor session / subagent identity, `sinceDays === 0` is
// the all-time sentinel. Reshapes the snake_case wire rows into the camelCase
// JobTranscriptRow shape api.ts re-exports.
export async function listJobTranscripts(
  repo = '',
  issue = '',
  mode = '',
  session = '',
  agent = '',
  sinceDays = 0,
): Promise<JobTranscriptRow[]> {
  const query: Record<string, string | number> = {};
  if (repo) query.repo = repo;
  if (issue) query.issue = issue;
  if (mode) query.mode = mode;
  if (session) query.session = session;
  if (agent) query.agent = agent;
  if (sinceDays > 0) query.since = `${sinceDays}d`;
  const rows = await call<ApiJobTranscriptRow[]>('/proxy/jobs', { query });
  return (rows ?? []).map(reshapeJobTranscript);
}

export async function getDoc(repoPrefix: string, filename: string): Promise<DocContent> {
  if (!repoPrefix || repoPrefix === 'all') {
    throw new Error('select a repository to view its documents');
  }
  const view = await call<ApiDocView>(`/repos/${repoPrefix}/documents/${filename}`);
  return reshapeDocContent(view);
}

export async function saveDoc(
  repoPrefix: string,
  filename: string,
  content: string,
): Promise<DocContent> {
  if (!repoPrefix || repoPrefix === 'all') {
    throw new Error('select a repository to save documents');
  }
  // PATCH (body-only edit) mirrors the desktop's SaveDoc → EditDocument:
  // the document already exists, so we only push the new content and leave
  // its type untouched. PUT is the upsert handler and requires `type`, so
  // saving body-only against it 400s. The edit handler returns the
  // refreshed row without its body, so re-fetch to get the content back.
  await call<unknown>(`/repos/${repoPrefix}/documents/${filename}`, {
    method: 'PATCH',
    body: { content },
  });
  return getDoc(repoPrefix, filename);
}

// ---------- Prompt templates (BACI-47/B+C, BACI-50) ----------
//
// Body + state-gate CRUD landed in BACI-36. BACI-50 finished the typed
// CRUD surface — add/rename/delete/restore-builtins — and added the
// /settings/templates/full list endpoint that returns the full DTO
// (label, defaults, is_builtin, …) so the web bundle stops deriving
// labels client-side.

export async function listPromptTemplates(): Promise<PromptTemplateDTO[]> {
  // BACI-50 added the composite /settings/templates/full endpoint that
  // returns the rich DTO — labels, defaults, is_builtin, and the
  // BACI-51 concurrency fields all flow through in one round-trip,
  // no client-side label derivation or placeholder fields needed.
  const rows = await call<ApiPromptTemplate[]>('/settings/templates/full');
  return (rows ?? []).map(reshapeTemplate);
}

// Refetch every template and return the one DTO the caller updated —
// SavePromptTemplate only returns the persisted row's body, not the
// full DTO; fetching once after the write keeps the caller's
// `templates` state consistent.
async function refreshOneTemplate(slug: string): Promise<PromptTemplateDTO> {
  const all = await listPromptTemplates();
  const found = all.find(t => t.slug === slug);
  if (!found) throw new Error(`template ${slug} not found after save`);
  return found;
}

export async function savePromptTemplate(
  mode: string,
  body: string,
): Promise<PromptTemplateDTO> {
  // Empty body = reset to default. BACI-36's PUT rejects an empty body
  // explicitly; the documented contract is to DELETE for reset.
  if (body === '') {
    await call<unknown>(`/settings/templates/${mode}`, { method: 'DELETE' });
  } else {
    await call<unknown>(`/settings/templates/${mode}`, {
      method: 'PUT',
      body: { body },
    });
  }
  return refreshOneTemplate(mode);
}

// savePromptConcurrency (BACI-51) PUTs the per-(repo, slug) in-flight
// dispatch cap. 0 = unlimited; positive integers cap. No DELETE route
// (a PUT 0 reverts to "unlimited").
export async function savePromptConcurrency(
  mode: string,
  concurrencyLimit: number,
): Promise<PromptTemplateDTO> {
  await call<unknown>(`/settings/templates/${mode}/concurrency`, {
    method: 'PUT',
    body: { concurrency_limit: concurrencyLimit },
  });
  return refreshOneTemplate(mode);
}

// savePromptActionLabel (BACI-67) sets or clears the imperative
// override rendered on the dispatch action menus. An empty actionLabel
// DELETEs the override, mirroring the body endpoint's reset shape; the
// UI then derives a default from the gerund display name. A non-empty
// value PUTs the override.
export async function savePromptActionLabel(
  mode: string,
  actionLabel: string,
): Promise<PromptTemplateDTO> {
  if (actionLabel === '') {
    await call<unknown>(`/settings/templates/${mode}/action-label`, { method: 'DELETE' });
  } else {
    await call<unknown>(`/settings/templates/${mode}/action-label`, {
      method: 'PUT',
      body: { action_label: actionLabel },
    });
  }
  return refreshOneTemplate(mode);
}

export async function addPromptTemplate(
  slug: string,
  name: string,
  body: string,
  actionLabel: string = '',
): Promise<PromptTemplateDTO> {
  // BACI-67: forward actionLabel verbatim — an empty string is the
  // "no override, derive from name" sentinel that the Go side honours.
  const raw = await call<ApiPromptTemplate>('/settings/templates', {
    method: 'POST',
    body: { slug, name, body, action_label: actionLabel },
  });
  return reshapeTemplate(raw);
}

export async function renamePromptTemplate(
  slug: string,
  newSlug: string,
  newName: string,
): Promise<PromptTemplateDTO> {
  const raw = await call<ApiPromptTemplate>(`/settings/templates/${slug}/rename`, {
    method: 'POST',
    body: { new_slug: newSlug, new_name: newName },
  });
  return reshapeTemplate(raw);
}

export async function deletePromptTemplate(
  slug: string,
): Promise<PromptTemplateDTO> {
  const raw = await call<ApiPromptTemplate>(`/settings/templates/${slug}/row`, {
    method: 'DELETE',
  });
  return reshapeTemplate(raw);
}

export async function restoreBuiltinPromptTemplates(): Promise<PromptTemplateDTO[]> {
  const raw = await call<ApiRestoreResponse>('/settings/templates/restore-builtins', {
    method: 'POST',
  });
  return (raw.templates ?? []).map(reshapeTemplate);
}

export async function promptPlaceholders(): Promise<string[]> {
  // Mirror internal/model/prompt.go:PromptTemplateTokens. The REST
  // surface doesn't return the token list separately, but it's a
  // small fixed set — render the canonical names client-side.
  return ['issue_id', 'issue_title', 'repo_prefix'];
}

// ---------- Bacio version (BACI-47/A) ----------

export async function bacioVersion(): Promise<string> {
  const res = await call<{ version: string }>('/version');
  return res.version;
}

// ---------- Sync setup (BACI-111) ----------
//
// POST /repos/{prefix}/sync/setup over a direct fetch (not the shared
// call() helper) so the 409 collision body decodes verbatim. Two wire
// shapes:
//   - 200 OK: SyncSetupApi with init/clone populated for the chosen mode,
//     preview_collisions absent.
//   - 409 Conflict: SyncSetupApi with preview_collisions populated and
//     init/clone absent — the engine wrote nothing.
//
// The function maps both into the SyncSetupResult shape the modal
// consumes (snake → camel), and on the 409 path throws the typed
// SyncSetupCollisionError so the caller can `instanceof`-branch into
// the step-2 confirm.






// Re-exports for the cross-transport modal: SyncSetupResult is the
// camelCase DTO the modal consumes; SyncSetupCollisionError is the
// typed exception thrown on the 409 path. Same names as api.ts.

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

// ---------- Phantom-repo linking (BACI-112) ----------
//
// The cross-transport shape — mirrors RepoLinkResultDTO in
// desktop/settingsservice.go and api.ts's re-export. The HTTP handler
// returns snake_case JSON; we reshape to camelCase on the way out so
// the React modal consumes the same shape under both transports.


// RepoLinkResultDTO is the cross-transport alias used by SyncView /
// PhantomLinkModal — matches the api.ts re-export. The HTTP wire shape
// stays snake_case in line with the rest of api.http.ts.

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

// ---------- Sync preferences (BACI-89 / BACI-108) ----------
//
// The BACI-89 sync.background_enabled toggle, exposed for the
// standalone Sync view (BACI-108). Same shape as the board / display
// preferences pairs; lives behind /settings/sync-preferences.


export async function getSyncPreferences(): Promise<SyncPreferences> {
  const res = await call<{ background_enabled: boolean }>('/settings/sync-preferences');
  return { backgroundEnabled: res.background_enabled };
}

export async function setSyncPreferences(backgroundEnabled: boolean): Promise<SyncPreferences> {
  const res = await call<{ background_enabled: boolean }>('/settings/sync-preferences', {
    method: 'PUT',
    body: { background_enabled: backgroundEnabled },
  });
  return { backgroundEnabled: res.background_enabled };
}

// ---------- Display preferences (BACI-68) ----------
//
// display.show_archived global toggle — when on, default lists / board
// / kanban views include archived rows; when off (the default) they're
// hidden. The CLI's per-call --include-archived flag overrides this
// setting for one call; the desktop / web UIs have no per-call knob,
// so the setting is the single source of truth here.


export async function getDisplayPreferences(): Promise<DisplayPreferencesDTO> {
  const res = await call<{ show_archived: boolean }>('/settings/display-preferences');
  return { showArchived: res.show_archived };
}

export async function setDisplayPreferences(showArchived: boolean): Promise<DisplayPreferencesDTO> {
  const res = await call<{ show_archived: boolean }>('/settings/display-preferences', {
    method: 'PUT',
    body: { show_archived: showArchived },
  });
  return { showArchived: res.show_archived };
}

// ---------- Archive preferences (BACI-162) ----------
//
// archive.auto_enabled + archive.retention_days global settings. When
// auto_enabled is false the hourly issue auto-archive pass is skipped
// entirely; retention_days (1..3650) is the number of days a
// terminal-state issue's terminal_at must sit before the next sweep
// archives it.


export async function getArchivePreferences(): Promise<ArchivePreferencesDTO> {
  const res = await call<{ auto_enabled: boolean; retention_days: number }>('/settings/archive-preferences');
  return { autoEnabled: res.auto_enabled, retentionDays: res.retention_days };
}

export async function setArchivePreferences(
  autoEnabled: boolean,
  retentionDays: number,
): Promise<ArchivePreferencesDTO> {
  const res = await call<{ auto_enabled: boolean; retention_days: number }>('/settings/archive-preferences', {
    method: 'PUT',
    body: { auto_enabled: autoEnabled, retention_days: retentionDays },
  });
  return { autoEnabled: res.auto_enabled, retentionDays: res.retention_days };
}

// ---------- Audio preferences (BACI-240) ----------
//
// ui.shipped_sfx global toggle — when on, the Pipeline Shipping-column Shipped pill
// plays a short ka-ching SFX on every genuine ship. Defaults to true
// (BACI-295 flipped the default on). The wire payload is
// {shipped_sfx: bool} rather than a generic "value" to leave room for
// future audio toggles without breaking the existing field name.


export async function getAudioPreferences(): Promise<AudioPreferencesDTO> {
  const res = await call<{ shipped_sfx: boolean }>('/settings/audio-preferences');
  return { shippedSfx: res.shipped_sfx };
}

export async function setAudioPreferences(shippedSfx: boolean): Promise<AudioPreferencesDTO> {
  const res = await call<{ shipped_sfx: boolean }>('/settings/audio-preferences', {
    method: 'PUT',
    body: { shipped_sfx: shippedSfx },
  });
  return { shippedSfx: res.shipped_sfx };
}

// ---------- Timezone preference (BACI-312) ----------
//
// Global ui.timezone setting — the user's IANA zone name. Drives the
// browser-side local-midnight cutoff for the Pipeline Shipping-column
// Shipped pill's "Today" scope. Empty when unset (App.tsx auto-detects
// the browser zone and persists it on first run). HTTP twin of api.ts's
// get/setTimezonePreferences — keep names + shape in lockstep.


export async function getTimezonePreferences(): Promise<TimezonePreferencesDTO> {
  const res = await call<{ timezone: string }>('/settings/timezone-preferences');
  return { timezone: res.timezone };
}

export async function setTimezonePreferences(timezone: string): Promise<TimezonePreferencesDTO> {
  const res = await call<{ timezone: string }>('/settings/timezone-preferences', {
    method: 'PUT',
    body: { timezone },
  });
  return { timezone: res.timezone };
}

// ---------- Default-feature preference (BACI-235) ----------
//
// Per-repo `default_feature` setting that auto-applies to issues
// created without an explicit feature_slug. Empty slug = unset (the
// legacy default). FK ON DELETE SET NULL clears the column when the
// referenced feature is deleted.


type defaultFeatureResp = { feature: { slug: string; title?: string; emoji?: string } | null };

function dtoFromResp(res: defaultFeatureResp): DefaultFeatureDTO {
  if (!res.feature) return { slug: '' };
  return { slug: res.feature.slug, title: res.feature.title, emoji: res.feature.emoji };
}

export async function getDefaultFeature(repoPrefix: string): Promise<DefaultFeatureDTO> {
  const res = await call<defaultFeatureResp>(`/repos/${encodeURIComponent(repoPrefix)}/settings/default-feature`);
  return dtoFromResp(res);
}

export async function setDefaultFeature(repoPrefix: string, slug: string): Promise<DefaultFeatureDTO> {
  const res = await call<defaultFeatureResp>(`/repos/${encodeURIComponent(repoPrefix)}/settings/default-feature`, {
    method: 'PUT',
    body: { slug },
  });
  return dtoFromResp(res);
}

export async function clearDefaultFeature(repoPrefix: string): Promise<DefaultFeatureDTO> {
  await call<defaultFeatureResp>(`/repos/${encodeURIComponent(repoPrefix)}/settings/default-feature`, {
    method: 'DELETE',
  });
  return { slug: '' };
}

// ---------- Per-repo board-hidden-states (BACI-248) ----------
//
// The set of kanban-column states hidden from the board on this
// machine. Lives in tui_settings[board.hidden_states] — surfaced now
// on the desktop / web Per-repository Settings pane, previously
// TUI-only. Mirrors the BACI-177 features/hidden shape (a single
// slice-valued envelope) so the React seam reads both per-repo
// board-hide endpoints with one transport pattern.


type boardHiddenStatesResp = { states: string[] | null };

export async function getBoardHiddenStates(repoPrefix: string): Promise<BoardHiddenStatesDTO> {
  if (!repoPrefix || repoPrefix === 'all') {
    throw new Error('select a repository to view its board-hide settings');
  }
  const res = await call<boardHiddenStatesResp>(
    `/repos/${encodeURIComponent(repoPrefix)}/board/hidden-states`,
  );
  return { states: res.states ?? [] };
}

// setBoardHiddenStates replaces the persisted set (replace-not-merge).
// Pass the full new array — unknown state names are silently dropped
// at the store boundary so a future state rename doesn't break old
// saved settings.
export async function setBoardHiddenStates(
  repoPrefix: string,
  states: string[],
): Promise<BoardHiddenStatesDTO> {
  if (!repoPrefix || repoPrefix === 'all') {
    throw new Error('select a repository to edit its board-hide settings');
  }
  const res = await call<boardHiddenStatesResp>(
    `/repos/${encodeURIComponent(repoPrefix)}/board/hidden-states`,
    { method: 'PUT', body: { states } },
  );
  return { states: res.states ?? [] };
}

// ---------- BACI-68 per-entity archive verbs ----------

export async function archiveIssue(prefix: string, key: string): Promise<unknown> {
  return call(`/repos/${encodeURIComponent(prefix)}/issues/${encodeURIComponent(key)}/archive`, { method: 'POST' });
}

export async function unarchiveIssue(prefix: string, key: string): Promise<unknown> {
  return call(`/repos/${encodeURIComponent(prefix)}/issues/${encodeURIComponent(key)}/unarchive`, { method: 'POST' });
}

export async function archiveFeature(prefix: string, slug: string): Promise<unknown> {
  return call(`/repos/${encodeURIComponent(prefix)}/features/${encodeURIComponent(slug)}/archive`, { method: 'POST' });
}

export async function unarchiveFeature(prefix: string, slug: string): Promise<unknown> {
  return call(`/repos/${encodeURIComponent(prefix)}/features/${encodeURIComponent(slug)}/unarchive`, { method: 'POST' });
}

export async function archiveDocument(prefix: string, filename: string): Promise<unknown> {
  return call(`/repos/${encodeURIComponent(prefix)}/documents/${encodeURIComponent(filename)}/archive`, { method: 'POST' });
}

export async function unarchiveDocument(prefix: string, filename: string): Promise<unknown> {
  return call(`/repos/${encodeURIComponent(prefix)}/documents/${encodeURIComponent(filename)}/unarchive`, { method: 'POST' });
}

// ---------- Leader election (BACI-50) ----------

// The bacio api server runs the elector itself, sharing the ui_leader
// table with the desktop and TUI. The browser can never be the leader,
// but its connected api server can — so the "Controlling" chip reflects
// the server's lease, not this page's. App.tsx polls this on a 10s
// cadence (matching POLL_INTERVAL_MS, which mirrors UILeaderHeartbeatInterval).
export async function getLeaderStatus(): Promise<LeaderStatusDTO> {
  const res = await call<{ amLeader: boolean; holderLabel: string }>('/leader');
  return { amLeader: !!res.amLeader, holderLabel: res.holderLabel ?? '' };
}

// ─── Pipeline (Phase 4) ──────────────────────────────────────────────
// HTTP twins of the api.ts pipeline methods — same names + shapes so
// PipelineView stays transport-agnostic. See internal/api/handlers_pipeline.go.
// The column-changing verbs (reorder / engine-mode / ship) return the
// updated issue, reshaped into a BoardCard (the glyph/branch joins are
// dropped on the bare model.Issue payload — the next listCards() poll
// re-populates them, same as setIssueState). The job-control verbs
// (process / start / stop) return the refreshed PipelineJob chain.

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

// ProcessSelection mirrors the lib/pipelineProcesses discriminated type
// (parallel shape — see the header note on why api.http.ts doesn't
// import). The cumulative-stepper picker sends an explicit stage list;
// the kept skip-Plan buttons send a preset slug.
type ProcessSelection = { stages: string[] } | { process: string };

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
