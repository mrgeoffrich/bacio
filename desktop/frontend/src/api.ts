// Thin typed wrapper over the generated BoardService Wails bindings.
// Centralises the binding import path and normalises rejections to Error so
// the React components can stay unaware of Wails specifics.
import {
  BoardService,
  Board,
  BoardColumn,
  BoardCard,
  IssueDetail,
  AgentCard,
  ClaimDTO,
  DispatchDTO,
} from '../bindings/github.com/mrgeoffrich/bacio/desktop';

export type { Board, BoardColumn, BoardCard, IssueDetail, AgentCard, ClaimDTO, DispatchDTO };

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

export async function dispatchIssue(
  repoPrefix: string,
  issueKey: string,
  agentName: string,
  mode: string,
  note: string,
): Promise<DispatchDTO> {
  try {
    return await BoardService.DispatchIssue(repoPrefix, issueKey, agentName, mode, note);
  } catch (err) {
    throw normalize(err);
  }
}
