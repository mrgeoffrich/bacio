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

// ---------- DTO shapes (mirror desktop/*service.go) ----------

export interface Board {
  prefix: string;
  name: string;
  issueCount: number;
  // BACI-89 background-sync status. syncEnabled = "this repo has git
  // sync configured"; the other three reflect the controller's
  // background sync runner last / current state. syncLastAt /
  // syncLastError are absent when the repo has never synced / the last
  // sync succeeded.
  syncEnabled: boolean;
  syncInProgress: boolean;
  syncLastAt?: string;
  syncLastError?: string;
}

// SyncStatusApi mirrors api.SyncStatusOut — the wire shape of the
// BACI-89 GET /sync endpoint.
interface SyncStatusApi {
  prefix: string;
  configured: boolean;
  background_enabled: boolean;
  in_progress: boolean;
  last_sync_at?: string;
  last_error?: string;
  remote?: string;
}

export interface BoardColumn {
  state: string;
  label: string;
}

export interface BoardCardTodo {
  content: string;
  status: string;
}

export interface BoardCard {
  key: string;
  column: string;
  columnLabel: string;
  title: string;
  tags: string[];
  assignees: string[];
  claude: boolean;
  taken: boolean;
  waitingForClaim: boolean;
  // BACI-60 enrichment: lower-cased prompt-template label of the
  // newest open claim's most recent non-cancelled dispatch (empty
  // when no verb can be derived) and the claiming session's
  // TodoWrite progress (zeroes when no todos / not taken).
  activeVerb?: string;
  todosDone?: number;
  todosTotal?: number;
  // BACI-75: the per-task rows underlying todosDone/todosTotal,
  // surfaced so the kanban card's "Tasks n/m" pill can expand inline
  // without a follow-up fetch. Absent (omitempty server-side) on
  // untaken cards / when the session never wrote a TodoList.
  todos?: BoardCardTodo[];
  // BACI-68: mirror of issues.archived_at IS NOT NULL. Cards with
  // archived=true only surface when display.show_archived is on; the
  // kanban renders them visibly muted so an archived card stands out
  // from the live ones around it.
  archived?: boolean;
}

export interface CommentDTO {
  author: string;
  body: string;
  createdAt: string;
}

export interface PRDTO {
  url: string;
}

export interface DocLinkDTO {
  filename: string;
  type: string;
  description: string;
}

export interface ClaimantDTO {
  sessionId: string;
  agentName: string;
  prompt: string;
  claimedAt: string;
  releasedAt: string | null;
  open: boolean;
}

export interface IssueDetail {
  key: string;
  column: string;
  columnLabel: string;
  title: string;
  description: string;
  tags: string[];
  assignees: string[];
  claude: boolean;
  comments: CommentDTO[];
  pullRequests: PRDTO[];
  documents: DocLinkDTO[];
  claimants: ClaimantDTO[];
  taken: boolean;
}

// IssueMetaDTO mirrors desktop/boardservice.go:IssueMetaDTO — the
// issue-header slice of an IssueBriefDTO. Kept thin so the rail and
// primary column don't have to thread the full brief.
export interface IssueMetaDTO {
  key: string;
  column: string;
  columnLabel: string;
  title: string;
  description: string;
  tags: string[];
  assignees: string[];
  claude: boolean;
  taken: boolean;
  waitingForClaim: boolean;
}

export interface LinkedDocDTO {
  filename: string;
  type: string;
  description: string;
  sourcePath?: string;
  linkedVia: string[];
  content: string;
}

export interface FeatureRefDTO {
  slug: string;
  title: string;
}

export interface RelationDTO {
  type: string;
  otherKey: string;
}

export interface RelationsDTO {
  outgoing: RelationDTO[];
  incoming: RelationDTO[];
}

// IssueBriefDTO mirrors desktop/boardservice.go:IssueBriefDTO — the
// workspace-shaped payload. The REST surface (GET /repos/.../brief)
// emits the snake_case internal/client/views.go::IssueBrief shape;
// reshapeApiBrief() collapses it into this shape so React reads the
// same camelCase fields in both modes.
export interface IssueBriefDTO {
  issue: IssueMetaDTO;
  feature: FeatureRefDTO | null;
  relations: RelationsDTO;
  pullRequests: PRDTO[];
  documents: LinkedDocDTO[];
  comments: CommentDTO[];
  claimants: ClaimantDTO[];
  taken: boolean;
  waitingForClaim: boolean;
  warnings: string[];
}

