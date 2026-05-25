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
  FeatureComment as FeatureCommentDTO,
  HistoryPage,
  HistoryEntryDTO,
  LeaderStatusDTO,
  PromptTemplateDTO,
  BoardPreferencesDTO,
  ArchivePreferencesDTO,
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
} from '../bindings/github.com/mrgeoffrich/bacio/desktop';
import { ClaimDTO } from '../bindings/github.com/mrgeoffrich/bacio/internal/agentcards';
// BACI-145: re-export the WaitingState / WaitingKind enums from the
// boardcards binding so the React components import them from the
// same api.ts seam as everything else (avoids one-off binding paths
// scattered through the kanban code).
import { WaitingState, WaitingKind } from '../bindings/github.com/mrgeoffrich/bacio/internal/boardcards';

export type { Board, BoardColumn, BoardCard, IssueDetail, IssueBriefDTO, IssueMetaDTO, LinkedDocDTO, FeatureRefDTO, RelationDTO, RelationsDTO, PRDTO, CommentDTO, AgentCard, ClaimDTO, DispatchDTO, DocSummary, DocContent, DocLinkDTO, FeatureSummary, FeatureDetail, FeatureLinkedIssue, FeatureCommentDTO, HistoryPage, HistoryEntryDTO, LeaderStatusDTO, PromptTemplateDTO, BoardPreferencesDTO, ArchivePreferencesDTO, WaitingState, SyncPreferencesDTO, SyncRegistryDTO, SyncRepoDTO, MemberProjectDTO, UnsyncedProjectDTO, SyncSetupDTO, CollisionPreviewDTO, RenumberEntryDTO, RenameEntryDTO, RepoLinkResultDTO };

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
// claimants + derived taken / waitingForClaim flags. Backs the
// top-level IssueWorkspace view.
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

export async function listDocs(repoPrefix: string, typeFilter = ''): Promise<DocSummary[]> {
  try {
    return await DocService.ListDocs(repoPrefix, typeFilter);
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
  states: string[],
  actionLabel: string = '',
): Promise<PromptTemplateDTO> {
  try {
    return await SettingsService.AddPromptTemplate(slug, name, body, states, actionLabel);
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

// savePromptStates stores the set of issue states a dispatch stage's
// prompt is valid to run from. Passing an empty array resets that
// stage's state-gate to its built-in default.
export async function savePromptStates(
  mode: string,
  states: string[],
): Promise<PromptTemplateDTO> {
  try {
    return await SettingsService.SavePromptStates(mode, states);
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

// getBoardPreferences returns the persisted desktop Board UI
// preferences (or the built-in defaults when none are stored).
export async function getBoardPreferences(): Promise<BoardPreferencesDTO> {
  try {
    return await SettingsService.GetBoardPreferences();
  } catch (err) {
    throw normalize(err);
  }
}

// setBoardPreferences stores the Board's hide-empty-columns preference
// and returns the refreshed DTO.
export async function setBoardPreferences(
  hideEmptyColumns: boolean,
): Promise<BoardPreferencesDTO> {
  try {
    return await SettingsService.SetBoardPreferences(hideEmptyColumns);
  } catch (err) {
    throw normalize(err);
  }
}

// BACI-68: display.show_archived global toggle. Same shape as the
// board-preferences pair; lives behind a dedicated Wails endpoint so
// the Settings panel can read / write it without coupling display
// state to board state.
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
