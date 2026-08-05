// Shared DTO contract for the `./api` seam (BACI-359, Phase 2b).
//
// This is the SINGLE source of truth for the camelCase DTO shapes both
// transports expose: api.ts (Wails bindings) and api.http.ts (HTTP fetch).
// Before this module each transport carried its own copy of every shape —
// api.ts re-exported the Wails-generated bindings, api.http.ts hand-wrote
// parallel interfaces — and a change to one that wasn't mirrored in the
// other was a silent web-only runtime bug (tsc only ever checks the
// component layer against api.ts, and the Vite web-swap erases types).
//
// Now both transports `import type` these shapes and annotate their
// function returns with them, so a drift surfaces as a tsc error:
//   - api.http.ts re-exports these names verbatim — they ARE its DTOs.
//   - api.ts annotates each binding-backed return with the contract type,
//     so a regenerated binding that no longer matches errors at compile.
//
// **TS-only — no runtime imports.** The web bundle can't load the Wails
// bindings (they reach for `@wailsio/runtime` at module load); keeping
// this file import-free means both transports can `import type` it. The
// shapes are reconciled to the true wire payload: Go `time.Time` fields
// serialise as strings, Go enums become string-literal unions, and the
// bindings' `T | null` nullability is preserved on the fields where the
// Wails return values carry it (so binding → contract stays assignable).

// ─── Board ───────────────────────────────────────────────────────────

export interface Board {
  prefix: string;
  name: string;
  issueCount: number;
  // BACI-89 background-sync status. syncEnabled = "this repo has git
  // sync configured"; the other three reflect the controller's
  // background sync runner last / current state. syncLastAt /
  // syncLastError are absent when the repo has never synced / the last
  // sync succeeded.
  //
  // BACI-376: syncBackgroundEnabled is the global sync.background_enabled
  // toggle, echoed on every board. A repo with syncEnabled but
  // !syncBackgroundEnabled is configured yet not being mirrored — the
  // ticker is off app-wide. syncMirroredBy is the label of the sync repo
  // already carrying this repo's data (the export is whole-DB, so this is
  // set for plenty of repos that have no sync config of their own);
  // absent when nothing mirrors it.
  syncEnabled: boolean;
  syncBackgroundEnabled: boolean;
  syncMirroredBy?: string;
  syncInProgress: boolean;
  syncLastAt?: string;
  syncLastError?: string;
}

// RepoActivity (BACI-369) is one repo's activity summary, polled by the
// topbar's repository picker to rank its rows. Deliberately separate
// from Board: Board is loaded once at mount, this is polled on the
// shared 10s cadence. lastActivityAt is absent for a repo nothing has
// happened in yet — those sort last.
export interface RepoActivity {
  prefix: string;
  lastActivityAt?: string;
  activeJobs: number;
}

export interface BoardColumn {
  state: string;
  label: string;
}

export interface BoardCardTodo {
  content: string;
  status: string;
}

// BoardCardBlocker (BACI-114) is one open `blocks` edge pointing AT
// a card. Title is intentionally NOT on the wire — the kanban
// popover joins it from the same `cards` array (Option A: thin
// renderer over data the brief already has).
export interface BoardCardBlocker {
  key: string;
  state: string; // todo | in_review | in_pipeline | to_be_shipped — open-state set
}

// BACI-145: WaitingKind / WaitingState mirror the Go-side types in
// internal/boardcards/cards.go. The empty-string member is the Go enum
// zero value (`WaitingKind.$zero`) the Wails binding carries in its type;
// it never appears at runtime alongside a non-null waitingState (the
// card is simply not waiting then), but it's kept in the union so the
// binding's `kind` field stays assignable to this contract.
export type WaitingKind = '' | 'queued_no_agent' | 'queued_blocked' | 'delivered';

export interface WaitingState {
  kind: WaitingKind;
  mode?: string;
  actionLabel?: string;
}

