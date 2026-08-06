// Kanban-domain wire types + reshapers (the pivot).
//
// The snake_case `Api*` shapes mirror api.kanbanColumnOut and
// client.KanbanColumnDeletePreview as the `bacio api` server serialises
// them. The Wails twin (BoardService's KanbanColumnDTO) is already the
// contract shape, so only the HTTP side needs reshaping — the same split
// ./doc.ts has.
//
// The one mismatch worth naming: HTTP nests the lane's `cards` inside the
// FULL model.KanbanColumn, so the payload also carries `id`, `repo_id` and
// timestamps. **None of those reach the contract.** Lanes are addressed by
// `uuid` end-to-end: it is the only identity that survives a sync round
// trip, and every lane mutator on both transports takes a uuid. Dropping
// the id here is what stops a view ever reaching for it.

import type { KanbanColumn, KanbanCardRef, KanbanColumnDeletePreview } from '../contract';

// ApiKanbanCardRef mirrors api.kanbanCardRefOut. Already camel-free —
// both fields are single words — but it is reshaped anyway so the DTO
// never aliases a wire object.
export interface ApiKanbanCardRef {
  key: string;
  position: number;
}

// ApiKanbanColumn mirrors api.kanbanColumnOut: model.KanbanColumn's fields
// flattened by the embedded struct, plus the ordered `cards` refs. `cards`
// is always present ([] for an empty lane) — the server guarantees it so
// the React side needs no guard.
export interface ApiKanbanColumn {
  id: number;
  uuid: string;
  repo_id: number;
  name: string;
  position: number;
  created_at: string;
  updated_at: string;
  cards: ApiKanbanCardRef[];
}

// ApiKanbanColumnDeletePreview mirrors client.KanbanColumnDeletePreview —
// the `?dry_run=true` body of DELETE .../kanban/columns/{uuid}. The count
// is nested under `cascade` on the wire and flattened in the DTO to match
// the Wails KanbanColumnDeletePreviewDTO.
export interface ApiKanbanColumnDeletePreview {
  column: {
    id: number;
    uuid: string;
    repo_id: number;
    name: string;
    position: number;
    created_at: string;
    updated_at: string;
  };
  cascade: {
    issues_removed_from_board: number;
  };
  would_delete: boolean;
}

export function reshapeKanbanCardRef(c: ApiKanbanCardRef): KanbanCardRef {
  return { key: c.key, position: c.position };
}

// reshapeKanbanColumn maps one wire lane into the contract's KanbanColumn,
// dropping id / repo_id / timestamps and copying the ordered card refs.
// `cards ?? []` defends against an older server that omitted an empty
// slice — the contract promises the array is always there.
export function reshapeKanbanColumn(c: ApiKanbanColumn): KanbanColumn {
  return {
    uuid: c.uuid,
    name: c.name,
    position: c.position,
    cards: (c.cards ?? []).map(reshapeKanbanCardRef),
  };
}

export function reshapeKanbanBoard(cols: readonly ApiKanbanColumn[]): KanbanColumn[] {
  return cols.map(reshapeKanbanColumn);
}

export function reshapeKanbanColumnDeletePreview(
  p: ApiKanbanColumnDeletePreview,
): KanbanColumnDeletePreview {
  return {
    uuid: p.column.uuid,
    name: p.column.name,
    issuesRemovedFromBoard: p.cascade.issues_removed_from_board,
  };
}
