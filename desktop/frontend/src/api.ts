// Thin typed wrapper over the generated Wails bindings. Centralises the
// binding import path and normalises rejections to Error so the React
// components can stay unaware of Wails specifics.
import {
  BoardService,
  DocService,
  FeatureService,
  HistoryService,
  LeaderService,
  SettingsService,
  Board,
  BoardColumn,
  BoardCard,
  IssueDetail,
  IssueBriefDTO,
  IssueMetaDTO,
  LinkedDocDTO,
  FeatureRefDTO,
  RelationDTO,
  RelationsDTO,
  PRDTO,
  CommentDTO,
  AgentCard,
  DispatchDTO,
  DocSummary,
  DocContent,
  DocLinkDTO,
  FeatureSummary,
  FeatureDetail,
  FeatureLinkedIssue,
  FeatureLinkedDoc,
  FeaturePlan,
  FeaturePlanEntry,
  FeatureComment as FeatureCommentDTO,
  HistoryPage,
  HistoryEntryDTO,
  LeaderStatusDTO,
  PromptTemplateDTO,
  ArchivePreferencesDTO,
  AudioPreferencesDTO,
  SyncPreferencesDTO,
  SyncRegistryDTO,
  SyncRepoDTO,
  MemberProjectDTO,
  UnsyncedProjectDTO,
  SyncSetupDTO,
  SetupSyncIn as SetupSyncInDTO,
  CollisionPreviewDTO,
  RenumberEntryDTO,
  RenameEntryDTO,
  RepoLinkResultDTO,
  ShippedIssueDTO,
  ShippedListDTO,
  LatestPlanDTO,
} from '../bindings/github.com/mrgeoffrich/bacio/desktop';
import { ClaimDTO } from '../bindings/github.com/mrgeoffrich/bacio/internal/agentcards';
// BACI-145: re-export the WaitingState / WaitingKind enums from the
// boardcards binding so the React components import them from the
// same api.ts seam as everything else (avoids one-off binding paths
// scattered through the kanban code).
import { WaitingState, WaitingKind } from '../bindings/github.com/mrgeoffrich/bacio/internal/boardcards';
// Phase 4 (Pipeline): PipelineJob is the per-stage process-chain row the
// controller engine advances. Lives in the model bindings; re-exported
// below so the Pipeline page imports it from the same ./api seam as
// everything else (parallels the WaitingState import rationale).
import { PipelineJob, Notification } from '../bindings/github.com/mrgeoffrich/bacio/internal/model';
// ProcessSelection (BACI-283) is the picker's discriminated handback —
// an explicit stage list or a preset slug. Re-exported below so callers
// pull it from the ./api seam alongside PipelineJob.
import type { ProcessSelection } from './lib/pipelineProcesses';

export type { Board, BoardColumn, BoardCard, IssueDetail, IssueBriefDTO, IssueMetaDTO, LinkedDocDTO, FeatureRefDTO, RelationDTO, RelationsDTO, PRDTO, CommentDTO, AgentCard, ClaimDTO, DispatchDTO, DocSummary, DocContent, DocLinkDTO, FeatureSummary, FeatureDetail, FeatureLinkedIssue, FeatureLinkedDoc, FeaturePlan, FeaturePlanEntry, FeatureCommentDTO, HistoryPage, HistoryEntryDTO, LeaderStatusDTO, PromptTemplateDTO, ArchivePreferencesDTO, AudioPreferencesDTO, WaitingState, SyncPreferencesDTO, SyncRegistryDTO, SyncRepoDTO, MemberProjectDTO, UnsyncedProjectDTO, SyncSetupDTO, CollisionPreviewDTO, RenumberEntryDTO, RenameEntryDTO, RepoLinkResultDTO, ShippedIssueDTO, ShippedListDTO, LatestPlanDTO, Notification };
// BACI-216: cross-transport alias. The web bundle's api.http.ts ships
// the same name from its own TS-only shape so KanbanCard / IssueWorkspace
// stay transport-agnostic.
export type LatestPlan = LatestPlanDTO;
// Phase 4 (Pipeline): re-export PipelineJob so the Pipeline page and its
// helpers import it from ./api like every other shape.
export type { PipelineJob };
export type { ProcessSelection };

// BACI-108: cross-transport aliases — components import from `./api`
// and stay unaware of whether they're on the Wails or HTTP seam. The
// web bundle's api.http.ts ships the same names with parallel TS-only
// shapes (the bundle can't load the Wails-generated bindings — see the
// note at the top of api.http.ts).
export type SyncPreferences = SyncPreferencesDTO;
export type SyncRegistry = SyncRegistryDTO;
export type SyncRepoEntry = SyncRepoDTO;
export type MemberProject = MemberProjectDTO;
export type UnsyncedProject = UnsyncedProjectDTO;
// BACI-111: cross-transport sync-setup wire shape. SyncSetupPayload is
// the camelCase input the SyncSetupModal builds; SyncSetupResult is the
// returned outcome the modal consumes on success. SyncSetupCollisionError
// is the typed exception thrown when the server (Wails or HTTP) refuses
// the call because the import would renumber / rename local rows — the
// modal `instanceof`-checks it to advance to the step-2 confirm.
export type SyncSetupPayload = SetupSyncInDTO;
export type SyncSetupResult = SyncSetupDTO;
// BACI-112: cross-transport alias for the phantom-repo link result.
// The web bundle's api.http.ts re-exports the same name from its own
// TS-only shape so SyncView / PhantomLinkModal stays unaware of the
// underlying transport.
export type RepoLinkResult = RepoLinkResultDTO;
export { WaitingKind };

