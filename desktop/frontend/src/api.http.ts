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

// SyncRegistryApi / SyncRepoApi / MemberProjectApi / UnsyncedProjectApi
// mirror api.SyncRegistryOut and friends — the wire shape of the
// BACI-107 GET /sync/repos endpoint. Snake-case on the wire matches
// every other api.http.ts wire DTO; getSyncRegistry() below reshapes
// to the camelCase SyncRegistry the React tree consumes.
interface SyncRegistryApi {
  sync_repos: SyncRepoApi[];
  unsynced_projects: UnsyncedProjectApi[];
}

interface SyncRepoApi {
  remote_url: string;
  label: string;
  local_path: string;
  cloned_at: string;
  last_sync_at?: string;
  last_error?: string;
  in_progress: boolean;
  projects: MemberProjectApi[];
}

interface MemberProjectApi {
  prefix: string;
  name: string;
  uuid?: string;
  status: 'linked' | 'phantom' | 'absent';
}

interface UnsyncedProjectApi {
  prefix: string;
  name: string;
  uuid: string;
  path: string;
}

// SyncRegistry / SyncRepoEntry / MemberProject / UnsyncedProject are
// the camelCase DTOs the React tree consumes. Same shape as the
// SyncRegistryApi wire types, just snake → camel on the field names.
export interface SyncRegistry {
  syncRepos: SyncRepoEntry[];
  unsyncedProjects: UnsyncedProject[];
}

export interface SyncRepoEntry {
  remoteUrl: string;
  label: string;
  localPath: string;
  clonedAt: string;
  lastSyncAt?: string;
  lastError?: string;
  inProgress: boolean;
  projects: MemberProject[];
}

export interface MemberProject {
  prefix: string;
  name: string;
  uuid?: string;
  status: 'linked' | 'phantom' | 'absent';
}

export interface UnsyncedProject {
  prefix: string;
  name: string;
  uuid: string;
  path: string;
}

export interface BoardColumn {
  state: string;
  label: string;
}

export interface BoardCardTodo {
  content: string;
  status: string;
}

// FollowOnInfo (BACI-182) is the dormant follow-on dispatch the kanban
// card chip renders. Mirrors internal/boardcards/cards.go::FollowOnInfo;
// surfaced server-side via BoardCard.followOnDispatch.
export interface FollowOnInfo {
  mode: string;
  actionLabel: string;
  dispatchID: number;
}

// BoardCardBlocker (BACI-114) is one open `blocks` edge pointing AT
// a card. Title is intentionally NOT on the wire — the kanban
// popover joins it from the same `cards` array (Option A: thin
// renderer over data the brief already has).
export interface BoardCardBlocker {
  key: string;
  state: string; // todo | in_progress | needs_action | in_review — open-state set
}

// BACI-145: WaitingKind / WaitingState mirror the Go-side types in
// internal/boardcards/cards.go. Kept as a hand-written copy here (the
// web bundle doesn't import the Wails bindings — those drag in
// runtime-only deps unavailable in the browser). See api.ts for the
// Wails-driven import path; both files re-export the same names so
// React components can switch transports without a type rewrite.
export type WaitingKind = 'queued_no_agent' | 'queued_blocked' | 'delivered';

export interface WaitingState {
  kind: WaitingKind;
  mode?: string;
  actionLabel?: string;
}

// BACI-192: BoardCardFollowOn is the denormalised view of the dormant
// follow-on dispatch attached to this issue's currently in-flight
// (parent) dispatch — single-slot per issue, drives the kanban
// footer's follow-on button visual state. Mirror of the Go-side
// boardcards.BoardCardFollowOn; api.ts re-exports the Wails-binding
// equivalent under the same name.
export interface BoardCardFollowOn {
  mode: string;
  actionLabel: string;
}

// LatestPlan (BACI-216) is the newest `plan`-typed doc linked
// directly to an issue. Drives the per-card plan icon on the
// kanban and the prominent "Open plan" link in the workspace
// header. Mirror of model.LatestPlan; api.ts re-exports the
// Wails-binding equivalent under the same name.
export interface LatestPlan {
  documentId: number;
  uuid: string;
  filename: string;
  updatedAt: string;
}
// LatestPlanDTO is the Wails-shaped alias — kept as a parallel name
// so a component importing either flavour gets the same shape.
export type LatestPlanDTO = LatestPlan;

