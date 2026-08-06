// Thin Wails barrel (BACI-359, Phase 2b).
//
// The transport implementation now lives in per-domain modules under
// ./api/* (each a typed wrapper over the generated Wails bindings that
// normalises rejections to Error). This file re-exports them plus the
// shared DTO contract, so components keep importing everything from `./api`
// and the public surface is unchanged. Vite swaps the whole module for
// api.http.ts in web mode (see vite.config.ts).
export * from './api/board';
export * from './api/issue';
export * from './api/feature';
export * from './api/pipeline';
export * from './api/agents';
export * from './api/notifications';
export * from './api/docs';
export * from './api/kanban';
export * from './api/monitor';
export * from './api/settings';
export * from './api/sync';

// DTO type surface — the single source both transports compile against
// (BACI-359). Names the Wails seam historically exported (including the
// `*DTO` aliases) are all kept so component imports don't change.
export type {
  Board,
  RepoActivity,
  BoardColumn,
  BoardCard,
  BoardCardTodo,
  BoardCardBlocker,
  BoardCardQuestion,
  BoardCardJob,
  BoardCardLatestPR,
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
  ClaimDTO,
  DispatchDTO,
  QuestionDTO,
  SessionTodoDTO,
  DocSummary,
  DocSummaryLink,
  DocContent,
  DocFolder,
  DocFolderDeletePreview,
  KanbanCardRef,
  KanbanColumn,
  KanbanColumnDeletePreview,
  RepoKind,
  DocLinkDTO,
  FeatureSummary,
  FeatureDetail,
  FeatureLinkedIssue,
  FeatureLinkedDoc,
  FeaturePlan,
  FeaturePlanEntry,
  FeatureCommentDTO,
  HistoryPage,
  HistoryEntryDTO,
  LeaderStatusDTO,
  PromptTemplateDTO,
  ArchivePreferencesDTO,
  AudioPreferencesDTO,
  TimezonePreferencesDTO,
  WaitingState,
  WaitingKind,
  SyncPreferences,
  SyncPreferencesDTO,
  SyncRegistry,
  SyncRegistryDTO,
  SyncRepoEntry,
  SyncRepoDTO,
  MemberProject,
  MemberProjectDTO,
  UnsyncedProject,
  UnsyncedProjectDTO,
  SyncSetupPayload,
  SyncSetupResult,
  SyncSetupDTO,
  CollisionPreviewDTO,
  RenumberEntryDTO,
  RenameEntryDTO,
  RepoLinkResult,
  RepoLinkResultDTO,
  ShippedIssueDTO,
  ShippedListDTO,
  LatestPlan,
  LatestPlanDTO,
  Notification,
  PipelineJob,
  AddRepositoryPayload,
  DisplayPreferencesDTO,
  DefaultFeatureDTO,
  BoardHiddenStatesDTO,
} from './api/contract';
// ProcessSelection (BACI-283) — the picker's discriminated handback; shared
// runtime-free lib type re-exported from the seam.
export type { ProcessSelection } from './lib/pipelineProcesses';
