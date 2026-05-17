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
  syncEnabled: boolean;
}

export interface BoardColumn {
  state: string;
  label: string;
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
  // an issue count and a syncEnabled flag. Issue count comes from a
  // per-repo issue-list query; syncEnabled isn't readable over HTTP at
  // all (lives in a machine-local config file), so it stays false in
  // the web build.
  const repos = await call<Array<{ prefix: string; name: string }>>('/repos');
  const boards: Board[] = [];
  for (const r of repos) {
    const issues = await call<ApiIssue[]>(`/repos/${r.prefix}/issues`);
    boards.push({
      prefix: r.prefix,
      name: r.name,
      issueCount: issues.length,
      syncEnabled: false,
    });
  }
  return boards;
}

export async function listColumns(): Promise<BoardColumn[]> {
  // Static — every state, in canonical order. No fetch.
  return Object.entries(STATE_LABELS).map(([state, label]) => ({ state, label }));
}

export async function listCards(repoPrefix: string): Promise<BoardCard[]> {
  // The "all repos" pseudo-board isn't directly addressable over REST;
  // a v2 follow-up could add `GET /issues?repo=all`. For now require a
  // concrete prefix in web mode.
  if (!repoPrefix || repoPrefix === 'all') {
    throw new Error('select a repository to view its board');
  }
  const issues = await call<ApiIssue[]>(`/repos/${repoPrefix}/issues`);
  return issues.map(cardFromIssue);
}

export async function addRepository(): Promise<Board> {
  // No browser equivalent of a native folder picker. A v2 follow-up
  // could surface a path-input modal and POST /repos with the typed
  // path — see docs/web-app-mode.md.
  throw new WebModeUnavailableError('Add repository (native folder picker)');
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

export async function listAgents(_repoPrefix: string): Promise<AgentCard[]> {
  // The agent-registry REST surface exists (BACI-34) but assembling
  // the AgentCard shape requires several joins the desktop service
  // does locally (per-session claim fetch, dispatch bucketing, busy /
  // waiting derivation). Web mode hides the Agents tab entirely
  // rather than reproduce that join chain here. See
  // docs/web-app-mode.md for the v2 plan.
  throw new WebModeUnavailableError('Agents view');
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

// ---------- Prompt templates (BACI-47/B+C) ----------
//
// Body + state-gate CRUD landed in BACI-36 (PUT/DELETE
// /settings/templates/{mode} and …/{mode}/states); the list endpoints
// (GET /settings/templates, /settings/templates/states) round it out.
// Web mode wires the per-template body textarea + state chips against
// those routes; the typed CRUD (add/rename/delete/restore-defaults)
// is still local-only — the four stubs below throw and the matching
// affordances are hidden in SettingsView.

// Built-in slugs + display labels mirror model.BuiltinTemplateSlugs() /
// BuiltinTemplateLabel(); the REST surface doesn't carry the template's
// display name (it's a `prompt_templates.name` column, not in the
// app_settings-era GET maps), so the web bundle derives it client-side.
const BUILTIN_SLUGS = new Set(['plan', 'implement', 'review', 'ship', 'fix_review']);
const BUILTIN_LABELS: Record<string, string> = {
  plan: 'Plan',
  implement: 'Implement',
  review: 'Review',
  ship: 'Ship',
  fix_review: 'Fix review',
};
function deriveLabel(slug: string): string {
  // Built-ins have known display names; user-created slugs fall back
  // to a title-cased version of the slug ("spike" → "Spike").
  if (BUILTIN_LABELS[slug]) return BUILTIN_LABELS[slug];
  return slug.replace(/[-_]+/g, ' ').replace(/\b\w/g, c => c.toUpperCase());
}

export async function listPromptTemplates(): Promise<PromptTemplateDTO[]> {
  // Parallel fetch — both maps are small and the two requests are
  // independent. /settings/templates is the authoritative source of
  // slugs; a slug missing from /settings/templates/states reads as an
  // empty state-gate (CLI-only template).
  const [bodies, states] = await Promise.all([
    call<Record<string, string>>('/settings/templates'),
    call<Record<string, string[]>>('/settings/templates/states'),
  ]);
  // Stable, alphabetical iteration order — the desktop bindings use
  // the store's insertion order, but the REST map is unordered.
  const slugs = Object.keys(bodies).sort();
  return slugs.map(slug => ({
    slug,
    mode: slug,
    label: deriveLabel(slug),
    body: bodies[slug],
    // Embedded defaults aren't on the REST surface. The desktop's
    // "Reset body" path uses these to render an indicator; web mode
    // lives without it for now.
    default: '',
    isBuiltin: BUILTIN_SLUGS.has(slug),
    isDefault: false,
    allowedStates: states[slug] ?? [],
    defaultStates: [],
    statesAreDefault: false,
  }));
}

// Refetch both maps and return the one DTO the caller updated — gives
// the SettingsView setter the freshly resolved row without separately
// fetching the body and state-gate.
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

export async function addPromptTemplate(): Promise<PromptTemplateDTO> {
  throw new WebModeUnavailableError('Add prompt template');
}

export async function renamePromptTemplate(): Promise<PromptTemplateDTO> {
  throw new WebModeUnavailableError('Rename prompt template');
}

export async function deletePromptTemplate(): Promise<PromptTemplateDTO> {
  throw new WebModeUnavailableError('Delete prompt template');
}

export async function restoreBuiltinPromptTemplates(): Promise<PromptTemplateDTO[]> {
  throw new WebModeUnavailableError('Restore built-in prompt templates');
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

// ---------- Local-only stubs ----------

export async function getLeaderStatus(): Promise<LeaderStatusDTO> {
  // The browser doesn't run the leader election (per-process Wails
  // concept), so it can never be the leader. App.jsx force-defaults
  // amLeader to true in WEB_MODE so dispatch-gating doesn't refuse
  // mutations the user triggers; this read is here for callers that
  // still want a value-shaped response.
  return { amLeader: false, holderLabel: '' };
}