export interface BoardCard {
  key: string;
  column: string;
  columnLabel: string;
  title: string;
  // BACI-171: short (~140-char) excerpt of the issue description used
  // by the bottom-right ActivityTray to render a one-or-two-line
  // summary per entry. Absent (server-side omitempty) when the issue
  // has no description; the kanban card itself ignores this field.
  descriptionExcerpt?: string;
  tags: string[];
  assignees: string[];
  claude: boolean;
  taken: boolean;
  // BACI-172: per-feature glyph denormalised onto the card by the
  // server (via the issue → feature join in store.issueSelect). Empty
  // (and absent on the wire) when the issue has no feature, or the
  // feature has no emoji set. The kanban card renderer only paints
  // the top-left slot when truthy.
  featureEmoji?: string;
  // BACI-145: structured "why is this card waiting?" state. Replaces
  // the two booleans waitingForClaim + waitingDispatchDelivered that
  // the BoardCard used to carry — neither could carry the active
  // dispatch's mode for the inline label. Absent (omitempty
  // server-side) when the card isn't waiting; the KanbanCard renders
  // no spinner in that case.
  waitingState?: WaitingState | null;
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
  // BACI-114: the open-state `blocks` edges pointing at this card.
  // Empty (and omitted from JSON) when nothing is blocking — the
  // kanban indicator only lights up when at least one entry is
  // present. Absent in older payloads so the renderer must null-
  // check (`card.blockedBy?.length ?? 0`).
  blockedBy?: BoardCardBlocker[];
  // BACI-187: the BACI-138 terminal_at stamp — non-null on done /
  // cancelled cards, absent on open cards. The topbar Shipped pill
  // derives its "last 7 days" count client-side from this field on
  // the already-polled cards array; the binding twin in api.ts mirrors
  // the same wire shape.
  terminalAt?: string;
  // BACI-192: dormant follow-on dispatch denormalised onto the card so
  // the kanban footer can render its mode label without an extra
  // per-card fetch. Absent (server-side omitempty) when the issue has
  // no dormant follow-on — the footer button stays in its outline
  // state in that case. Set optimistically by the App-level handlers
  // on a successful queue / cancel; the next 10s poll re-asserts the
  // authoritative shape.
  followOn?: BoardCardFollowOn | null;
  // BACI-216: the newest `plan`-typed doc linked directly to this
  // issue, or absent when none. Drives the per-card plan icon that
  // opens the doc viewer. Server-side omitempty drops the field
  // when no plan is linked; the cards endpoint already serves
  // camelCase via the Go json tag so no reshape is needed in
  // listCards().
  latestPlan?: LatestPlan | null;
}

// ShippedIssueDTO (BACI-187) is one row in the topbar shipping-log
// popover. Mirrors desktop/boardservice.go:ShippedIssueDTO and the
// /repos/{prefix}/shipped wire shape exactly — the React-side
// ShippedPopover imports the type from `./api` (this file in web
// mode, api.ts in desktop mode) without reshape.
export interface ShippedIssueDTO {
  key: string;
  title: string;
  terminalAt: string;
  tags: string[];
  featureSlug?: string;
  featureEmoji?: string;
  prUrl?: string;
}

export interface CommentDTO {
  uuid: string;
  author: string;
  body: string;
  createdAt: string;
  // BACI-131 eval-comment fields. eval is true when the row was
  // posted from the kanban card's quick-eval composer; the triple
  // (agentSessionId, dispatchId, mode) is the in-flight context
  // captured server-side at write time. agentName is the persistent
  // agent identity slug resolved from agentSessionId at read time
  // (via a JOIN on agent_sessions → agents) — never persisted on
  // the row. All five fields are zero values on a normal (non-eval)
  // comment.
  eval?: boolean;
  agentSessionId?: string;
  dispatchId?: number;
  mode?: string;
  agentName?: string;
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
  // BACI-216: newest `plan`-typed doc linked directly to this
  // issue; null when none. See LatestPlan above.
  latestPlan?: LatestPlan | null;
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
  // BACI-216: newest `plan`-typed doc linked directly to this
  // issue, surfaced so IssueWorkspace's header can render the
  // prominent "Open plan" link without descending into the brief's
  // documents list.
  latestPlan?: LatestPlan | null;
}