// LatestPlan (BACI-216) is the newest `plan`-typed doc linked
// directly to an issue. Drives the per-card plan icon on the
// kanban and the prominent "Open plan" link in the workspace
// header.
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
  // BACI-349: optional one-line customer impact denormalised onto the
  // card. Absent (server-side omitempty) when unset; the opt-in
  // impact-primary Pipeline view renders it as the card head and demotes
  // the title to a subtitle only when this is non-empty.
  customerImpact?: string;
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
  // BACI-231: per-feature integration branch denormalised onto the
  // card by the same join. Empty (and absent on the wire) when the
  // card belongs to no feature or to a feature that ships straight
  // to main — the kanban renders the branch chip only when truthy
  // and the ActivityTray groups by this field.
  featureBranchName?: string;
  // BACI-145: structured "why is this card waiting?" state. Replaces
  // the two booleans waitingForClaim + waitingDispatchDelivered that
  // the BoardCard used to carry — neither could carry the active
  // dispatch's mode for the inline label. Absent (omitempty
  // server-side) when the card isn't waiting; the PipelineCard renders
  // no spinner in that case.
  waitingState?: WaitingState | null;
  // BACI-60 enrichment: lower-cased prompt-template label of the
  // newest open claim's most recent non-cancelled dispatch (empty
  // when no verb can be derived) and the claiming session's
  // TodoWrite progress (zeroes when no todos / not taken).
  activeVerb?: string;
  // BACI-286: external session id of the winning open claim — the worker
  // running this card's job. The Pipeline running-job card's "message"
  // button targets it to push a user→agent steer message. Absent
  // (omitempty server-side) on untaken cards.
  runningSessionId?: string;
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
  // BACI-141: combined eval/transcript kanban indicator counts. The
  // server serves both on every card (camelCase Go json tags), so
  // listCards() carries them without a reshape; absent (omitempty) when
  // zero. Surfaced here so both transports agree on the wire shape.
  transcriptDocCount?: number;
  evalCommentCount?: number;
  // BACI-187: the BACI-138 terminal_at stamp — non-null on done /
  // cancelled cards, absent on open cards — the timestamp a card
  // reached a terminal column.
  terminalAt?: string;
  // BACI-216: the newest `plan`-typed doc linked directly to this
  // issue, or absent when none. Drives the per-card plan icon that
  // opens the doc viewer.
  latestPlan?: LatestPlan | null;
  // LatestPR (BACI-239) is the most-recently-attached PR on this issue,
  // or absent when none. Drives the per-card PR chip.
  latestPR?: BoardCardLatestPR | null;
  // Pipeline (Phase 4) — the card's process chain + engine drive state,
  // populated by the cards endpoint only while the card is in_pipeline.
  // `jobs` is the sequence-ordered chain; `currentJob` is the running
  // stage (or the next pending one); `engineMode` is "off" | "auto"; and
  // `enginePauseReason` is one of "" | "open_question" |
  // "agent_error_transient" | "agent_error_terminal" (BACI-296) |
  // "subagent_cancelled" (BACI-328).
  jobs?: BoardCardJob[];
  currentJob?: BoardCardJob | null;
  engineMode?: string;
  enginePauseReason?: string;
  // BACI-53: open ask_user_question rows for this card. On a pipeline
  // card the first open question is the "waiting on you" signal that
  // trips the engine halt.
  openQuestions?: BoardCardQuestion[];
}

// BoardCardLatestPR (BACI-239) is the newest PR attached to a card.
export interface BoardCardLatestPR {
  url: string;
  count: number;
  // createdAt rides on the wire (the join carries the attach time) but
  // the card chip renders only url + count today.
  createdAt?: string;
}

// BoardCardQuestion (BACI-53) is one open ask_user_question row carried
// on a BoardCard. pipelineJobId (Phase 4) is the job the question is
// parented to, when the card is in_pipeline. count / askedAt ride on the
// wire (the server serves the full projection) but the badge renders
// only the header / first question.
export interface BoardCardQuestion {
  id: number;
  header?: string;
  firstQuestion?: string;
  count?: number;
  askedAt?: string;
  pipelineJobId?: number | null;
}

// BoardCardJob (Phase 4) is one stage of a card's process chain — the
// camelCase projection the /cards endpoint serves on BoardCard.jobs.
export interface BoardCardJob {
  sequence: number;
  mode: string;
  status: string; // pending | running | complete | cancelled
}

// ─── Pipeline ────────────────────────────────────────────────────────

