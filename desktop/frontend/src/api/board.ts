// Board-domain Wails calls (BACI-359): repos, columns, cards, history.
import { BoardService, HistoryService } from '../../bindings/github.com/mrgeoffrich/bacio/desktop';
import type { Board, BoardColumn, BoardCard, RepoActivity, AddRepositoryPayload, HistoryPage } from './contract';
import { normalizeRepoKind } from './wire/repo';
import { normalize } from './normalize';

// WailsBoard is the generated binding's Board — identical to the contract
// shape except that `kind` is typed as a bare `string`. Derived from the
// binding's own return type so a regenerated binding that drops or renames
// a field breaks here rather than silently downstream.
type WailsBoard = Awaited<ReturnType<typeof BoardService.ListBoards>>[number];

// toBoard narrows the binding's `kind: string` onto the contract's
// RepoKind union. Everything else is already contract-shaped, so this is a
// spread plus the one narrowing — the HTTP twin does the same job inside
// boardWithSync.
function toBoard(b: WailsBoard): Board {
  return { ...b, kind: normalizeRepoKind(b.kind) };
}

export async function listBoards(): Promise<Board[]> {
  try {
    return (await BoardService.ListBoards()).map(toBoard);
  } catch (err) {
    throw normalize(err);
  }
}

// listRepoActivity returns the per-repo activity summary the topbar
// picker ranks itself by (BACI-369). Polled, so it stays off the Board
// payload.
export async function listRepoActivity(): Promise<RepoActivity[]> {
  try {
    return await BoardService.ListRepoActivity();
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

// getLaunchRepo returns the prefix of the repo the app was launched from
// (BACI-368), or '' when it wasn't started inside one. RepoProvider uses it
// as the highest-priority default when the URL carries no prefix.
export async function getLaunchRepo(): Promise<string> {
  try {
    return await BoardService.LaunchRepo();
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
// working tree. The returned Board has an empty prefix if the user
// cancelled. The optional payload is web-only — desktop ignores it.
export async function addRepository(_payload?: AddRepositoryPayload): Promise<Board> {
  try {
    return toBoard(await BoardService.AddRepository());
  } catch (err) {
    throw normalize(err);
  }
}

// addWorkspace registers a manual workspace — a pathless, git-less repo row
// (kind='workspace'). Deliberately NOT addRepository with a flag: the git
// path pops a native folder picker and refuses anything outside a working
// tree, which is exactly the wrong check for a container that has no
// directory at all.
//
// prefix is optional; omit it (or pass '') to allocate one from the name
// through the same machinery a git registration uses — workspaces and git
// repos share one prefix namespace.
export async function addWorkspace(name: string, prefix?: string): Promise<Board> {
  try {
    return toBoard(await BoardService.AddWorkspace(name, prefix ?? ''));
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
