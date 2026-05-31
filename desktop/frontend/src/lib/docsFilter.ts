// docsFilter — pure helpers for the BACI-204 Documents page facet
// rail + toolbar. Keeps the reducer/row code in DocsList.jsx free of
// branchy filter logic so the contract can be smoke-tested in plain
// Node (see desktop/frontend/src/lib/__tests__/docsFilter.smoketest.mjs).
//
// Shapes are deliberately TS-loose — the only inputs are the
// DocSummary rows from `api.listDocs` (Wails or HTTP), plus a small
// query bag the rail + toolbar drive. Output is the lean
// `{visible, counts}` pair the list needs to render. The Type facet
// in the rail's tablist is the single transcript-filtering control
// (BACI-219 retired the toolbar's Hide-transcripts checkbox so two
// ways to hide the same bucket collapsed back to one).

// Doc is the structural shape this module reads. Mirrors DocSummary
// from api.ts / api.http.ts but only the fields the filter touches —
// callers can pass either DTO transparently.
export interface Doc {
  filename: string;
  type: string;
  sizeBytes: number;
  updatedAt: string;
  createdAt: string;
  archivedAt?: string | null;
  snippet?: string | null;
  links?: { issueKey?: string; featureSlug?: string; description?: string }[] | null;
}

// LinksFacet groups every row by its link-edge presence — backs the
// rail's "Has issue / Has feature / Unlinked" tablist.
export type LinksFacet = 'all' | 'issue' | 'feature' | 'unlinked';

// StatusFacet defers to the global display.show_archived setting when
// set to `all`; explicit `active` / `archived` overrides for the user
// who toggles in the rail itself.
export type StatusFacet = 'active' | 'archived' | 'all';

export type SortKey = 'updated' | 'created' | 'name' | 'size';

export interface DocsQuery {
  search: string;
  type: string; // '' = all types
  links: LinksFacet;
  status: StatusFacet;
  sort: SortKey;
}

export interface DocsCounts {
  // Counts before the facet filter — exactly the per-bucket totals
  // the rail chips display so the user always sees the absolute size
  // of each bucket, not a count that collapses to zero as they narrow
  // the selection (matches the FeaturesView precedent).
  total: number;
  active: number;
  archived: number;
  withIssueLink: number;
  withFeatureLink: number;
  unlinked: number;
  transcript: number;
  byType: Record<string, number>;
}

export interface FilterResult {
  visible: Doc[];
  counts: DocsCounts;
}

// Conservative-by-filename transcript predicate so a hand-uploaded
// `.jsonl` doc doesn't accidentally collapse into the transcript
// bucket. Kept local so this module stays import-free (the smoketest
// doesn't have a TS loader for `docFormat.ts`'s caller chain). Still
// used by the facet rail to count legacy `bacio-transcript-*.jsonl`
// docs (BACI-307 retired the .jsonl-attach path but left existing rows
// in place; BACI-308 re-points per-job transcript viewing at the
// captured-message source).
export function isTranscriptDoc(d: Doc): boolean {
  if (d.type === 'transcript') return true;
  const f = d.filename.trim().toLowerCase();
  return (
    f.startsWith('bacio-transcript-') &&
    f.endsWith('.jsonl') &&
    f.includes('-agent-')
  );
}

// matchesLinks returns true if the doc satisfies the given link facet.
function matchesLinks(d: Doc, facet: LinksFacet): boolean {
  const links = d.links ?? [];
  switch (facet) {
    case 'all':
      return true;
    case 'issue':
      return links.some(l => l.issueKey);
    case 'feature':
      return links.some(l => l.featureSlug);
    case 'unlinked':
      return links.length === 0;
  }
}