// SyncSetupCollisionError carries the typed CollisionPreviewDTO so the
// modal can render the renumber / rename preview verbatim. Throws are
// the natural shape for the wire-level seam — a 409-equivalent
// turns into one of these in both transports — but the underlying
// SyncSetupResult is preserved on the .result property when the caller
// wants to see the full structured shape (e.g. for tests).
export class SyncSetupCollisionError extends Error {
  previewCollisions: CollisionPreviewDTO;
  result: SyncSetupDTO;
  constructor(result: SyncSetupDTO) {
    super('sync setup: renumber collision');
    this.name = 'SyncSetupCollisionError';
    this.result = result;
    // Non-null assert: caller only constructs this when the server
    // populated previewCollisions; the Go side guarantees it.
    this.previewCollisions = result.previewCollisions!;
  }
}

function normalize(err: unknown): Error {
  if (err instanceof Error) return err;
  if (typeof err === 'string') return new Error(err);
  return new Error(String((err as { message?: unknown })?.message ?? err));
}

export async function listBoards(): Promise<Board[]> {
  try {
    return await BoardService.ListBoards();
  } catch (err) {
    throw normalize(err);
  }
}

export async function listColumns(): Promise<BoardColumn[]> {
  try {
    return await BoardService.ListColumns();
  } catch (err) {
    throw normalize(err);
  }
}

export async function listCards(repoPrefix: string): Promise<BoardCard[]> {
  try {
    return await BoardService.ListCards(repoPrefix);
  } catch (err) {
    throw normalize(err);
  }
}

// AddRepositoryPayload mirrors the web-build shape so callers can pass
// the same signature in both modes. Desktop ignores it — the Wails
// AddRepository pops a native folder picker and resolves path/name
// itself.
export interface AddRepositoryPayload {
  path: string;
  name: string;
  prefix?: string;
}

// addRepository opens a native folder picker and registers the chosen git
// working tree. The returned Board has an empty prefix if the user
// cancelled. The optional payload is web-only — desktop ignores it.
export async function addRepository(_payload?: AddRepositoryPayload): Promise<Board> {
  try {
    return await BoardService.AddRepository();
  } catch (err) {
    throw normalize(err);
  }
}

export async function getIssue(repoPrefix: string, key: string): Promise<IssueDetail> {
  try {
    return await BoardService.GetIssue(repoPrefix, key);
  } catch (err) {
    throw normalize(err);
  }
}

// getIssueBrief returns the workspace-shaped payload — issue meta +
// feature + relations + inlined linked-doc bodies + comments +
// claimants + the derived `taken` flag + the structured WaitingState
// (BACI-145). Backs the top-level IssueWorkspace view.
export async function getIssueBrief(repoPrefix: string, key: string): Promise<IssueBriefDTO> {
  try {
    return await BoardService.GetIssueBrief(repoPrefix, key);
  } catch (err) {
    throw normalize(err);
  }
}

// attachPullRequest attaches a pull-request URL to an issue and returns
// the resolved PRDTO. Used by the IssueWorkspace's PR-attach form;
// validation errors (bad scheme / missing host) surface verbatim.
export async function attachPullRequest(
  repoPrefix: string,
  key: string,
  url: string,
): Promise<PRDTO> {
  try {
    return await BoardService.AttachPullRequest(repoPrefix, key, url);
  } catch (err) {
    throw normalize(err);
  }
}

export async function listAgents(repoPrefix: string): Promise<AgentCard[]> {
  try {
    return await BoardService.ListAgents(repoPrefix);
  } catch (err) {
    throw normalize(err);
  }
}

// BACI-53 ask_user_question Wails bindings. Backs the desktop
// Agents-view modal. Web mode (api.http.ts) exposes the same three
// functions through the REST routes so the React components don't
// branch on backend.

export async function getSessionQuestion(id: number) {
  try {
    return await BoardService.GetSessionQuestion(id);
  } catch (err) {
    throw normalize(err);
  }
}

export async function answerSessionQuestion(id: number, answers: Record<string, unknown>) {
  try {
    return await BoardService.AnswerSessionQuestion(id, answers);
  } catch (err) {
    throw normalize(err);
  }
}

export async function cancelSessionQuestion(id: number) {
  try {
    return await BoardService.CancelSessionQuestion(id);
  } catch (err) {
    throw normalize(err);
  }
}

