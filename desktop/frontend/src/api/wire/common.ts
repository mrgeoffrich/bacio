// Shared reshape primitives for the api/wire/* modules (BACI-358).
//
// These are the cross-domain bits that both the per-domain reshapers in
// this directory and the inline reshapes still living in api.http.ts
// reach for: the kanban state labels and the assignee single→list
// helper. Kept in one tiny module so a domain reshaper doesn't have to
// import another domain just to label a column.

// State labels mirror desktop/boardservice.go:stateLabels so the web
// build renders the same kanban column titles as the desktop. Keep in
// sync.
export const STATE_LABELS: Record<string, string> = {
  todo: 'Todo',
  in_review: 'In Review',
  done: 'Done',
  cancelled: 'Cancelled',
  in_pipeline: 'In Pipeline',
  to_be_shipped: 'To Be Shipped',
};

export function stateLabel(s: string): string {
  return STATE_LABELS[s] ?? s;
}

// assigneeList wraps the wire's single optional assignee string into the
// array the DTOs carry — `[]` when unset.
export function assigneeList(a: string | undefined | null): string[] {
  return a ? [a] : [];
}
