// docsFilter — pure helpers for the BACI-204 Documents page facet
// rail + toolbar. Keeps the reducer/row code in DocsList.tsx free of
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
  // The pivot's hierarchy fields. Optional here (not on DocSummary, where
  // both are required) so pre-pivot fixtures and the plain-Node smoke tests
  // can build a row without them; `folderUuid ?? ''` is the tree root, which
  // is exactly what a missing value should mean.
  folderUuid?: string;
  folderPosition?: number;
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

// ─── The page tree (the pivot) ───────────────────────────────────────
//
// Everything below is pure and total: no throw, no api import, no React.
// The rail assembles the hierarchy client-side from two flat lists —
// `api.listDocs` rows carrying `folderUuid` / `folderPosition`, and the
// full `api.listDocFolders` list — so the display path of any node is
// derivable without a round trip per level.
//
// **`''` is a value, not an absence.** `parentUuid === ''` /
// `folderUuid === ''` means the tree ROOT, which is not itself a folder.

// Folder is the structural shape this module reads off the api seam's
// DocFolder — the fields the tree needs, nothing else, so a caller can
// pass a DocFolder transparently (same contract as `Doc` above).
export interface Folder {
  uuid: string;
  name: string;
  parentUuid: string;
  position: number;
}

// A folder node. `docCount` is the page count of the WHOLE subtree, which
// is what the rail's per-row count and the "N pages" line on the folder
// page both want — a folder whose pages all live one level down should not
// read as empty.
export interface DocTreeFolder {
  kind: 'folder';
  uuid: string;
  name: string;
  position: number;
  children: DocTreeNode[];
  docCount: number;
}

// A page node. `filename` is lifted out of `doc` because it is the
// selection identity everywhere else in the view (URL slug, api argument,
// React key) — filenames stay globally unique per repo, so it is a safe key.
export interface DocTreeDoc {
  kind: 'doc';
  filename: string;
  doc: Doc;
}

export type DocTreeNode = DocTreeFolder | DocTreeDoc;

// The store caps folder nesting at 16. The client cap is deliberately
// looser and exists only to guarantee the ancestry walks terminate on a
// malformed list — it is a safety net, not a policy.
const DEPTH_CAP = 64;

function cmpFolders(a: Folder, b: Folder): number {
  if (a.position !== b.position) return a.position - b.position;
  return a.name.toLowerCase().localeCompare(b.name.toLowerCase());
}

// Docs inside one folder order by their manual sort key, then filename.
// folderPosition is a SORT KEY, not a dense index: siblings may share one
// (every pre-pivot row sits at 0), and the filename tie-break is what keeps
// those in their historical alphabetical order.
function cmpDocsInFolder(a: Doc, b: Doc): number {
  const pa = a.folderPosition ?? 0;
  const pb = b.folderPosition ?? 0;
  if (pa !== pb) return pa - pb;
  return a.filename.toLowerCase().localeCompare(b.filename.toLowerCase());
}

// resolveParent normalises one folder's parent pointer against the list we
// actually hold: a dangling uuid, a cycle, or a runaway chain all resolve
// to the root. Without this a folder whose parent is missing from the list
// would silently disappear from the tree along with every page under it —
// data loss on screen, from a purely presentational bug.
function resolveParent(f: Folder, byUuid: Map<string, Folder>): string {
  if (!f.parentUuid) return '';
  if (!byUuid.has(f.parentUuid)) return '';
  const seen = new Set<string>([f.uuid]);
  let cur: string = f.parentUuid;
  let hops = 0;
  while (cur) {
    if (seen.has(cur)) return ''; // cycle
    const parent = byUuid.get(cur);
    if (!parent) return ''; // dangling mid-chain
    seen.add(cur);
    cur = parent.parentUuid;
    if (++hops > DEPTH_CAP) return '';
  }
  return f.parentUuid;
}