// BACI-287 notification bell. The list/count/mark wrappers back the global
// (cross-repo) <NotificationBell> in the topbar — agent→user notifications
// sent via the send_user_notification channel tool. listNotifications
// defaults to unread (the bell's default view); pass unreadOnly=false for
// "show all". markAllNotificationsRead returns the count flipped.
export async function listNotifications(unreadOnly = true, limit = 0): Promise<Notification[]> {
  try {
    // The Wails binding types the slice as (Notification | null)[] because
    // the Go element is a pointer; the store never returns nil rows, so
    // filter defensively to land the non-null shape the bell consumes.
    const rows = await BoardService.ListNotifications(unreadOnly, limit);
    return (rows ?? []).filter((n): n is Notification => n != null);
  } catch (err) {
    throw normalize(err);
  }
}

export async function countUnreadNotifications(): Promise<number> {
  try {
    return await BoardService.CountUnreadNotifications();
  } catch (err) {
    throw normalize(err);
  }
}

export async function markNotificationRead(id: number): Promise<Notification | null> {
  try {
    return await BoardService.MarkNotificationRead(id);
  } catch (err) {
    throw normalize(err);
  }
}

export async function markAllNotificationsRead(): Promise<number> {
  try {
    return await BoardService.MarkAllNotificationsRead();
  } catch (err) {
    throw normalize(err);
  }
}

export async function listDocs(repoPrefix: string, typeFilter = ''): Promise<DocSummary[]> {
  try {
    return await DocService.ListDocs(repoPrefix, typeFilter);
  } catch (err) {
    throw normalize(err);
  }
}

// addIssue (BACI-166) creates a new issue and returns the freshly-shaped
// BoardCard. Backs the "+ from prompt" composer in the Topbar: the
// composer calls addIssue then chains dispatchIssue(_, _, 'scope') to
// queue the scope worker on the new issue. Mirrors dispatchIssue's
// shape line-for-line; validation (empty title, control chars, etc.)
// lives at the store boundary inside BoardService.AddIssue.
export async function addIssue(
  repoPrefix: string,
  title: string,
  description: string,
  featureSlug = '',
): Promise<BoardCard> {
  try {
    return await BoardService.AddIssue(repoPrefix, title, description, featureSlug);
  } catch (err) {
    throw normalize(err);
  }
}

// dispatchIssue queues a dispatch against an issue for a job stage. The
// agent is auto-picked by the backend (the most-recently-active free
// agent) — the caller never names one. Post-BACI-51 this is the enqueue
// path: the dispatch is always queued; the matcher binds an agent later.
export async function dispatchIssue(
  repoPrefix: string,
  issueKey: string,
  mode: string,
): Promise<DispatchDTO> {
  try {
    return await BoardService.DispatchIssue(repoPrefix, issueKey, mode);
  } catch (err) {
    throw normalize(err);
  }
}

// cancelWaitingDispatch (BACI-51) withdraws an issue's queued / pending /
// delivered dispatch — the spinner-as-cancel-button click handler. The
// backend resolves the dispatch id from the issue + cancels in one call
// so we don't round-trip the id through card DTOs.
export async function cancelWaitingDispatch(
  repoPrefix: string,
  issueKey: string,
): Promise<void> {
  try {
    await BoardService.CancelWaitingDispatch(repoPrefix, issueKey);
  } catch (err) {
    throw normalize(err);
  }
}

// rescueDispatch (BACI-190) posts a `from="bacio-rescue"` channel event
// to an idle supervisor session asking it to handle a dead worker's
// stranded worktree INLINE. The AgentsView dispatch row renders a
// "Rescue" button on dispatches whose target session has ended without
// the dispatch being acked (the NeedsRescue flag on DispatchDTO).
// Eligibility re-checks live on the backend so a stale UI click can't
// queue an invalid rescue; errors surface as Error.message and bubble
// through reportError() in App.jsx.
export async function rescueDispatch(dispatchID: number): Promise<DispatchDTO> {
  try {
    return await BoardService.RescueDispatch(dispatchID);
  } catch (err) {
    throw normalize(err);
  }
}

// sendSessionMessage (BACI-286) pushes a user→agent steer message at a
// busy session. The channel serving that session injects it as a
// `<channel kind="message">` tag at the worker's next turn boundary —
// NOT a dispatch. Backs the "message" button on the Pipeline running-job
// card (targets card.runningSessionId) and the Agents-page session card
// (targets a.sessionId). Errors (unknown session, empty/over-cap body)
// surface as Error.message for a toast.
export async function sendSessionMessage(sessionID: string, body: string) {
  try {
    return await BoardService.SendSessionMessage(sessionID, body);
  } catch (err) {
    throw normalize(err);
  }
}

// setIssueState changes an issue's state — backs the board's drag-to-move,
// persisting the column change so it survives the next refresh poll.
export async function setIssueState(
  repoPrefix: string,
  key: string,
  state: string,
): Promise<BoardCard> {
  try {
    return await BoardService.SetIssueState(repoPrefix, key, state);
  } catch (err) {
    throw normalize(err);
  }
}