export interface ClaimDTO {
  issueKey: string;
  prompt: string;
  claimedAt: string;
  state: string;
}

export interface DispatchDTO {
  id: number;
  issueKey: string;
  targetAgent: string;
  mode: string;
  status: string;
  payload: string;
  createdBy: string;
  createdAt: string;
}

export interface SessionTodoDTO {
  content: string;
  status: string;
  // BACI-62: per-job scope so a future "history" pane can group
  // prior-job todos. Empty / absent on rows from sessions registered
  // before BACI-62 or when the hook couldn't attribute a single
  // open claim. Optional on the wire (omitempty server-side).
  issueKey?: string;
}

// QuestionDTO is one open BACI-53 ask_user_question row — the
// minimal shape the agent card needs to render its "user input
// needed" badge. Header is the question's short tag; the full
// payload is fetched via getSessionQuestion when the user opens
// the modal.
export interface QuestionDTO {
  id: number;
  issueKey?: string;
  header: string;
  askedAt: string;
}

// SessionQuestion is the full row returned by the per-question
// endpoints. Mirrors the Go shape (snake_case is the wire format
// for these — the agent registry was built before we standardised
// camelCase, and questions match the existing dispatch/claim
// shapes for consistency).
export interface SessionQuestionPayload {
  questions: SessionQuestionItem[];
}

export interface SessionQuestionItem {
  question: string;
  header: string;
  multiSelect?: boolean;
  options: SessionQuestionOption[];
}

export interface SessionQuestionOption {
  label: string;
  description?: string;
}

export interface SessionQuestionRow {
  id: number;
  session_id: string;
  request_uuid: string;
  issue_key?: string;
  payload: SessionQuestionPayload;
  answers?: Record<string, unknown>;
  state: 'open' | 'answered' | 'cancelled' | 'abandoned';
  asked_at: string;
  answered_at?: string;
  asked_by: string;
  answered_by?: string;
}

export interface AgentCard {
  sessionId: string;
  agentName: string;
  actor: string;
  model: string;
  branch: string;
  repoPrefix: string;
  status: string;
  busy: boolean;
  busyIssue: string;
  waiting: boolean;
  waitingIssue: string;
  hasChannel: boolean;
  bacioVersion: string;
  bacioVersionStale: boolean;
  lastSeenAt: string;
  claims: ClaimDTO[];
  dispatches: DispatchDTO[];
  todos: SessionTodoDTO[];
  todosDone: number;
  todosTotal: number;
  // BACI-53: open ask_user_question rows. Empty when the agent
  // isn't waiting on the user.
  openQuestions: QuestionDTO[];
}

export interface DocSummary {
  filename: string;
  type: string;
  sizeBytes: number;
  updatedAt: string;
}

export interface DocContent {
  filename: string;
  type: string;
  content: string;
  updatedAt: string;
}

export interface FeatureSummary {
  slug: string;
  title: string;
  updatedAt: string;
}

export interface FeatureLinkedIssue {
  key: string;
  title: string;
  state: string;
  stateLabel: string;
}

export interface FeatureDetail {
  slug: string;
  title: string;
  description: string;
  createdAt: string;
  updatedAt: string;
  issues: FeatureLinkedIssue[];
}

export interface HistoryEntryDTO {
  id: number;
  actor: string;
  op: string;
  kind: string;
  targetLabel: string;
  details: string;
  createdAt: string;
}

export interface HistoryPage {
  entries: HistoryEntryDTO[];
  page: number;
  pageSize: number;
  hasMore: boolean;
}

export interface LeaderStatusDTO {
  amLeader: boolean;
  holderLabel: string;
}

export interface PromptTemplateDTO {
  slug: string;
  mode: string;
  label: string;
  body: string;
  default: string;
  isBuiltin: boolean;
  isDefault: boolean;
  allowedStates: string[];
  defaultStates: string[];
  statesAreDefault: boolean;
  concurrencyLimit: number;
  defaultConcurrencyLimit: number;
  concurrencyIsDefault: boolean;
  // BACI-67: imperative override rendered on the dispatch action
  // menus (kanban-card + issue-workspace dropdowns). When empty, the
  // UI derives one from `label` (the gerund display name); the seed
  // step stamps every built-in with an explicit imperative so the
  // derivation rule is only a fallback for user-created templates.
  actionLabel: string;
  defaultActionLabel: string;
  actionLabelIsDefault: boolean;
}

