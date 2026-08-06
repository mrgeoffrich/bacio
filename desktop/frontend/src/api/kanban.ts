// Kanban-domain Wails calls (the pivot) — the human lane axis, orthogonal
// to `state` and to the Agentic Pipeline.
//
// Naming: `KanbanColumn` end-to-end. NOT `BoardColumn`, which is a shipped
// DTO meaning `{state, label}` and is still consumed by
// components/settings/PerRepoSettingsSection — the two are unrelated.
//
// **Three of these return the whole refreshed board, not the thing you
// wrote** (reorderKanbanColumn, deleteKanbanColumn, moveIssueToKanbanColumn).
// That is the seam signature on purpose: the store re-densifies every
// sibling's position around the write, so a caller rendering board order
// has to re-read them regardless. The Wails services already answer this
// way; the HTTP twin re-reads the board after its write to match.
import { BoardService } from '../../bindings/github.com/mrgeoffrich/bacio/desktop';
import type { KanbanColumn, KanbanColumnDeletePreview } from './contract';
import { normalize } from './normalize';

// listKanbanColumns returns the repo's lanes in board order, each with its
// ordered card references. A card is on the Kanban if and only if it
// appears in some lane's `cards`.
export async function listKanbanColumns(repoPrefix: string): Promise<KanbanColumn[]> {
  try {
    return await BoardService.ListKanbanColumns(repoPrefix);
  } catch (err) {
    throw normalize(err);
  }
}

// createKanbanColumn appends a lane to the right-hand end of the board.
export async function createKanbanColumn(
  repoPrefix: string,
  name: string,
): Promise<KanbanColumn> {
  try {
    return await BoardService.CreateKanbanColumn(repoPrefix, name);
  } catch (err) {
    throw normalize(err);
  }
}

// renameKanbanColumn renames a lane in place; its position and cards are
// untouched.
export async function renameKanbanColumn(
  repoPrefix: string,
  uuid: string,
  newName: string,
): Promise<KanbanColumn> {
  try {
    return await BoardService.RenameKanbanColumn(repoPrefix, uuid, newName);
  } catch (err) {
    throw normalize(err);
  }
}

// reorderKanbanColumn moves a lane to `position` (0-based, clamped to the
// board) and returns the WHOLE refreshed board — the siblings re-densify
// underneath.
export async function reorderKanbanColumn(
  repoPrefix: string,
  uuid: string,
  position: number,
): Promise<KanbanColumn[]> {
  try {
    return await BoardService.ReorderKanbanColumn(repoPrefix, uuid, position);
  } catch (err) {
    throw normalize(err);
  }
}

// previewDeleteKanbanColumn reports how many cards a lane delete would
// take off the board WITHOUT deleting anything — the dry run behind the
// confirmation dialog.
export async function previewDeleteKanbanColumn(
  repoPrefix: string,
  uuid: string,
): Promise<KanbanColumnDeletePreview> {
  try {
    return await BoardService.PreviewDeleteKanbanColumn(repoPrefix, uuid);
  } catch (err) {
    throw normalize(err);
  }
}

// deleteKanbanColumn removes a lane for real and takes its cards off the
// board (the issues themselves survive). Returns the refreshed board.
export async function deleteKanbanColumn(
  repoPrefix: string,
  uuid: string,
): Promise<KanbanColumn[]> {
  try {
    return await BoardService.DeleteKanbanColumn(repoPrefix, uuid);
  } catch (err) {
    throw normalize(err);
  }
}

// moveIssueToKanbanColumn places a card on the Kanban — the drag-drop
// write. Wraps BoardService.SetIssueKanbanColumn.
//
// columnUuid === '' takes the card OFF the board entirely; that empty
// string is the only way to un-opt a git-repo card, so it is a meaningful
// value rather than a missing argument.
//
// position is the 0-based top-to-bottom slot in the target lane; pass null
// to append. Both the source and the target lane re-densify around the
// move, so this returns the whole refreshed board.
export async function moveIssueToKanbanColumn(
  repoPrefix: string,
  key: string,
  columnUuid: string,
  position: number | null,
): Promise<KanbanColumn[]> {
  try {
    return await BoardService.SetIssueKanbanColumn(repoPrefix, key, columnUuid, position);
  } catch (err) {
    throw normalize(err);
  }
}