// ─── Pipeline (Phase 4) ──────────────────────────────────────────────
// The Pipeline page's card-ordering, process-assignment, and engine
// controls. Mirrored one-for-one in api.http.ts so PipelineView stays
// transport-agnostic. The job-control verbs return the refreshed chain;
// the column-changing verbs (reorder / engine-mode / ship) return the
// updated BoardCard so the optimistic UI can reconcile.

// reorderCard moves a card to a 1-based position within its (repo, state)
// ordering band — the Backlog / Shipping drag-to-reorder.
export async function reorderCard(
  repoPrefix: string,
  key: string,
  position: number,
): Promise<BoardCard> {
  try {
    return await BoardService.ReorderCard(repoPrefix, key, position);
  } catch (err) {
    throw normalize(err);
  }
}

// setCardProcess assigns a process (see lib/pipelineProcesses),
// materialising the card's pending job chain. The selection is either an
// explicit ordered stage list (the cumulative-stepper picker) or a preset
// slug (the kept skip-Plan buttons) — mutually exclusive. Returns the new
// chain.
export async function setCardProcess(
  repoPrefix: string,
  key: string,
  selection: ProcessSelection,
): Promise<PipelineJob[]> {
  try {
    const process = 'process' in selection ? selection.process : '';
    const stages = 'stages' in selection ? selection.stages : [];
    // Wails maps Go []*model.PipelineJob to (PipelineJob | null)[]; the
    // store never returns nil elements, so drop them to match the
    // non-null contract the HTTP twin already satisfies.
    const jobs = await BoardService.SetCardProcess(repoPrefix, key, process, stages);
    return (jobs ?? []).filter((j): j is PipelineJob => j != null);
  } catch (err) {
    throw normalize(err);
  }
}

// startCardJob is the manual Start control — advance one step (start the
// next pending job, or run the Ship hand-off). Returns the refreshed chain.
export async function startCardJob(
  repoPrefix: string,
  key: string,
): Promise<PipelineJob[]> {
  try {
    const jobs = await BoardService.StartCardJob(repoPrefix, key);
    return (jobs ?? []).filter((j): j is PipelineJob => j != null);
  } catch (err) {
    throw normalize(err);
  }
}

// stopCardJob is the manual Stop control — cancel the running job and
// halt Auto. Returns the refreshed chain.
export async function stopCardJob(
  repoPrefix: string,
  key: string,
): Promise<PipelineJob[]> {
  try {
    const jobs = await BoardService.StopCardJob(repoPrefix, key);
    return (jobs ?? []).filter((j): j is PipelineJob => j != null);
  } catch (err) {
    throw normalize(err);
  }
}

// setEngineMode sets the controller engine's per-card drive mode
// ("off" | "auto") while the card is in_pipeline. Returns the updated card.
export async function setEngineMode(
  repoPrefix: string,
  key: string,
  mode: string,
): Promise<BoardCard> {
  try {
    return await BoardService.SetCardEngineMode(repoPrefix, key, mode);
  } catch (err) {
    throw normalize(err);
  }
}

// shipCard is the in_pipeline → to_be_shipped hand-off (no agent — the
// ship agent fires from the Shipping column). Returns the updated card.
export async function shipCard(
  repoPrefix: string,
  key: string,
): Promise<BoardCard> {
  try {
    return await BoardService.ShipCard(repoPrefix, key);
  } catch (err) {
    throw normalize(err);
  }
}

// setAutoShip toggles the per-repo Shipping-column auto-ship setting.
// Returns the persisted value.
export async function setAutoShip(
  repoPrefix: string,
  enabled: boolean,
): Promise<boolean> {
  try {
    return await BoardService.SetAutoShip(repoPrefix, enabled);
  } catch (err) {
    throw normalize(err);
  }
}

// getAutoShip reads the per-repo Shipping-column auto-ship setting — the
// DB value the controller's auto-ship ticker acts on, so the Pipeline's
// Shipping switch seeds from the source of truth.
export async function getAutoShip(repoPrefix: string): Promise<boolean> {
  try {
    return await BoardService.GetAutoShip(repoPrefix);
  } catch (err) {
    throw normalize(err);
  }
}

// setBacklogCollapsed (BACI-288) persists the per-repo Pipeline
// Backlog-column collapse preference. Returns the persisted value.
export async function setBacklogCollapsed(
  repoPrefix: string,
  collapsed: boolean,
): Promise<boolean> {
  try {
    return await BoardService.SetBacklogCollapsed(repoPrefix, collapsed);
  } catch (err) {
    throw normalize(err);
  }
}

// getBacklogCollapsed (BACI-288) reads the per-repo Pipeline
// Backlog-column collapse preference so the rail state seeds from the
// persisted KV.
export async function getBacklogCollapsed(repoPrefix: string): Promise<boolean> {
  try {
    return await BoardService.GetBacklogCollapsed(repoPrefix);
  } catch (err) {
    throw normalize(err);
  }
}