// PipelineJob (Phase 4) mirrors model.PipelineJob (snake_case wire
// shape). The job-control endpoints (process / start / stop / jobs)
// return the refreshed chain in this shape. dispatch_id / started_at /
// completed_at are null/absent until the stage runs.
export interface PipelineJob {
  id: number;
  issue_id: number;
  sequence: number;
  mode: string;
  status: string;
  dispatch_id?: number | null;
  created_at: string;
  started_at?: string;
  completed_at?: string;
}

// ShippedIssueDTO (BACI-187) is one row in the Pipeline Shipping-column
// shipping-log popover.
export interface ShippedIssueDTO {
  key: string;
  title: string;
  // BACI-349: optional one-line customer impact. The popover renders
  // this as the primary line and falls back to `title` (with a
  // muted/italic class) when it's absent.
  customerImpact?: string;
  terminalAt: string;
  tags: string[];
  featureSlug?: string;
  featureEmoji?: string;
  prUrl?: string;
}

// ShippedListDTO (BACI-221) wraps the popover's per-fetch rows with
// the total count under the same scope so the popover header can
// render "showing N of TOTAL" without an extra round trip.
export interface ShippedListDTO {
  rows: ShippedIssueDTO[];
  total: number;
}

// ─── Issue ───────────────────────────────────────────────────────────

export interface CommentDTO {
  uuid: string;
  author: string;
  body: string;
  createdAt: string;
  // BACI-131 eval-comment fields. eval is true when the row was
  // posted from the kanban card's quick-eval composer; the triple
  // (agentSessionId, dispatchId, mode) is the in-flight context
  // captured server-side at write time. agentName is the persistent
  // agent identity slug resolved at read time. transcriptEventRef
  // (BACI-141) is the optional per-event anchor. All are zero values
  // on a normal (non-eval) comment.
  eval?: boolean;
  agentSessionId?: string;
  dispatchId?: number | null;
  mode?: string;
  agentName?: string;
  transcriptEventRef?: string;
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
  // BACI-349: optional one-line customer impact; the detail header
  // renders it inline and lets the user edit it.
  customerImpact?: string;
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
  // issue; null when none.
  latestPlan?: LatestPlan | null;
}

// IssueMetaDTO is the issue-header slice of an IssueBriefDTO. Kept thin
// so the rail and primary column don't have to thread the full brief.
export interface IssueMetaDTO {
  key: string;
  column: string;
  columnLabel: string;
  title: string;
  // BACI-349: optional one-line customer impact; the IssueWorkspace
  // header renders it inline and lets the user edit it.
  customerImpact?: string;
  description: string;
  tags: string[];
  assignees: string[];
  claude: boolean;
  taken: boolean;
  // BACI-216: newest `plan`-typed doc linked directly to this issue.
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

// IssueBriefDTO is the workspace-shaped payload. The REST surface
// (GET /repos/.../brief) emits the snake_case IssueBrief shape;
// reshapeApiBrief() collapses it into this shape so React reads the
// same camelCase fields in both modes.
export interface IssueBriefDTO {
  issue: IssueMetaDTO;
  // Optional + nullable: the Wails binding types it `feature?: ... | null`;
  // the HTTP reshaper always sets it (to a ref or null).
  feature?: FeatureRefDTO | null;
  relations: RelationsDTO;
  pullRequests: PRDTO[];
  documents: LinkedDocDTO[];
  comments: CommentDTO[];
  claimants: ClaimantDTO[];
  taken: boolean;
  // BACI-145: structured waiting state for the IssueLockBanner.
  // Absent (server-side omitempty) when the issue isn't waiting.
  waitingState?: WaitingState | null;
  // BACI-216: duplicated alongside issue.latestPlan so envelope
  // consumers don't have to descend into the meta block.
  latestPlan?: LatestPlan | null;
  warnings: string[];
}

// ─── Agents ──────────────────────────────────────────────────────────

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
  // BACI-190: true when this dispatch's target session ended without the
  // dispatch being acked — drives the AgentsView "Rescue" button. The
  // composite AgentCard payload carries it; reshapeDispatch (the
  // single-dispatch return path) leaves it unset.
  needsRescue?: boolean;
}

