// Kanban-domain HTTP transport (the pivot). Fetch wrappers + reshapers
// over the `bacio api` REST surface; the public ./api surface is the same
// as the Wails seam's. See ./client.http for the shared plumbing.
//
// **The return-shape bridge.** Three seam calls answer with the whole
// refreshed board — reorderKanbanColumn, deleteKanbanColumn,
// moveIssueToKanbanColumn — because the Wails services do and because the
// store re-densifies every sibling's position around the write. The REST
// routes answer more narrowly (the moved lane, a 204, the moved issue), so
// each of those three re-reads GET .../kanban/columns after its write.
// That extra round trip is the price of one seam signature; a caller
// rendering board order would have had to make it anyway.
import { call } from './client.http';
import { reshapeKanbanBoard, reshapeKanbanColumn, reshapeKanbanColumnDeletePreview } from './wire/kanban';
import type { ApiKanbanColumn, ApiKanbanColumnDeletePreview } from './wire/kanban';
import type { KanbanColumn, KanbanColumnDeletePreview } from './contract';

// requireRepo guards the "all repositories" pseudo-board — the Kanban is
// per-repo on both transports.
function requireRepo(repoPrefix: string, verb: string): string {
  if (!repoPrefix || repoPrefix === 'all') {
    throw new Error(`select a repository to ${verb}`);
  }
  return encodeURIComponent(repoPrefix);
}

function fetchBoard(prefix: string): Promise<ApiKanbanColumn[]> {
  return call<ApiKanbanColumn[]>(`/repos/${prefix}/kanban/columns`);
}

// listKanbanColumns returns the repo's lanes in board order, each with its
// ordered card references.
export async function listKanbanColumns(repoPrefix: string): Promise<KanbanColumn[]> {
  const prefix = requireRepo(repoPrefix, 'view its Kanban board');
  return reshapeKanbanBoard(await fetchBoard(prefix));
}

// createKanbanColumn appends a lane to the right-hand end of the board.
export async function createKanbanColumn(
  repoPrefix: string,
  name: string,
): Promise<KanbanColumn> {
  const prefix = requireRepo(repoPrefix, 'add a Kanban lane');
  const col = await call<ApiKanbanColumn>(`/repos/${prefix}/kanban/columns`, {
    method: 'POST',
    body: { name },
  });
  return reshapeKanbanColumn(col);
}

// renameKanbanColumn renames a lane in place; its position and cards are
// untouched.
export async function renameKanbanColumn(
  repoPrefix: string,
  uuid: string,
  newName: string,
): Promise<KanbanColumn> {
  const prefix = requireRepo(repoPrefix, 'rename a Kanban lane');
  // PATCH is a presence map: the key that is PRESENT selects the
  // operation. `name` alone renames; sending `position` too is a 400.
  const col = await call<ApiKanbanColumn>(
    `/repos/${prefix}/kanban/columns/${encodeURIComponent(uuid)}`,
    { method: 'PATCH', body: { name: newName } },
  );
  return reshapeKanbanColumn(col);
}

// reorderKanbanColumn moves a lane to `position` (0-based, clamped to the
// board) and returns the WHOLE refreshed board. The PATCH answers with the
// moved lane only, so re-read the board to see the re-densified siblings.
export async function reorderKanbanColumn(
  repoPrefix: string,
  uuid: string,
  position: number,
): Promise<KanbanColumn[]> {
  const prefix = requireRepo(repoPrefix, 'reorder a Kanban lane');
  await call<ApiKanbanColumn>(
    `/repos/${prefix}/kanban/columns/${encodeURIComponent(uuid)}`,
    { method: 'PATCH', body: { position } },
  );
  return reshapeKanbanBoard(await fetchBoard(prefix));
}

// previewDeleteKanbanColumn reports how many cards a lane delete would
// take off the board WITHOUT deleting anything — the dry run behind the
// confirmation dialog.
export async function previewDeleteKanbanColumn(
  repoPrefix: string,
  uuid: string,
): Promise<KanbanColumnDeletePreview> {
  const prefix = requireRepo(repoPrefix, 'delete a Kanban lane');
  // Same DELETE route as the real write; `dry_run=true` flips it to a 200
  // with the preview body instead of a 204.
  const preview = await call<ApiKanbanColumnDeletePreview>(
    `/repos/${prefix}/kanban/columns/${encodeURIComponent(uuid)}`,
    { method: 'DELETE', query: { dry_run: true } },
  );
  return reshapeKanbanColumnDeletePreview(preview);
}

// deleteKanbanColumn removes a lane for real and takes its cards off the
// board (the issues themselves survive). The DELETE answers 204, so
// re-read the board to return the same refreshed shape the Wails twin does.
export async function deleteKanbanColumn(
  repoPrefix: string,
  uuid: string,
): Promise<KanbanColumn[]> {
  const prefix = requireRepo(repoPrefix, 'delete a Kanban lane');
  await call<void>(`/repos/${prefix}/kanban/columns/${encodeURIComponent(uuid)}`, {
    method: 'DELETE',
  });
  return reshapeKanbanBoard(await fetchBoard(prefix));
}

// moveIssueToKanbanColumn places a card on the Kanban — the drag-drop
// write.
//
// columnUuid === '' takes the card OFF the board entirely; that empty
// string is the only way to un-opt a git-repo card, so `column_uuid` is
// always sent and never omitted.
//
// position is the 0-based slot in the target lane; null appends. null maps
// to an ABSENT `position` on the wire, because the server's `*int` reads
// absent as "append" and 0 as "the top of the lane" — the two must not
// collapse.
//
// The PUT answers with the moved issue, which has nowhere to carry a lane,
// so the refreshed board is re-read.
export async function moveIssueToKanbanColumn(
  repoPrefix: string,
  key: string,
  columnUuid: string,
  position: number | null,
): Promise<KanbanColumn[]> {
  const prefix = requireRepo(repoPrefix, 'move a card on the Kanban board');
  const body: { issue_key: string; column_uuid: string; position?: number } = {
    issue_key: key,
    column_uuid: columnUuid,
  };
  if (position !== null) body.position = position;
  await call<unknown>(`/repos/${prefix}/issues/${encodeURIComponent(key)}/kanban`, {
    method: 'PUT',
    body,
  });
  return reshapeKanbanBoard(await fetchBoard(prefix));
}