// updateIssueDescription replaces an issue's description and returns the
// refreshed issue-drawer payload.
export async function updateIssueDescription(
  repoPrefix: string,
  key: string,
  description: string,
): Promise<IssueDetail> {
  try {
    return await BoardService.UpdateIssueDescription(repoPrefix, key, description);
  } catch (err) {
    throw normalize(err);
  }
}

// updateIssueTitle replaces an issue's title and returns the refreshed
// issue-drawer payload.
export async function updateIssueTitle(
  repoPrefix: string,
  key: string,
  title: string,
): Promise<IssueDetail> {
  try {
    return await BoardService.UpdateIssueTitle(repoPrefix, key, title);
  } catch (err) {
    throw normalize(err);
  }
}

// addComment appends a comment to an issue and returns the refreshed
// issue-drawer payload. An empty author falls back to the OS username.
// opts.eval (BACI-131) flags the row as a quality-review note posted
// from the kanban quick-eval composer — the server pins the in-flight
// (agent_session_id, dispatch_id, mode) snapshot onto the comment.
// opts.transcriptEventRef (BACI-141) is the optional per-event anchor
// the transcript viewer's per-event composer sets — empty leaves the
// note rendered pinned to the dispatch prompt card.
export async function addComment(
  repoPrefix: string,
  key: string,
  author: string,
  body: string,
  opts?: { eval?: boolean; transcriptEventRef?: string },
): Promise<IssueDetail> {
  try {
    return await BoardService.AddComment(
      repoPrefix,
      key,
      author,
      body,
      !!opts?.eval,
      opts?.transcriptEventRef ?? '',
    );
    // ^ Wails binding parameter is `isEval` (renamed from `eval` so the
    //   generated TS doesn't clash with the JavaScript reserved word).
  } catch (err) {
    throw normalize(err);
  }
}

// deleteComment removes a comment from an issue and returns the
// refreshed issue-drawer payload. The comment is addressed by its uuid.
export async function deleteComment(
  repoPrefix: string,
  key: string,
  commentUUID: string,
): Promise<IssueDetail> {
  try {
    return await BoardService.DeleteComment(repoPrefix, key, commentUUID);
  } catch (err) {
    throw normalize(err);
  }
}

export async function listFeatures(repoPrefix: string): Promise<FeatureSummary[]> {
  try {
    return await FeatureService.ListFeatures(repoPrefix);
  } catch (err) {
    throw normalize(err);
  }
}

export async function getFeature(repoPrefix: string, slug: string): Promise<FeatureDetail> {
  try {
    return await FeatureService.GetFeature(repoPrefix, slug);
  } catch (err) {
    throw normalize(err);
  }
}

// getFeaturePlan (BACI-236) returns the topo-sorted dependency-graph
// payload for a feature. includeClosed=false matches the historical
// open-only `bacio feature plan` shape (used today by the CLI text
// render); true widens to every issue in the feature plus every
// `blocks` edge whose endpoints are both in the feature, with
// closed=true on terminal entries so the graph view can mute them.
export async function getFeaturePlan(
  repoPrefix: string,
  slug: string,
  includeClosed: boolean,
): Promise<FeaturePlan> {
  try {
    return await FeatureService.GetFeaturePlan(repoPrefix, slug, includeClosed);
  } catch (err) {
    throw normalize(err);
  }
}

// setFeatureEmoji (BACI-172) updates the per-feature emoji glyph
// rendered on every kanban card under the feature. Empty clears.
export async function setFeatureEmoji(
  repoPrefix: string,
  slug: string,
  emoji: string,
): Promise<FeatureDetail> {
  try {
    return await FeatureService.SetFeatureEmoji(repoPrefix, slug, emoji);
  } catch (err) {
    throw normalize(err);
  }
}

// setFeatureBranchName (BACI-231) updates the per-feature integration
// branch. Empty clears the branch (the feature ships straight to
// main again). Validated by the store-side ValidateBranchName so
// malformed input surfaces as an error string from the Wails binding.
export async function setFeatureBranchName(
  repoPrefix: string,
  slug: string,
  branchName: string,
): Promise<FeatureDetail> {
  try {
    return await FeatureService.SetFeatureBranchName(repoPrefix, slug, branchName);
  } catch (err) {
    throw normalize(err);
  }
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
  try {
    return await FeatureService.SetHiddenOnBoard(repoPrefix, slug, hidden);
  } catch (err) {
    throw normalize(err);
  }
}

// setFeatureState (BACI-199) flips the feature's three-state column
// and returns the refreshed FeatureDetail. BACI-250 decoupled this from
// the auto-close pin — call setFeatureAutoClose to flip
// `state_manual` independently.
export async function setFeatureState(
  repoPrefix: string,
  slug: string,
  state: string,
): Promise<FeatureDetail> {
  try {
    return await FeatureService.SetFeatureState(repoPrefix, slug, state);
  } catch (err) {
    throw normalize(err);
  }
}