export interface BoardPreferencesDTO {
  hideEmptyColumns: boolean;
}

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

// ---------- Reshape helpers (API JSON → desktop DTOs) ----------

// State labels mirror desktop/boardservice.go:stateLabels so the web
// build renders the same kanban column titles as the desktop. Keep in
// sync.
const STATE_LABELS: Record<string, string> = {
  todo: 'Todo',
  in_progress: 'In Progress',
  needs_action: 'Needs Action',
  in_review: 'In Review',
  done: 'Done',
  cancelled: 'Cancelled',
};

function stateLabel(s: string): string {
  return STATE_LABELS[s] ?? s;
}

interface ApiIssue {
  key: string;
  title: string;
  description?: string;
  state: string;
  assignee?: string;
  tags?: string[];
  taken?: boolean;
  waiting_for_claim?: boolean;
}

function assigneeList(a: string | undefined | null): string[] {
  return a ? [a] : [];
}

function cardFromIssue(iss: ApiIssue): BoardCard {
  const assignee = iss.assignee ?? '';
  return {
    key: iss.key,
    column: iss.state,
    columnLabel: stateLabel(iss.state),
    title: iss.title,
    tags: iss.tags ?? [],
    assignees: assigneeList(assignee),
    claude: assignee === 'claude',
    taken: !!iss.taken,
    waitingForClaim: !!iss.waiting_for_claim,
  };
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

// boardWithSync folds a SyncStatusApi (possibly undefined) into a
// Board. Centralised so listBoards and addRepository stay in lockstep.
function boardWithSync(
  prefix: string,
  name: string,
  issueCount: number,
  sync: SyncStatusApi | undefined,
): Board {
  return {
    prefix,
    name,
    issueCount,
    syncEnabled: sync?.configured ?? false,
    syncInProgress: sync?.in_progress ?? false,
    syncLastAt: sync?.last_sync_at,
    syncLastError: sync?.last_error,
  };
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
export interface AddRepositoryPayload {
  path: string;
  name: string;
  prefix?: string;
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
  interface ApiRepo {
    prefix: string;
    name: string;
    path: string;
  }
  const repo = await call<ApiRepo>('/repos', { method: 'POST', body });
  // Match listBoards: issueCount=0 on a freshly-created repo. A
  // freshly-added repo almost never has sync configured yet — the
  // zero SyncStatusApi gives syncEnabled=false. The next listBoards
  // refresh picks up real sync status from GET /sync.
  return boardWithSync(repo.prefix, repo.name, 0, undefined);
}

interface ApiCommentEnvelope { author: string; body: string; created_at: string; }
interface ApiPR { url: string; }
interface ApiDocLink {
  document_filename: string;
  document_type: string;
  description?: string;
}
interface ApiClaimant {
  session_id: string;
  agent_name: string;
  prompt: string;
  claimed_at: string;
  released_at: string | null;
}
interface ApiIssueView {
  issue: ApiIssue;
  comments: ApiCommentEnvelope[];
  pull_requests: ApiPR[];
  documents: ApiDocLink[];
  claimants: ApiClaimant[];
  taken: boolean;
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
  const iss = view.issue;
  const assignee = iss.assignee ?? '';
  return {
    key: iss.key,
    column: iss.state,
    columnLabel: stateLabel(iss.state),
    title: iss.title,
    description: iss.description ?? '',
    tags: iss.tags ?? [],
    assignees: assigneeList(assignee),
    claude: assignee === 'claude',
    comments: (view.comments ?? []).map(c => ({
      author: c.author, body: c.body, createdAt: c.created_at,
    })),
    pullRequests: (view.pull_requests ?? []).map(p => ({ url: p.url })),
    documents: (view.documents ?? []).map(d => ({
      filename: d.document_filename,
      type: d.document_type,
      description: d.description ?? '',
    })),
    claimants: (view.claimants ?? []).map(c => ({
      sessionId: c.session_id,
      agentName: c.agent_name,
      prompt: c.prompt,
      claimedAt: c.claimed_at,
      releasedAt: c.released_at,
      open: c.released_at == null,
    })),
    taken: !!view.taken,
  };
}

interface ApiBriefDoc {
  filename: string;
  type: string;
  description?: string;
  source_path?: string;
  linked_via?: string[];
  content?: string;
}

interface ApiRelation {
  // ListIssueRelations emits model.Relation, which serialises as
  // {from_issue, to_issue, type, id, created_at}. The brief reshape
  // resolves these into RelationDTO entries with the "other end" key.
  from_issue: string;
  to_issue: string;
  type: string;
}

interface ApiIssueRelations {
  outgoing?: ApiRelation[] | null;
  incoming?: ApiRelation[] | null;
}

interface ApiFeatureRef {
  slug: string;
  title: string;
}

interface ApiIssueBrief {
  issue: ApiIssue;
  feature?: ApiFeatureRef | null;
  relations?: ApiIssueRelations | null;
  pull_requests?: ApiPR[] | null;
  documents?: ApiBriefDoc[] | null;
  comments?: ApiCommentEnvelope[] | null;
  claimants?: ApiClaimant[] | null;
  taken?: boolean;
  warnings?: string[] | null;
}

function reshapeApiBrief(view: ApiIssueBrief): IssueBriefDTO {
  const iss = view.issue;
  const assignee = iss.assignee ?? '';
  const meta: IssueMetaDTO = {
    key: iss.key,
    column: iss.state,
    columnLabel: stateLabel(iss.state),
    title: iss.title,
    description: iss.description ?? '',
    tags: iss.tags ?? [],
    assignees: assigneeList(assignee),
    claude: assignee === 'claude',
    taken: !!view.taken,
    waitingForClaim: !!iss.waiting_for_claim,
  };
  const feat: FeatureRefDTO | null = view.feature
    ? { slug: view.feature.slug, title: view.feature.title }
    : null;
  const outgoing: RelationDTO[] = (view.relations?.outgoing ?? []).map(r => ({
    type: r.type,
    otherKey: r.to_issue,
  }));
  const incoming: RelationDTO[] = (view.relations?.incoming ?? []).map(r => ({
    type: r.type,
    otherKey: r.from_issue,
  }));
  return {
    issue: meta,
    feature: feat,
    relations: { outgoing, incoming },
    pullRequests: (view.pull_requests ?? []).map(p => ({ url: p.url })),
    documents: (view.documents ?? []).map(d => ({
      filename: d.filename,
      type: d.type,
      description: d.description ?? '',
      sourcePath: d.source_path ?? '',
      linkedVia: d.linked_via ?? [],
      content: d.content ?? '',
    })),
    comments: (view.comments ?? []).map(c => ({
      author: c.author, body: c.body, createdAt: c.created_at,
    })),
    claimants: (view.claimants ?? []).map(c => ({
      sessionId: c.session_id,
      agentName: c.agent_name,
      prompt: c.prompt,
      claimedAt: c.claimed_at,
      releasedAt: c.released_at,
      open: c.released_at == null,
    })),
    taken: !!view.taken,
    waitingForClaim: !!iss.waiting_for_claim,
    warnings: view.warnings ?? [],
  };
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

interface ApiDocument {
  filename: string;
  type: string;
  size_bytes: number;
  updated_at: string;
  content?: string;
}

export async function listDocs(repoPrefix: string, typeFilter = ''): Promise<DocSummary[]> {
  if (!repoPrefix || repoPrefix === 'all') {
    throw new Error('select a repository to view its documents');
  }
  const docs = await call<ApiDocument[]>(`/repos/${repoPrefix}/documents`, {
    query: { type: typeFilter || undefined },
  });
  return docs.map(d => ({
    filename: d.filename,
    type: d.type,
    sizeBytes: d.size_bytes,
    updatedAt: d.updated_at,
  }));
}

interface ApiDispatch {
  id: number;
  issue_key?: string;
  target_agent_name?: string;
  mode?: string;
  status: string;
  payload?: string;
  created_by?: string;
  created_at: string;
}

function reshapeDispatch(d: ApiDispatch): DispatchDTO {
  return {
    id: d.id,
    issueKey: d.issue_key ?? '',
    targetAgent: d.target_agent_name ?? '',
    mode: d.mode ?? '',
    status: d.status,
    payload: d.payload ?? '',
    createdBy: d.created_by ?? '',
    createdAt: d.created_at,
  };
}

// dispatchIssue queues a state-gated auto-pick dispatch (BACI-40). The
// server re-checks the stage's state-gate against the issue's current
// state and picks the most-recently-active free agent — the caller
// names neither an agent nor a note. Errors from the server (no free
// agent / state-gate mismatch) come back as the error envelope and
// surface through reportError() up in App.jsx.
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
  await call<unknown>(`/agents/dispatches/${dsp.id}/cancel`, { method: 'POST' });
}

interface ApiBoardCard {
  // setIssueState returns the model.Issue; same reshape as listCards.
  key: string;
  state: string;
  title: string;
  assignee?: string;
  tags?: string[];
  taken?: boolean;
  waiting_for_claim?: boolean;
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

export async function addComment(
  repoPrefix: string,
  key: string,
  author: string,
  body: string,
): Promise<IssueDetail> {
  if (!repoPrefix || repoPrefix === 'all') {
    const i = key.lastIndexOf('-');
    if (i <= 0) throw new Error(`invalid issue key: ${key}`);
    repoPrefix = key.slice(0, i);
  }
  const effectiveAuthor = author?.trim() || readActor() || 'web';
  await call<unknown>(`/repos/${repoPrefix}/issues/${key}/comments`, {
    method: 'POST',
    body: { author: effectiveAuthor, body },
  });
  return getIssue(repoPrefix, key);
}

interface ApiFeature {
  slug: string;
  title: string;
  description?: string;
  created_at: string;
  updated_at: string;
}

export async function listFeatures(repoPrefix: string): Promise<FeatureSummary[]> {
  if (!repoPrefix || repoPrefix === 'all') {
    throw new Error('select a repository to view its features');
  }
  const feats = await call<ApiFeature[]>(`/repos/${repoPrefix}/features`);
  return feats.map(f => ({ slug: f.slug, title: f.title, updatedAt: f.updated_at }));
}

interface ApiFeatureView {
  feature: ApiFeature;
  issues: Array<{ key: string; title: string; state: string }>;
}

export async function getFeature(repoPrefix: string, slug: string): Promise<FeatureDetail> {
  if (!repoPrefix || repoPrefix === 'all') {
    throw new Error('select a repository to view its features');
  }
  const view = await call<ApiFeatureView>(`/repos/${repoPrefix}/features/${slug}`);
  const f = view.feature;
  return {
    slug: f.slug,
    title: f.title,
    description: f.description ?? '',
    createdAt: f.created_at,
    updatedAt: f.updated_at,
    issues: (view.issues ?? []).map(iss => ({
      key: iss.key,
      title: iss.title,
      state: iss.state,
      stateLabel: stateLabel(iss.state),
    })),
  };
}

interface ApiHistoryEntry {
  id: number;
  actor: string;
  op: string;
  kind?: string;
  target_label?: string;
  details?: string;
  created_at: string;
}

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
  const entries = (hasMore ? rows.slice(0, pageSize) : rows).map(e => ({
    id: e.id,
    actor: e.actor,
    op: e.op,
    kind: e.kind ?? '',
    targetLabel: e.target_label ?? '',
    details: e.details ?? '',
    createdAt: e.created_at,
  }));
  return { entries, page, pageSize, hasMore };
}

interface ApiDocView {
  document: ApiDocument & { content: string };
  links: unknown[];
}

export async function getDoc(repoPrefix: string, filename: string): Promise<DocContent> {
  if (!repoPrefix || repoPrefix === 'all') {
    throw new Error('select a repository to view its documents');
  }
  const view = await call<ApiDocView>(`/repos/${repoPrefix}/documents/${filename}`);
  const d = view.document;
  return {
    filename: d.filename,
    type: d.type,
    content: d.content ?? '',
    updatedAt: d.updated_at,
  };
}

export async function saveDoc(
  repoPrefix: string,
  filename: string,
  content: string,
): Promise<DocContent> {
  if (!repoPrefix || repoPrefix === 'all') {
    throw new Error('select a repository to save documents');
  }
  // PUT (upsert) mirrors the desktop's SaveDoc / CLI's `doc upsert` —
  // the document already exists from the list, and PATCH would be a
  // body-only edit. Keep the existing type by re-fetching after the
  // write; the upsert handler only returns the persisted row.
  await call<unknown>(`/repos/${repoPrefix}/documents/${filename}`, {
    method: 'PUT',
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

interface ApiPromptTemplate {
  slug: string;
  mode: string;
  label: string;
  body: string;
  default: string;
  is_builtin: boolean;
  is_default: boolean;
  allowed_states: string[];
  default_states: string[];
  states_are_default: boolean;
  concurrency_limit?: number;
  default_concurrency_limit?: number;
  concurrency_is_default?: boolean;
  // BACI-67: imperative override for the dispatch action menus.
  action_label?: string;
  default_action_label?: string;
  action_label_is_default?: boolean;
}

function reshapeTemplate(t: ApiPromptTemplate): PromptTemplateDTO {
  return {
    slug: t.slug,
    mode: t.mode,
    label: t.label,
    body: t.body,
    default: t.default,
    isBuiltin: t.is_builtin,
    isDefault: t.is_default,
    allowedStates: t.allowed_states ?? [],
    defaultStates: t.default_states ?? [],
    statesAreDefault: t.states_are_default,
    concurrencyLimit: t.concurrency_limit ?? 0,
    defaultConcurrencyLimit: t.default_concurrency_limit ?? 0,
    concurrencyIsDefault: t.concurrency_is_default ?? true,
    actionLabel: t.action_label ?? '',
    defaultActionLabel: t.default_action_label ?? '',
    actionLabelIsDefault: t.action_label_is_default ?? true,
  };
}

export async function listPromptTemplates(): Promise<PromptTemplateDTO[]> {
  // BACI-50 added the composite /settings/templates/full endpoint that
  // returns the rich DTO — labels, defaults, is_builtin, and the
  // BACI-51 concurrency fields all flow through in one round-trip,
  // no client-side label derivation or placeholder fields needed.
  const rows = await call<ApiPromptTemplate[]>('/settings/templates/full');
  return (rows ?? []).map(reshapeTemplate);
}

// Refetch every template and return the one DTO the caller updated —
// SavePromptTemplate / SavePromptStates only return the persisted
// row's body or states, not the full DTO; fetching once after the
// write keeps the caller's `templates` state consistent.
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

export async function savePromptStates(
  mode: string,
  states: string[],
): Promise<PromptTemplateDTO> {
  if (states.length === 0) {
    await call<unknown>(`/settings/templates/${mode}/states`, { method: 'DELETE' });
  } else {
    await call<unknown>(`/settings/templates/${mode}/states`, {
      method: 'PUT',
      body: { states },
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
  states: string[],
  actionLabel: string = '',
): Promise<PromptTemplateDTO> {
  // BACI-67: forward actionLabel verbatim — an empty string is the
  // "no override, derive from name" sentinel that the Go side honours.
  const raw = await call<ApiPromptTemplate>('/settings/templates', {
    method: 'POST',
    body: { slug, name, body, states, action_label: actionLabel },
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

interface ApiRestoreResponse {
  restored: string[];
  templates: ApiPromptTemplate[];
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

// ---------- Board preferences (BACI-47/D) ----------

export async function getBoardPreferences(): Promise<BoardPreferencesDTO> {
  const res = await call<{ hide_empty_columns: boolean }>('/settings/board-preferences');
  return { hideEmptyColumns: res.hide_empty_columns };
}

export async function setBoardPreferences(hideEmptyColumns: boolean): Promise<BoardPreferencesDTO> {
  const res = await call<{ hide_empty_columns: boolean }>('/settings/board-preferences', {
    method: 'PUT',
    body: { hide_empty_columns: hideEmptyColumns },
  });
  return { hideEmptyColumns: res.hide_empty_columns };
}

// ---------- Display preferences (BACI-68) ----------
//
// display.show_archived global toggle — when on, default lists / board
// / kanban views include archived rows; when off (the default) they're
// hidden. The CLI's per-call --include-archived flag overrides this
// setting for one call; the desktop / web UIs have no per-call knob,
// so the setting is the single source of truth here.

export type DisplayPreferencesDTO = { showArchived: boolean };

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
// the server's lease, not this page's. App.jsx polls this on a 10s
// cadence (matching POLL_INTERVAL_MS, which mirrors UILeaderHeartbeatInterval).
export async function getLeaderStatus(): Promise<LeaderStatusDTO> {
  const res = await call<{ amLeader: boolean; holderLabel: string }>('/leader');
  return { amLeader: !!res.amLeader, holderLabel: res.holderLabel ?? '' };
}
