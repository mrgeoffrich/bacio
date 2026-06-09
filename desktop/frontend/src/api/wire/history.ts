// History-domain wire type + reshaper (BACI-358).
//
// ApiHistoryEntry mirrors one audit-log row as the `bacio api` server
// serialises it; reshapeHistoryEntry maps it into the camelCase
// HistoryEntryDTO the History page consumes. The over-fetch / HasMore
// paging logic stays in api.http.ts's listHistory — only the per-row
// reshape moves here. See ./issue.ts for the pattern + the Phase 2b note.

import type { HistoryEntryDTO } from '../contract';

export interface ApiHistoryEntry {
  id: number;
  actor: string;
  op: string;
  kind?: string;
  target_label?: string;
  details?: string;
  created_at: string;
}

export function reshapeHistoryEntry(e: ApiHistoryEntry): HistoryEntryDTO {
  return {
    id: e.id,
    actor: e.actor,
    op: e.op,
    kind: e.kind ?? '',
    targetLabel: e.target_label ?? '',
    details: e.details ?? '',
    createdAt: e.created_at,
  };
}