// setFeatureAutoClose (BACI-250) flips the per-feature auto-close
// toggle — the sticky-bit `state_manual` column that gates the
// BACI-199 archive-sweep's auto-completion pass — and returns the
// refreshed FeatureDetail. enabled=true clears the bit (the sweep may
// promote this feature once every child is terminal); enabled=false
// sets the bit (long-lived catch-alls stay `active` indefinitely).
export async function setFeatureAutoClose(
  repoPrefix: string,
  slug: string,
  enabled: boolean,
): Promise<FeatureDetail> {
  try {
    return await FeatureService.SetFeatureAutoClose(repoPrefix, slug, enabled);
  } catch (err) {
    throw normalize(err);
  }
}

// addFeatureComment posts a chronological handoff comment to a feature
// (BACI-124) and returns the refreshed feature detail.
export async function addFeatureComment(
  repoPrefix: string,
  slug: string,
  author: string,
  body: string,
): Promise<FeatureDetail> {
  try {
    return await FeatureService.AddFeatureComment(repoPrefix, slug, author, body);
  } catch (err) {
    throw normalize(err);
  }
}

// deleteFeatureComment removes a feature comment by uuid (BACI-124) and
// returns the refreshed feature detail.
export async function deleteFeatureComment(
  repoPrefix: string,
  slug: string,
  commentUUID: string,
): Promise<FeatureDetail> {
  try {
    return await FeatureService.DeleteFeatureComment(repoPrefix, slug, commentUUID);
  } catch (err) {
    throw normalize(err);
  }
}

export async function listHistory(
  repoPrefix: string,
  page: number,
  pageSize: number,
): Promise<HistoryPage> {
  try {
    return await HistoryService.ListHistory(repoPrefix, page, pageSize);
  } catch (err) {
    throw normalize(err);
  }
}

// listShippedIssues (BACI-187, reshaped for BACI-221) returns the
// topbar shipping-log popover rows for one repo (newest-first) wrapped
// with the total count under the same scope so the popover header can
// render "showing N of TOTAL". `sinceDays` clamps the window
// (0 = "Forever" — no lower bound on terminal_at); `limit` caps the
// row count (0 = the server's default 20, max 100). The HTTP twin in
// api.http.ts MUST keep the same name + shape so callers stay
// transport-agnostic.
export async function listShippedIssues(
  repoPrefix: string,
  sinceDays: number,
  limit: number,
): Promise<ShippedListDTO> {
  try {
    return await BoardService.ListShipped(repoPrefix, sinceDays, limit);
  } catch (err) {
    throw normalize(err);
  }
}

// countShippedIssues (BACI-221) is the lean count-only sibling polled
// on the topbar pill's 10s cadence so the "Shipped · N" label reflects
// the active Today / Last Week / Forever scope even when the popover
// is closed. `sinceDays` mirrors listShippedIssues — 0 means "Forever".
export async function countShippedIssues(
  repoPrefix: string,
  sinceDays: number,
): Promise<number> {
  try {
    return await BoardService.CountShipped(repoPrefix, sinceDays);
  } catch (err) {
    throw normalize(err);
  }
}

export async function getDoc(repoPrefix: string, filename: string): Promise<DocContent> {
  try {
    return await DocService.GetDoc(repoPrefix, filename);
  } catch (err) {
    throw normalize(err);
  }
}

export async function saveDoc(
  repoPrefix: string,
  filename: string,
  content: string,
): Promise<DocContent> {
  try {
    return await DocService.SaveDoc(repoPrefix, filename, content);
  } catch (err) {
    throw normalize(err);
  }
}

// archiveDocument / unarchiveDocument (BACI-204) are the Wails parity
// of the HTTP /documents/{filename}/{archive,unarchive} routes — added
// here so the redesigned Documents page (DocsViewer header strip)
// stays transport-agnostic.
export async function archiveDocument(
  repoPrefix: string,
  filename: string,
): Promise<void> {
  try {
    await DocService.ArchiveDoc(repoPrefix, filename);
  } catch (err) {
    throw normalize(err);
  }
}

export async function unarchiveDocument(
  repoPrefix: string,
  filename: string,
): Promise<void> {
  try {
    await DocService.UnarchiveDoc(repoPrefix, filename);
  } catch (err) {
    throw normalize(err);
  }
}

// listPromptTemplates returns every registered dispatch prompt template
// (built-ins + user-created), in store iteration order.
export async function listPromptTemplates(): Promise<PromptTemplateDTO[]> {
  try {
    return await SettingsService.ListPromptTemplates();
  } catch (err) {
    throw normalize(err);
  }
}

// addPromptTemplate creates a new dispatch prompt template. The trailing
// actionLabel (BACI-67) is the imperative override rendered on the
// dispatch action menus; pass "" to skip it and have the UI derive
// from the display name via the gerund→imperative rule.
export async function addPromptTemplate(
  slug: string,
  name: string,
  body: string,
  actionLabel: string = '',
): Promise<PromptTemplateDTO> {
  try {
    return await SettingsService.AddPromptTemplate(slug, name, body, actionLabel);
  } catch (err) {
    throw normalize(err);
  }
}