export interface SessionTodoDTO {
  content: string;
  status: string;
  // BACI-62: per-job scope so a future "history" pane can group
  // prior-job todos. Empty / absent on rows from sessions registered
  // before BACI-62 or when the hook couldn't attribute a single
  // open claim.
  issueKey?: string;
  // BACI-132: per-dispatch scope so two dispatches on the same
  // (session, issue) get separate task lists. Absent on pre-BACI-132
  // rows. The JSON tag is `dispatch_id` (snake_case) to match the
  // model.SessionTodo wire shape.
  dispatch_id?: number | null;
}

// QuestionDTO is one open BACI-53 ask_user_question row — the
// minimal shape the agent card needs to render its "user input
// needed" badge. The full payload is fetched via getSessionQuestion
// when the user opens the modal.
export interface QuestionDTO {
  id: number;
  issueKey?: string;
  header: string;
  askedAt: string;
}

// SessionQuestion is the full row returned by the per-question
// endpoints. Mirrors the Go shape (snake_case is the wire format
// for these — the agent registry was built before we standardised
// camelCase, and questions match the existing dispatch/claim shapes).
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
  // BACI-296: the Anthropic API failure behind an "errored" status.
  // Empty for every other status.
  errorType?: string;
  errorMessage?: string;
  busy: boolean;
  busyIssue: string;
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

// ─── Notifications ───────────────────────────────────────────────────

// Notification (BACI-287) is the wire shape of one agent→user
// notification. Field naming follows the Go snake_case JSON tags so
// the React-side <NotificationBell> reads the same shape in both modes.
// The nullable numeric / timestamp fields carry the bindings' `T | null`
// (the Go pointers); the wire omits them when zero rather than sending
// null, but the union keeps the binding return assignable.
export interface Notification {
  id: number;
  repo_id: number;
  repo_prefix?: string;
  issue_id?: number | null;
  issue_key?: string;
  body: string;
  source_agent: string;
  session_pk?: number | null;
  created_at: string;
  read_at?: string | null;
}

// ─── Documents ───────────────────────────────────────────────────────

// DocSummaryLink (BACI-204) is one link row on a DocSummary.
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

// ─── Features ────────────────────────────────────────────────────────

export interface FeatureSummary {
  slug: string;
  title: string;
  // BACI-184: per-feature emoji glyph for the Features panel list.
  // Empty when none is set.
  emoji: string;
  // BACI-199: three-state column — `active` (default), `done` or
  // `cancelled`.
  state: string;
  // BACI-231: per-feature integration branch. Empty string when the
  // feature ships straight to main (the legacy default).
  branchName: string;
  updatedAt: string;
  // BACI-177: per-feature "Show on board" toggle state. When true,
  // every kanban card belonging to this feature is hidden from the
  // board on this machine.
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
// via `bacio doc link <file> <feature-slug>`.
export interface FeatureLinkedDoc {
  filename: string;
  type: string;
  description: string;
  sourcePath?: string;
}

// FeaturePlanEntry mirrors FeaturePlanEntry (BACI-236). blockedBy
// carries the keys of in-feature blockers the dependency-graph view
// draws as directed edges. closed is true for done / cancelled issues.
export interface FeaturePlanEntry {
  key: string;
  title: string;
  state: string;
  assignee: string;
  blockedBy: string[];
  closed: boolean;
}

// FeaturePlan is the dependency-graph payload for one feature
// (BACI-236). order is the topo-sorted issue list.
export interface FeaturePlan {
  slug: string;
  order: FeaturePlanEntry[];
}

export interface FeatureDetail {
  slug: string;
  title: string;
  description: string;
  // BACI-172: per-feature emoji rendered on every kanban card under
  // this feature. Empty when none is set.
  emoji: string;
  // BACI-231: per-feature integration branch. Empty when the feature
  // ships straight to main.
  branchName: string;
  // BACI-199: three-state column + sticky bit.
  state: string;
  stateManual: boolean;
  // BACI-333: per-feature collect-handoffs toggle. ON (the default)
  // collects worker close-out handoff comments; OFF silences a
  // standing bucket.
  collectHandoffs: boolean;
  createdAt: string;
  updatedAt: string;
  issues: FeatureLinkedIssue[];
  comments: FeatureCommentDTO[];
  // BACI-214: documents linked to this feature.
  documents: FeatureLinkedDoc[];
  // BACI-177: per-feature "Show on board" toggle state.
  hiddenOnBoard: boolean;
}

// ─── History ─────────────────────────────────────────────────────────

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

// ─── Leader ──────────────────────────────────────────────────────────

export interface LeaderStatusDTO {
  amLeader: boolean;
  holderLabel: string;
}

// ─── Settings / prompt templates ─────────────────────────────────────

export interface PromptTemplateDTO {
  slug: string;
  mode: string;
  label: string;
  body: string;
  default: string;
  isBuiltin: boolean;
  isDefault: boolean;
  concurrencyLimit: number;
  defaultConcurrencyLimit: number;
  concurrencyIsDefault: boolean;
  // BACI-67: imperative override rendered on the dispatch action
  // menus. When empty, the UI derives one from `label`.
  actionLabel: string;
  defaultActionLabel: string;
  actionLabelIsDefault: boolean;
}

// ─── Repositories ────────────────────────────────────────────────────

// AddRepositoryPayload mirrors the web-build shape so callers can pass
// the same signature in both modes. Desktop ignores it — the Wails
// AddRepository pops a native folder picker and resolves path/name
// itself.
export interface AddRepositoryPayload {
  path: string;
  name: string;
  prefix?: string;
}

// ─── Sync ────────────────────────────────────────────────────────────

// SyncRegistry / SyncRepoEntry / MemberProject / UnsyncedProject are
// the camelCase DTOs the React tree consumes (BACI-108).
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
  // linked | phantom | absent — kept as `string` (not a union) because the
  // Wails binding generator widens the Go enum to a bare string, so this is
  // the shape both transports' return values satisfy.
  status: string;
}