// matchesSearch is a case-insensitive contains across filename,
// snippet, type, and the targets of every link (so a search for
// "BACI-204" surfaces docs linked to that issue).
function matchesSearch(d: Doc, q: string): boolean {
  if (!q) return true;
  const needle = q.trim().toLowerCase();
  if (!needle) return true;
  if (d.filename.toLowerCase().includes(needle)) return true;
  if ((d.snippet ?? '').toLowerCase().includes(needle)) return true;
  if (d.type.toLowerCase().includes(needle)) return true;
  for (const l of d.links ?? []) {
    if (l.issueKey && l.issueKey.toLowerCase().includes(needle)) return true;
    if (l.featureSlug && l.featureSlug.toLowerCase().includes(needle)) return true;
  }
  return false;
}

// matchesStatus respects the explicit facet override, otherwise
// defers to showArchived (the global display.show_archived setting
// the desktop / web already plumb through).
function matchesStatus(d: Doc, facet: StatusFacet, showArchived: boolean): boolean {
  const archived = !!d.archivedAt;
  switch (facet) {
    case 'active':
      return !archived;
    case 'archived':
      return archived;
    case 'all':
      return showArchived ? true : !archived;
  }
}

// sortDocs returns a new array sorted by sortKey. Stable: the input
// arrives sorted by filename from the store-side ORDER BY, so ties
// (same updatedAt / same size) keep the alphabetical order from the
// API.
export function sortDocs(docs: Doc[], sort: SortKey): Doc[] {
  const out = docs.slice();
  switch (sort) {
    case 'updated':
      out.sort((a, b) => cmpStr(b.updatedAt, a.updatedAt));
      break;
    case 'created':
      out.sort((a, b) => cmpStr(b.createdAt, a.createdAt));
      break;
    case 'name':
      out.sort((a, b) => a.filename.toLowerCase().localeCompare(b.filename.toLowerCase()));
      break;
    case 'size':
      out.sort((a, b) => b.sizeBytes - a.sizeBytes);
      break;
  }
  return out;
}

function cmpStr(a: string, b: string): number {
  if (a === b) return 0;
  return a < b ? -1 : 1;
}

// countFacets walks the doc list once and counts each bucket the
// rail chips render. The counts ignore the in-flight query so the
// rail never collapses to zero as the user narrows — matches the
// FeaturesView precedent.
export function countFacets(docs: Doc[]): DocsCounts {
  const counts: DocsCounts = {
    total: docs.length,
    active: 0,
    archived: 0,
    withIssueLink: 0,
    withFeatureLink: 0,
    unlinked: 0,
    transcript: 0,
    byType: {},
  };
  for (const d of docs) {
    if (d.archivedAt) counts.archived++;
    else counts.active++;
    const links = d.links ?? [];
    if (links.length === 0) counts.unlinked++;
    if (links.some(l => l.issueKey)) counts.withIssueLink++;
    if (links.some(l => l.featureSlug)) counts.withFeatureLink++;
    if (isTranscriptDoc(d)) counts.transcript++;
    counts.byType[d.type] = (counts.byType[d.type] ?? 0) + 1;
  }
  return counts;
}

// filterDocs runs the rail + toolbar query against the full doc set
// and returns the lean visible/counts pair the list renders.
// showArchived is the global display.show_archived setting; the
// status facet's `all` mode defers to it.
//
// BACI-219 retired the transcript fold — the rail's Type tablist is
// now the only transcript-filtering control. The rail's transcript
// count (in `counts.transcript`) stays honest for the chip render.
export function filterDocs(
  docs: Doc[],
  q: DocsQuery,
  showArchived: boolean,
): FilterResult {
  const counts = countFacets(docs);
  const matched: Doc[] = [];
  for (const d of docs) {
    if (!matchesStatus(d, q.status, showArchived)) continue;
    if (q.type && d.type !== q.type) continue;
    if (!matchesLinks(d, q.links)) continue;
    if (!matchesSearch(d, q.search)) continue;
    matched.push(d);
  }
  const visible = sortDocs(matched, q.sort);
  return { visible, counts };
}