export interface LinkedDocDTO {
  filename: string;
  type: string;
  description: string;
  sourcePath?: string;
  linkedVia: string[];
  // sizeBytes is the doc's on-disk size in bytes. Always populated, even
  // when content is omitted from the brief (BACI-115 keeps transcript
  // bodies out of the inlined payload but carries the size so a consumer
  // can render "body not inlined — N KB" instead of "body is empty").
  sizeBytes: number;
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
  // BACI-145: structured waiting state for the IssueLockBanner.
  // Absent (server-side omitempty) when the issue isn't waiting.
  waitingState?: WaitingState | null;
  // BACI-216: duplicated alongside issue.latestPlan so envelope
  // consumers don't have to descend into the meta block. The two
  // are always in lockstep — same source-of-truth lookup.
  latestPlan?: LatestPlan | null;
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
  // BACI-132: per-dispatch scope so two dispatches on the same
  // (session, issue) get separate task lists. Absent on pre-BACI-132
  // rows and on orphan rows the hook couldn't attribute to an
  // in-flight dispatch. Optional on the wire (omitempty server-side).
  // The JSON tag is `dispatch_id` (snake_case) to match the
  // model.SessionTodo wire shape exposed by the unfiltered
  // /agents/sessions/{id}/todos endpoint — the AgentCard DTO uses
  // the same key for symmetry.
  dispatch_id?: number;
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

// DocSummaryLink (BACI-204) is one link row on a DocSummary —
// matches the desktop Wails-side DocSummaryLinkDTO so callers can
// share the rendering between transports.
export interface DocSummaryLink {
  issueKey?: string;
  featureSlug?: string;
  description?: string;
}

export interface DocSummary {
  filename: string;
  type: string;
  sizeBytes: number;
  updatedAt: string;
  // BACI-204: lifecycle dates + the per-row snippet + linked-issue /
  // linked-feature chips ride back on every row so the redesigned
  // Documents page renders without an N+1 per-row round trip.
  createdAt: string;
  archivedAt?: string;
  snippet?: string;
  links?: DocSummaryLink[];
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
  // BACI-184: per-feature emoji glyph for the Features panel list.
  // Empty when none is set — the list renders nothing (no placeholder)
  // in that case. Mirrors FeatureDetail.emoji so both surfaces share
  // the same glyph BACI-172 paints on every kanban card.
  emoji: string;
  // BACI-199: three-state column on the feature row — `active`
  // (default), `done` (delivered) or `cancelled` (abandoned). The
  // Features panel renders a state pill so a glance distinguishes
  // work in flight from delivered / abandoned work.
  state: string;
  updatedAt: string;
  // BACI-177: per-feature "Show on board" toggle state. When true,
  // every kanban card belonging to this feature is hidden from the
  // board on this machine. Lives in the per-repo board-hide KV
  // (tui_settings), not on the features row.
  hiddenOnBoard: boolean;
}

export interface FeatureLinkedIssue {
  key: string;
  title: string;
  state: string;
  stateLabel: string;
}

// FeatureCommentDTO mirrors model.FeatureComment (BACI-124) — the
// feature-scoped chronological-handoff scratchpad row.
export interface FeatureCommentDTO {
  uuid: string;
  author: string;
  body: string;
  createdAt: string;
}

// FeatureLinkedDoc (BACI-214) is one document linked to the feature
// via `bacio doc link <file> <feature-slug>`. Drives the "Documents"
// section on FeaturesView's detail pane. Deliberately narrower than
// the issue brief's LinkedDocDTO (no `linkedVia` / no `sizeBytes`):
// on the feature pane the source is unambiguous (the feature itself),
// and the size readout would cost an extra round-trip per doc.
// `sourcePath` is reserved for parity with the desktop binding but
// stays empty today.
export interface FeatureLinkedDoc {
  filename: string;
  type: string;
  description: string;
  sourcePath?: string;
}

export interface FeatureDetail {
  slug: string;
  title: string;
  description: string;
  // BACI-172: per-feature emoji rendered on every kanban card under
  // this feature. Empty when none is set.
  emoji: string;
  // BACI-199: three-state column on the feature row + sticky bit.
  // The drawer's segmented control reads `state` to highlight the
  // active button and `stateManual` to flag rows pinned against the
  // auto-completion sweep.
  state: string;
  stateManual: boolean;
  createdAt: string;
  updatedAt: string;
  issues: FeatureLinkedIssue[];
  comments: FeatureCommentDTO[];
  // BACI-214: documents linked to this feature via
  // `bacio doc link <file> <feature-slug>`. Each row is a link into
  // the canonical /documents/<filename> viewer.
  documents: FeatureLinkedDoc[];
  // BACI-177: per-feature "Show on board" toggle state. When true,
  // every kanban card belonging to this feature is hidden from the
  // board on this machine.
  hiddenOnBoard: boolean;
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
  // BACI-172: per-feature glyph denormalised from the join in
  // store.issueSelect. Snake-case on the wire; the cardFromIssue
  // reshape below copies it onto the camelCase BoardCard.featureEmoji.
  feature_emoji?: string;
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
    // BACI-172: thread the joined feature emoji through so a card
    // refreshed via setIssueState() (drag-to-move) keeps its glyph
    // until the next listCards() rebuilds the array.
    featureEmoji: iss.feature_emoji,
    // BACI-145: setIssueState (a drag-to-move) is blocked for waiting
    // cards by the UI, so the wire shape produced here can't be
    // observably waiting. Leave waitingState undefined; the next 10s
    // poll re-runs listCards and re-populates it from the server.
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

// getSyncRegistry fetches BACI-107's GET /sync/repos and reshapes the
// snake-case wire payload to the camelCase SyncRegistry the React
// tree consumes. The reshape is mechanical — every field is renamed
// 1:1 — so the helper stays a thin map() over the two slices.
export async function getSyncRegistry(): Promise<SyncRegistry> {
  const wire = await call<SyncRegistryApi>('/sync/repos');
  return {
    syncRepos: wire.sync_repos.map(reshapeSyncRepo),
    unsyncedProjects: wire.unsynced_projects.map(reshapeUnsyncedProject),
  };
}

function reshapeSyncRepo(r: SyncRepoApi): SyncRepoEntry {
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

function reshapeMemberProject(m: MemberProjectApi): MemberProject {
  return { prefix: m.prefix, name: m.name, uuid: m.uuid, status: m.status };
}

function reshapeUnsyncedProject(u: UnsyncedProjectApi): UnsyncedProject {
  return { prefix: u.prefix, name: u.name, uuid: u.uuid, path: u.path };
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

interface ApiCommentEnvelope {
  uuid: string;
  author: string;
  body: string;
  created_at: string;
  // BACI-131 — wire-shape mirror of model.Comment. eval is always
  // present on the wire (the server omits no field), agent_name is
  // populated only by the JOIN-aware list endpoint. The four context
  // fields stay omitted on normal comments via the model's omitempty.
  eval?: boolean;
  agent_session_id?: string;
  dispatch_id?: number;
  mode?: string;
  agent_name?: string;
}
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
  // BACI-216: snake-cased wire shape — see ApiLatestPlan above.
  latest_plan?: ApiLatestPlan | null;
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
    comments: (view.comments ?? []).map(mapApiComment),
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
    latestPlan: mapApiLatestPlan(view.latest_plan),
  };
}

// mapApiComment normalises the wire-shape envelope (snake_case,
// always-defined-but-maybe-zero context fields) to the CommentDTO
// the React tree consumes (camelCase, optional-when-zero context).
// Shared by getIssue() and reshapeApiBrief() so the BACI-131 fields
// stay in lock-step across both read paths.
function mapApiComment(c: ApiCommentEnvelope): CommentDTO {
  return {
    uuid: c.uuid,
    author: c.author,
    body: c.body,
    createdAt: c.created_at,
    eval: !!c.eval,
    agentSessionId: c.agent_session_id ?? '',
    dispatchId: c.dispatch_id,
    mode: c.mode ?? '',
    agentName: c.agent_name ?? '',
  };
}

interface ApiBriefDoc {
  filename: string;
  type: string;
  description?: string;
  source_path?: string;
  linked_via?: string[];
  size_bytes?: number;
  content?: string;
}

// ApiLatestPlan is the snake_case wire shape of model.LatestPlan — the
// per-issue plan projection BACI-216 attaches to the issue show / brief
// payloads. Reshaped into the camelCase LatestPlan the UI consumes by
// mapApiLatestPlan below. The cards endpoint already serves camelCase
// via the Go json tag so the listCards() path doesn't go through this
// reshape — only the snake-cased show / brief handlers do.
interface ApiLatestPlan {
  document_id: number;
  uuid: string;
  filename: string;
  updated_at: string;
}

function mapApiLatestPlan(p: ApiLatestPlan | null | undefined): LatestPlan | null {
  if (!p) return null;
  return {
    documentId: p.document_id,
    uuid: p.uuid,
    filename: p.filename,
    updatedAt: p.updated_at,
  };
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
  // BACI-145: snake-case wire shape mirroring internal/api/views.go
  // IssueBrief.WaitingState. WaitingState's own field names stay
  // camelCase server-side (the boardcards JSON tags are camel) so no
  // per-field rename here — only the outer wrapper is snake.
  waiting_state?: WaitingState | null;
  // BACI-216: snake-cased wire shape — see ApiLatestPlan above.
  latest_plan?: ApiLatestPlan | null;
  warnings?: string[] | null;
}

function reshapeApiBrief(view: ApiIssueBrief): IssueBriefDTO {
  const iss = view.issue;
  const assignee = iss.assignee ?? '';
  // BACI-216: resolve the latest plan once; both the meta and the
  // envelope-level field carry the same shape.
  const latestPlan = mapApiLatestPlan(view.latest_plan);
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
    latestPlan,
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
      sizeBytes: d.size_bytes ?? 0,
      content: d.content ?? '',
    })),
    comments: (view.comments ?? []).map(mapApiComment),
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
    waitingState: view.waiting_state ?? null,
    latestPlan,
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