// renamePromptTemplate renames an existing template — slug, name, or
// both. Pass an empty string for newSlug to keep the slug; pass an
// empty string for newName to keep the display name.
export async function renamePromptTemplate(
  slug: string,
  newSlug: string,
  newName: string,
): Promise<PromptTemplateDTO> {
  try {
    return await SettingsService.RenamePromptTemplate(slug, newSlug, newName);
  } catch (err) {
    throw normalize(err);
  }
}

// deletePromptTemplate removes a template by slug. Historical dispatch
// rows that reference the slug are left intact (a dispatch is a
// snapshot, not a foreign key).
export async function deletePromptTemplate(slug: string): Promise<PromptTemplateDTO> {
  try {
    return await SettingsService.DeletePromptTemplate(slug);
  } catch (err) {
    throw normalize(err);
  }
}

// restoreBuiltinPromptTemplates re-seeds any built-in slug that's been
// deleted, then returns the refreshed full template list.
export async function restoreBuiltinPromptTemplates(): Promise<PromptTemplateDTO[]> {
  try {
    return await SettingsService.RestoreBuiltinPromptTemplates();
  } catch (err) {
    throw normalize(err);
  }
}

// promptPlaceholders returns the placeholder token names a template body
// can interpolate (without the surrounding {{ }}).
export async function promptPlaceholders(): Promise<string[]> {
  try {
    return await SettingsService.PromptPlaceholders();
  } catch (err) {
    throw normalize(err);
  }
}

// bacioVersion returns the version string of the bacio binary this
// desktop client is running, so the Settings panel can surface it for
// cross-checking against per-session "Bacio version" on the Agents panel.
export async function bacioVersion(): Promise<string> {
  try {
    return await SettingsService.BacioVersion();
  } catch (err) {
    throw normalize(err);
  }
}

// savePromptTemplate stores a custom body for one dispatch stage. Passing
// an empty body resets that stage to its built-in default.
export async function savePromptTemplate(
  mode: string,
  body: string,
): Promise<PromptTemplateDTO> {
  try {
    return await SettingsService.SavePromptTemplate(mode, body);
  } catch (err) {
    throw normalize(err);
  }
}

// savePromptConcurrency (BACI-51) updates a template's per-(repo, slug)
// in-flight dispatch cap the matcher enforces. 0 = unlimited; positive
// integers cap. Returns the refreshed template DTO.
export async function savePromptConcurrency(
  mode: string,
  concurrencyLimit: number,
): Promise<PromptTemplateDTO> {
  try {
    return await SettingsService.SavePromptConcurrency(mode, concurrencyLimit);
  } catch (err) {
    throw normalize(err);
  }
}

// savePromptActionLabel (BACI-67) sets or clears a template's
// imperative override — the verb the dispatch action menus render.
// An empty actionLabel clears the override; the UI then derives one
// from the gerund display name. Returns the refreshed template DTO.
export async function savePromptActionLabel(
  mode: string,
  actionLabel: string,
): Promise<PromptTemplateDTO> {
  try {
    return await SettingsService.SavePromptActionLabel(mode, actionLabel);
  } catch (err) {
    throw normalize(err);
  }
}

// BACI-68: display.show_archived global toggle. Lives behind a
// dedicated Wails endpoint so the Settings panel can read / write
// it without coupling display state to other preferences.
export type DisplayPreferencesDTO = { showArchived: boolean };

export async function getDisplayPreferences(): Promise<DisplayPreferencesDTO> {
  try {
    return await SettingsService.GetDisplayPreferences();
  } catch (err) {
    throw normalize(err);
  }
}

export async function setDisplayPreferences(
  showArchived: boolean,
): Promise<DisplayPreferencesDTO> {
  try {
    return await SettingsService.SetDisplayPreferences(showArchived);
  } catch (err) {
    throw normalize(err);
  }
}

// BACI-162: auto-archive settings — archive.auto_enabled +
// archive.retention_days. Both fields are written atomically through
// setArchivePreferences; the read path returns the pair. Same shape
// pattern as the display / sync preference pairs.

export async function getArchivePreferences(): Promise<ArchivePreferencesDTO> {
  try {
    return await SettingsService.GetArchivePreferences();
  } catch (err) {
    throw normalize(err);
  }
}

export async function setArchivePreferences(
  autoEnabled: boolean,
  retentionDays: number,
): Promise<ArchivePreferencesDTO> {
  try {
    return await SettingsService.SetArchivePreferences(autoEnabled, retentionDays);
  } catch (err) {
    throw normalize(err);
  }
}

// BACI-240: ui.shipped_sfx global toggle. When on, the topbar
// Shipped pill plays a short ka-ching SFX on every genuine ship.
// BACI-295 flipped the default ON.

export async function getAudioPreferences(): Promise<AudioPreferencesDTO> {
  try {
    return await SettingsService.GetAudioPreferences();
  } catch (err) {
    throw normalize(err);
  }
}

export async function setAudioPreferences(
  shippedSfx: boolean,
): Promise<AudioPreferencesDTO> {
  try {
    return await SettingsService.SetAudioPreferences(shippedSfx);
  } catch (err) {
    throw normalize(err);
  }
}

