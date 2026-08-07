// Thin HTTP barrel (BACI-359, Phase 2b).
//
// Fetch-based variant of api.ts that talks to a remote `bacio api` server
// instead of Wails-bound services. Vite swaps this in for api.ts when
// MODE === 'web' (see vite.config.ts). The per-domain implementations live
// in ./api/*.http.ts (built on the shared ./api/client.http plumbing); this
// file re-exports them plus the shared DTO contract so the public ./api
// surface mirrors the Wails seam one-for-one.
export * from './api/board.http';
export * from './api/issue.http';
export * from './api/feature.http';
export * from './api/pipeline.http';
export * from './api/agents.http';
export * from './api/notifications.http';
export * from './api/docs.http';
export * from './api/kanban.http';
export * from './api/monitor.http';
export * from './api/settings.http';
export * from './api/sync.http';
export { WebModeUnavailableError } from './api/client.http';

// DTO type surface — the single source both transports compile against
// (BACI-359). Mirrors the api.ts barrel's contract re-export.
export type {
  Board,
  RepoActivity,
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
  DocFolder,
  DocFolderDeletePreview,
  KanbanCardRef,
  KanbanColumn,
  KanbanColumnDeletePreview,
  RepoKind,
  FeatureSummary,
  FeatureLinkedIssue,
  FeatureCommentDTO,
  FeatureLinkedDoc,
  FeaturePlanEntry,
  FeaturePlan,
  FeatureDetail,
  FeatureUpdateFields,
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
