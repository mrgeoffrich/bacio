// Thin typed wrapper over the generated Wails bindings. Centralises the
// binding import path and normalises rejections to Error so the React
// components can stay unaware of Wails specifics.
import {
  BoardService,
  DocService,
  FeatureService,
  HistoryService,
  SettingsService,
  Board,
  BoardColumn,
  BoardCard,
  IssueDetail,
  AgentCard,
  ClaimDTO,
  DispatchDTO,
  DocSummary,
  DocContent,
  DocLinkDTO,
  FeatureSummary,
  FeatureDetail,
  FeatureLinkedIssue,
  HistoryPage,
  HistoryEntryDTO,
  PromptTemplateDTO,
  BoardPreferencesDTO,
} from '../bindings/github.com/mrgeoffrich/bacio/desktop';

export type { Board, BoardColumn, BoardCard, IssueDetail, AgentCard, ClaimDTO, DispatchDTO, DocSummary, DocContent, DocLinkDTO, FeatureSummary, FeatureDetail, FeatureLinkedIssue, HistoryPage, HistoryEntryDTO, PromptTemplateDTO, BoardPreferencesDTO };

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

// addRepository opens a native folder picker and registers the chosen git
// working tree. The returned Board has an empty prefix if the user cancelled.
export async function addRepository(): Promise<Board> {
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

export async function listAgents(repoPrefix: string): Promise<AgentCard[]> {
  try {
    return await BoardService.ListAgents(repoPrefix);
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
// agent) — the caller never names one.
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
export async function addComment(
  repoPrefix: string,
  key: string,
  author: string,
  body: string,
): Promise<IssueDetail> {
  try {
    return await BoardService.AddComment(repoPrefix, key, author, body);
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

// listPromptTemplates returns the five customisable dispatch prompt
// templates, in job-lifecycle order.
export async function listPromptTemplates(): Promise<PromptTemplateDTO[]> {
  try {
    return await SettingsService.ListPromptTemplates();
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