export interface UnsyncedProject {
  prefix: string;
  name: string;
  uuid: string;
  path: string;
}

// SyncSetupPayload (BACI-111) is the camelCase input the SyncSetupModal
// builds; the three modes map 1:1 onto the engine's bootstrap paths.
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
  // Nullable: the Wails binding types the 409-preview field `| null`.
  previewCollisions?: CollisionPreviewDTO | null;
}
// SyncSetupResult is the camelCase DTO the SyncSetupModal consumes.
export type SyncSetupResult = SyncSetupDTO;

// RepoLinkResult (BACI-112) is the phantom-repo link outcome.
export interface RepoLinkResult {
  prefix: string;
  path: string;
  syncRemoteUrl: string;
  alreadyLinked: boolean;
}
// RepoLinkResultDTO is the cross-transport alias used by SyncView /
// PhantomLinkModal.
export type RepoLinkResultDTO = RepoLinkResult;

// SyncPreferences (BACI-89 / BACI-108) is the sync.background_enabled
// toggle pair.
export interface SyncPreferences {
  backgroundEnabled: boolean;
}

// Cross-transport `*DTO` aliases — the Wails seam historically exported
// these snake-of-Go-binding names alongside the friendly ones; kept so
// the public ./api surface stays byte-identical after the contract split.
export type SyncRegistryDTO = SyncRegistry;
export type SyncRepoDTO = SyncRepoEntry;
export type MemberProjectDTO = MemberProject;
export type UnsyncedProjectDTO = UnsyncedProject;
export type SyncPreferencesDTO = SyncPreferences;

// ─── Preferences (BACI-68 / 162 / 240 / 312 / 235 / 248) ─────────────

// display.show_archived global toggle.
export type DisplayPreferencesDTO = { showArchived: boolean };

// archive.auto_enabled + archive.retention_days global settings.
export type ArchivePreferencesDTO = { autoEnabled: boolean; retentionDays: number };

// ui.shipped_sfx global toggle.
export type AudioPreferencesDTO = { shippedSfx: boolean };

// ui.timezone global setting (IANA zone name); empty when unset.
export type TimezonePreferencesDTO = { timezone: string };

// Per-repo default_feature setting. Empty slug = unset; slug + title +
// emoji are inflated when set.
export type DefaultFeatureDTO = { slug: string; title?: string; emoji?: string };

// Per-repo board-hidden-states (the kanban-column states hidden from the
// board on this machine).
export type BoardHiddenStatesDTO = { states: string[] };