// BACI-235: per-repo default_feature setting. Empty slug = unset.
// Slug + title + emoji are inflated when set so the dropdown can
// render the same glyph + label as the rest of the feature affordances.
export type DefaultFeatureDTO = { slug: string; title?: string; emoji?: string };

export async function getDefaultFeature(
  repoPrefix: string,
): Promise<DefaultFeatureDTO> {
  try {
    return await BoardService.GetDefaultFeature(repoPrefix);
  } catch (err) {
    throw normalize(err);
  }
}

export async function setDefaultFeature(
  repoPrefix: string,
  slug: string,
): Promise<DefaultFeatureDTO> {
  try {
    return await BoardService.SetDefaultFeature(repoPrefix, slug);
  } catch (err) {
    throw normalize(err);
  }
}

export async function clearDefaultFeature(
  repoPrefix: string,
): Promise<DefaultFeatureDTO> {
  try {
    return await BoardService.ClearDefaultFeature(repoPrefix);
  } catch (err) {
    throw normalize(err);
  }
}

// BACI-248: per-repo board-hidden-states (the set of kanban-column
// states hidden from the board on this machine). Lives in
// tui_settings[board.hidden_states] — surfaced now on the desktop /
// web Per-repository Settings pane, previously TUI-only.
export type BoardHiddenStatesDTO = { states: string[] };

export async function getBoardHiddenStates(
  repoPrefix: string,
): Promise<BoardHiddenStatesDTO> {
  try {
    return await BoardService.GetBoardHiddenStates(repoPrefix);
  } catch (err) {
    throw normalize(err);
  }
}

// setBoardHiddenStates replaces the persisted set (replace-not-merge).
// Pass the full new array — unknown state names are silently dropped
// at the store boundary so a future state rename doesn't break old
// saved settings.
export async function setBoardHiddenStates(
  repoPrefix: string,
  states: string[],
): Promise<BoardHiddenStatesDTO> {
  try {
    return await BoardService.SetBoardHiddenStates(repoPrefix, states);
  } catch (err) {
    throw normalize(err);
  }
}

// BACI-89 / BACI-108: sync.background_enabled toggle. Same shape as
// the board-preferences pair; lives behind a dedicated Wails endpoint
// so the standalone Sync view can read / write it without coupling to
// the board / display preference plumbing.

export async function getSyncPreferences(): Promise<SyncPreferencesDTO> {
  try {
    return await SettingsService.GetSyncPreferences();
  } catch (err) {
    throw normalize(err);
  }
}

export async function setSyncPreferences(
  backgroundEnabled: boolean,
): Promise<SyncPreferencesDTO> {
  try {
    return await SettingsService.SetSyncPreferences(backgroundEnabled);
  } catch (err) {
    throw normalize(err);
  }
}

// getSyncRegistry returns the BACI-108 sync-repo registry payload —
// one entry per `sync_remotes` row with the projects each carries,
// plus the residual tracked project repos that don't yet have a
// sync.remote in their .bacio/config.yaml. Backs the standalone Sync
// view.
export async function getSyncRegistry(): Promise<SyncRegistryDTO> {
  try {
    return await SettingsService.GetSyncRegistry();
  } catch (err) {
    throw normalize(err);
  }
}

// setupSync (BACI-111) drives the SyncSetupModal — three modes
// (init / clone / attach) map 1:1 onto the engine's bootstrap paths.
// On a renumber-collision refusal the Wails service returns the
// SyncSetupDTO with `previewCollisions` populated and a nil Go-side
// error; the JS-side seam detects the populated preview and throws
// SyncSetupCollisionError so the modal can `instanceof`-branch into
// the step-2 confirm. Any other failure (validation / engine error /
// lock timeout) surfaces as a plain Error via normalize().
export async function setupSync(
  prefix: string,
  payload: SyncSetupPayload,
): Promise<SyncSetupResult> {
  let result: SyncSetupDTO;
  try {
    result = await SettingsService.SetupSync(prefix, payload);
  } catch (err) {
    throw normalize(err);
  }
  if (result.previewCollisions) {
    throw new SyncSetupCollisionError(result);
  }
  return result;
}

// linkPhantomRepo (BACI-112) binds a phantom repo (a sync_clone-
// imported row with no local path) to a local working tree. Drives
// the PhantomLinkModal on the desktop / web SyncView. Errors come
// back as plain Error here; the caller renders the message inline.
export async function linkPhantomRepo(
  prefix: string,
  path: string,
): Promise<RepoLinkResult> {
  try {
    return await SettingsService.LinkPhantomRepo(prefix, path);
  } catch (err) {
    throw normalize(err);
  }
}

// getLeaderStatus returns the current UI leader-election state synchronously.
// Used on mount to seed the UI before the first "leaderStatus" event arrives.
export async function getLeaderStatus(): Promise<LeaderStatusDTO> {
  try {
    return await LeaderService.GetLeaderStatus();
  } catch (err) {
    throw normalize(err);
  }
}