// ApiDocumentLink is the raw wire shape for one document_link row
// returned alongside a doc in the list response. Field naming follows
// the Go-side snake_case the API serialises (links carry the issue
// key / feature slug pre-formatted, so the client doesn't need to
// resolve them).
interface ApiDocumentLink {
  issue_key?: string;
  feature_slug?: string;
  description?: string;
}

interface ApiDocument {
  filename: string;
  type: string;
  size_bytes: number;
  updated_at: string;
  created_at: string;
  archived_at?: string;
  content?: string;
  // BACI-204: per-row snippet + link rows hydrated by the store-side
  // IN-query so the Documents page renders chips and previews without
  // an N+1 round trip.
  snippet?: string;
  links?: ApiDocumentLink[];
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
    createdAt: d.created_at,
    archivedAt: d.archived_at,
    snippet: d.snippet,
    links: d.links?.map(l => ({
      issueKey: l.issue_key,
      featureSlug: l.feature_slug,
      description: l.description,
    })),
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
): Promise<BoardCard> {
  if (!repoPrefix || repoPrefix === 'all') {
    throw new Error('addIssue: a repo prefix is required (cross-repo pseudo-board has no target)');
  }
  const iss = await call<ApiIssue>(
    `/repos/${repoPrefix}/issues`,
    { method: 'POST', body: { title, description } },
  );
  return cardFromIssue(iss);
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

// dispatchIssueChain (BACI-209) queues a primary dispatch + a dormant
// follow-on against the same issue in one transaction over HTTP. Same
// auto-pick + state-gate shape as dispatchIssue for the primary; the
// follow-on rides on the next BoardCard refresh via card.followOn.
export async function dispatchIssueChain(
  repoPrefix: string,
  issueKey: string,
  mode: string,
  followOnMode: string,
): Promise<DispatchDTO> {
  if (!repoPrefix || repoPrefix === 'all') {
    const i = issueKey.lastIndexOf('-');
    if (i <= 0) throw new Error(`invalid issue key: ${issueKey}`);
    repoPrefix = issueKey.slice(0, i);
  }
  const raw = await call<ApiDispatch>(
    `/repos/${repoPrefix}/issues/${issueKey}/dispatch-chain`,
    {
      method: 'POST',
      body: { issue_key: issueKey, mode, follow_on_mode: followOnMode },
    },
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

// queueFollowOnDispatch (BACI-180) attaches a dormant follow-on dispatch
// to the issue's in-flight (parent) dispatch in web mode. Mirrors
// dispatchIssue's POST shape — the server resolves the parent via
// WaitingDispatchForIssue and re-runs the state-gate. Errors (no open
// dispatch, state-gate mismatch, single-slot rejection) surface as the
// usual error envelope through reportError() in App.jsx.
export async function queueFollowOnDispatch(
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
    `/repos/${repoPrefix}/issues/${issueKey}/followon`,
    { method: 'POST', body: { issue_key: issueKey, mode } },
  );
  return reshapeDispatch(raw);
}

// cancelFollowOnDispatch (BACI-180) removes the dormant follow-on
// attached to an issue. Idempotent on the backend — a DELETE against
// an issue with no dormant follow-on returns 200 with a JSON-null body,
// which we surface as the zero DispatchDTO (no error). Mirrors the
// Wails seam's CancelFollowOnDispatch behaviour.
export async function cancelFollowOnDispatch(
  repoPrefix: string,
  issueKey: string,
): Promise<DispatchDTO> {
  if (!repoPrefix || repoPrefix === 'all') {
    const i = issueKey.lastIndexOf('-');
    if (i <= 0) throw new Error(`invalid issue key: ${issueKey}`);
    repoPrefix = issueKey.slice(0, i);
  }
  const raw = await call<ApiDispatch | null>(
    `/repos/${repoPrefix}/issues/${issueKey}/followon`,
    { method: 'DELETE' },
  );
  if (!raw) {
    return {
      id: 0,
      issueKey: '',
      targetAgent: '',
      mode: '',
      status: '',
      payload: '',
      createdBy: '',
      createdAt: '',
    } as DispatchDTO;
  }
  return reshapeDispatch(raw);
}

interface ApiBoardCard {
  // setIssueState returns the model.Issue; same reshape as listCards.
  // waiting_for_claim is on the model.Issue wire shape but the
  // cardFromIssue reshape ignores it — see the comment in
  // cardFromIssue for the rationale (drag is blocked on waiting cards).
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

interface ApiFeature {
  slug: string;
  title: string;
  description?: string;
  // BACI-172: per-feature emoji glyph from the wire. Optional /
  // absent on pre-BACI-172 features (or features with no glyph
  // set); model.Feature serialises with omitempty.
  emoji?: string;
  // BACI-199: three-state column + sticky bit. Always present
  // in JSON (no omitempty on model.Feature). Pre-BACI-199 servers
  // (no column wired yet) leave these absent — `state ?? 'active'`
  // is the safe default.
  state?: string;
  state_manual?: boolean;
  created_at: string;
  updated_at: string;
  // BACI-177: per-feature "Show on board" toggle state. Always
  // present in JSON (no omitempty) so the React component reads it
  // without an `?? false`. Defaults to false on a server that hasn't
  // shipped BACI-177 yet — the field will simply be absent.
  hidden_on_board?: boolean;
}

export async function listFeatures(repoPrefix: string): Promise<FeatureSummary[]> {
  if (!repoPrefix || repoPrefix === 'all') {
    throw new Error('select a repository to view its features');
  }
  const feats = await call<ApiFeature[]>(`/repos/${repoPrefix}/features`);
  return feats.map(f => ({
    slug: f.slug,
    title: f.title,
    emoji: f.emoji ?? '',
    state: f.state ?? 'active',
    updatedAt: f.updated_at,
    hiddenOnBoard: !!f.hidden_on_board,
  }));
}

interface ApiFeatureComment {
  uuid: string;
  author: string;
  body: string;
  created_at: string;
}

// ApiFeatureLink mirrors model.DocumentLink (BACI-214) — the snake_case
// wire shape served by `GET /repos/{prefix}/features/{slug}` for each
// linked document. Description is the link-time `--why`. Other
// model.DocumentLink fields (ids, issue/feature foreign keys, created_at)
// aren't surfaced on the feature pane.
interface ApiFeatureLink {
  document_filename: string;
  document_type: string;
  description?: string;
}

interface ApiFeatureView {
  feature: ApiFeature;
  issues: Array<{ key: string; title: string; state: string }>;
  comments?: ApiFeatureComment[];
  documents?: ApiFeatureLink[];
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
    emoji: f.emoji ?? '',
    state: f.state ?? 'active',
    stateManual: !!f.state_manual,
    createdAt: f.created_at,
    updatedAt: f.updated_at,
    issues: (view.issues ?? []).map(iss => ({
      key: iss.key,
      title: iss.title,
      state: iss.state,
      stateLabel: stateLabel(iss.state),
    })),
    comments: (view.comments ?? []).map(c => ({
      uuid: c.uuid,
      author: c.author,
      body: c.body,
      createdAt: c.created_at,
    })),
    documents: (view.documents ?? []).map(d => ({
      filename: d.document_filename,
      type: d.document_type,
      description: d.description ?? '',
      sourcePath: '',
    })),
    hiddenOnBoard: !!f.hidden_on_board,
  };
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

// setFeatureState (BACI-199) flips the feature's three-state column
// and returns the refreshed FeatureDetail. The handler stamps the
// sticky bit so the leader-elected archive-sweep's auto-completion
// pass leaves the row alone until the user pins a new value.
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

// listShippedIssues (BACI-187) is the HTTP twin of api.ts's
// listShippedIssues. Keep the parameter list and return type in
// lockstep with the desktop binding — the React-side ShippedPopover
// imports the same name from `./api` in both modes.
export async function listShippedIssues(
  repoPrefix: string,
  sinceDays: number,
  limit: number,
): Promise<ShippedIssueDTO[]> {
  if (!repoPrefix || repoPrefix === 'all') {
    throw new Error('select a repository to view its shipping log');
  }
  const query: Record<string, string | number> = {};
  if (sinceDays > 0) query.since = `${sinceDays}d`;
  if (limit > 0) query.limit = limit;
  const rows = await call<ShippedIssueDTO[]>(`/repos/${repoPrefix}/shipped`, { query });
  // The server returns `[]` on an empty repo (never null), but be
  // defensive in case the call helper ever surfaces null on 204.
  return rows ?? [];
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

export interface SyncSetupPayload {
  mode: 'init' | 'clone' | 'attach';
  remote?: string;
  localPath?: string;
  allowRenumber?: boolean;
}

export interface RenumberEntryDTO {
  prefix: string;
  oldNumber: number;
  newNumber: number;
  uuid: string;
}

export interface RenameEntryDTO {
  kind: string;
  old: string;
  new: string;
  uuid: string;
}

export interface CollisionPreviewDTO {
  renumbered?: RenumberEntryDTO[];
  renamed?: RenameEntryDTO[];
}

export interface SyncSetupDTO {
  mode: string;
  localPath?: string;
  remote?: string;
  commitSHA?: string;
  pushed?: boolean;
  attached?: boolean;
  previewCollisions?: CollisionPreviewDTO;
}

// Re-exports for the cross-transport modal: SyncSetupResult is the
// camelCase DTO the modal consumes; SyncSetupCollisionError is the
// typed exception thrown on the 409 path. Same names as api.ts.
export type SyncSetupResult = SyncSetupDTO;

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
interface SyncSetupApi {
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

function reshapeSyncSetup(wire: SyncSetupApi): SyncSetupDTO {
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

export interface RepoLinkResult {
  prefix: string;
  path: string;
  syncRemoteUrl: string;
  alreadyLinked: boolean;
}

// RepoLinkResultDTO is the cross-transport alias used by SyncView /
// PhantomLinkModal — matches the api.ts re-export. The HTTP wire shape
// stays snake_case in line with the rest of api.http.ts.
export type RepoLinkResultDTO = RepoLinkResult;

interface RepoLinkResultApi {
  repo: { prefix: string; path: string };
  sync_remote_url: string;
  already_linked?: boolean;
  would_link?: boolean;
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
  return {
    prefix: res.repo?.prefix ?? prefix,
    path: res.repo?.path ?? path,
    syncRemoteUrl: res.sync_remote_url ?? '',
    alreadyLinked: !!res.already_linked,
  };
}

// ---------- Sync preferences (BACI-89 / BACI-108) ----------
//
// The BACI-89 sync.background_enabled toggle, exposed for the
// standalone Sync view (BACI-108). Same shape as the board / display
// preferences pairs; lives behind /settings/sync-preferences.

export interface SyncPreferences {
  backgroundEnabled: boolean;
}

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

// ---------- Archive preferences (BACI-162) ----------
//
// archive.auto_enabled + archive.retention_days global settings. When
// auto_enabled is false the hourly issue auto-archive pass is skipped
// entirely; retention_days (1..3650) is the number of days a
// terminal-state issue's terminal_at must sit before the next sweep
// archives it.

export type ArchivePreferencesDTO = { autoEnabled: boolean; retentionDays: number };

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