// buildTree assembles the render-ready hierarchy from the flat doc +
// folder lists. Folders sort before pages at every level (the convention
// every file tree the user has ever used follows, and the only stable
// choice given folders and pages carry two independent position spaces).
//
// Pass the ALREADY-FILTERED doc list: the tree is a grouping of the same
// rows the flat mode ranks, so `status`/`showArchived` are honoured for
// free and there is only ever one index to reason about.
export function buildTree(docs: Doc[], folders: Folder[]): DocTreeNode[] {
  const byUuid = new Map<string, Folder>();
  for (const f of folders) {
    if (f.uuid) byUuid.set(f.uuid, f);
  }

  const foldersByParent = new Map<string, Folder[]>();
  for (const f of folders) {
    if (!f.uuid) continue;
    const parent = resolveParent(f, byUuid);
    const list = foldersByParent.get(parent);
    if (list) list.push(f);
    else foldersByParent.set(parent, [f]);
  }

  const docsByFolder = new Map<string, Doc[]>();
  for (const d of docs) {
    const uuid = d.folderUuid ?? '';
    // A page filed under a folder we don't hold shows at the root rather
    // than vanishing — same reasoning as resolveParent.
    const parent = uuid && byUuid.has(uuid) ? uuid : '';
    const list = docsByFolder.get(parent);
    if (list) list.push(d);
    else docsByFolder.set(parent, [d]);
  }

  for (const list of foldersByParent.values()) list.sort(cmpFolders);
  for (const list of docsByFolder.values()) list.sort(cmpDocsInFolder);

  const build = (parentUuid: string, depth: number): DocTreeNode[] => {
    if (depth > DEPTH_CAP) return [];
    const out: DocTreeNode[] = [];
    for (const f of foldersByParent.get(parentUuid) ?? []) {
      const children = build(f.uuid, depth + 1);
      let docCount = 0;
      for (const c of children) {
        docCount += c.kind === 'folder' ? c.docCount : 1;
      }
      out.push({
        kind: 'folder',
        uuid: f.uuid,
        name: f.name,
        position: f.position,
        children,
        docCount,
      });
    }
    for (const d of docsByFolder.get(parentUuid) ?? []) {
      out.push({ kind: 'doc', filename: d.filename, doc: d });
    }
    return out;
  };

  return build('', 0);
}

// flattenTreeDocs walks the tree in render order and returns just the
// pages. That order is what the header's `‹ ›` peer-jump and the footer
// peer cards step through, so "next" always means "the next row you can
// see", including across a folder boundary.
export function flattenTreeDocs(nodes: DocTreeNode[]): Doc[] {
  const out: Doc[] = [];
  const walk = (list: DocTreeNode[]) => {
    for (const n of list) {
      if (n.kind === 'folder') walk(n.children);
      else out.push(n.doc);
    }
  };
  walk(nodes);
  return out;
}

// findFolderNode locates one folder node in a built tree. The folder page
// needs the node (for its live children index), not just the DocFolder row,
// and a selected folder can go missing under you — deleted in another
// window, or filtered out — so the caller gets null rather than a throw and
// falls back to the page view.
export function findFolderNode(nodes: DocTreeNode[], uuid: string): DocTreeFolder | null {
  if (!uuid) return null;
  for (const n of nodes) {
    if (n.kind !== 'folder') continue;
    if (n.uuid === uuid) return n;
    const hit = findFolderNode(n.children, uuid);
    if (hit) return hit;
  }
  return null;
}

// folderAncestry returns the root→leaf chain for a folder uuid, for the
// breadcrumb and for auto-expanding the rail down to the selected page.
// Returns [] for the root ('') or an unknown uuid; total on a malformed
// list (a cycle terminates at the depth cap).
export function folderAncestry(folders: Folder[], uuid: string): Folder[] {
  if (!uuid) return [];
  const byUuid = new Map<string, Folder>();
  for (const f of folders) {
    if (f.uuid) byUuid.set(f.uuid, f);
  }
  const chain: Folder[] = [];
  const seen = new Set<string>();
  let cur = uuid;
  while (cur && chain.length <= DEPTH_CAP) {
    if (seen.has(cur)) break; // cycle
    const f = byUuid.get(cur);
    if (!f) break;
    seen.add(cur);
    chain.push(f);
    cur = f.parentUuid;
  }
  chain.reverse();
  return chain;
}

// shouldFlatten is the flatten-on-filter predicate — the answer to the 208
// auto-generated session retros nobody wants to browse as a tree. The
// moment the user narrows (types in the rail search, or activates any
// facet), the rail body drops the hierarchy and shows today's proven
// ranked list; clearing the narrowing brings the tree back.
//
// Compares against each facet's NEUTRAL value, not against the user's
// persisted preferences: `sort` is deliberately excluded because re-ranking
// isn't narrowing, and a sort preference that silently killed the tree
// would be baffling.
export function shouldFlatten(q: DocsQuery): boolean {
  return (
    q.search.trim() !== '' ||
    q.type !== '' ||
    q.links !== 'all' ||
    q.status !== 'active'
  );
}

// activeFacetCount is what the facet fold's summary badge reports — how many
// chips are currently narrowing the list. A collapsed fold must never be
// able to hide the reason the rail flipped to a flat list. Search is
// excluded: it has its own always-visible box with its own clear button.
export function activeFacetCount(q: DocsQuery): number {
  let n = 0;
  if (q.type !== '') n++;
  if (q.links !== 'all') n++;
  if (q.status !== 'active') n++;
  return n;
}
