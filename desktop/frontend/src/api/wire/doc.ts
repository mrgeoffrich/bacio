// Document-domain wire types + reshapers (BACI-358).
//
// The snake_case `Api*` shapes mirror model.Document / its document_link
// rows as the `bacio api` server serialises them; reshapeDocSummary maps
// one list row into a DocSummary and reshapeDocContent maps the
// single-doc view into a DocContent. Moved out of api.http.ts so the
// reshapes are unit testable — see ./issue.ts for the pattern + the
// Phase 2b note.

import type { DocSummary, DocContent, DocFolder, DocFolderDeletePreview } from '../contract';

// ApiDocumentLink is the raw wire shape for one document_link row
// returned alongside a doc in the list response. Field naming follows
// the Go-side snake_case the API serialises (links carry the issue
// key / feature slug pre-formatted, so the client doesn't need to
// resolve them).
export interface ApiDocumentLink {
  issue_key?: string;
  feature_slug?: string;
  description?: string;
}

export interface ApiDocument {
  filename: string;
  type: string;
  size_bytes: number;
  updated_at: string;
  created_at: string;
  archived_at?: string;
  content?: string;
  // BACI-204: per-row snippet + link rows hydrated by the store-side
  // IN-query so the Documents page renders chips and previews without
  // an N+1 round trip.
  snippet?: string;
  links?: ApiDocumentLink[];
  // The pivot's tree placement. `folder_id` is model.Document's numeric
  // FK and carries `omitempty`, so it is ABSENT (not 0, not null) for a
  // page at the tree root. It never reaches a DTO — reshapeDocSummary
  // resolves it to a uuid through a FolderUuidIndex.
  folder_id?: number;
  folder_position?: number;
}

export interface ApiDocView {
  document: ApiDocument & { content: string };
  links: unknown[];
}

// ApiDocFolder mirrors model.DocFolder — the wire shape of
// GET/POST/PATCH /repos/{prefix}/doc-folders.
//
// `parent_id` is the numeric self-FK with `omitempty`: absent means a root
// folder. That is the whole reason this module exists rather than a
// one-line rename — the Wails DocFolderDTO carries `parentUuid` ('' = root)
// and the raw int64 does not survive a sync round trip, so the contract
// standardises on the uuid and the id is resolved here.
export interface ApiDocFolder {
  id: number;
  uuid: string;
  repo_id: number;
  parent_id?: number;
  name: string;
  position: number;
  created_at: string;
  updated_at: string;
}

// ApiDocFolderDeletePreview mirrors client.DocFolderDeletePreview — the
// `?dry_run=true` body of DELETE /repos/{prefix}/doc-folders/{uuid}. The
// counts are nested under `cascade` on the wire and flattened in the DTO
// to match the Wails DocFolderDeletePreviewDTO.
export interface ApiDocFolderDeletePreview {
  folder: ApiDocFolder;
  path: string;
  cascade: {
    subfolders: number;
    documents_re_rooted: number;
  };
  would_delete: boolean;
}

// FolderUuidIndex maps a folder's numeric id to its uuid. Built once from
// the folder list a call has just received, then threaded through the
// reshapers so no view ever sees an id.
export type FolderUuidIndex = ReadonlyMap<number, string>;

// folderUuidIndex builds the id→uuid lookup from a whole folder list.
// GET /repos/{prefix}/doc-folders always returns EVERY folder in the repo
// (there is deliberately no single-folder GET), so one list is enough to
// resolve every `parent_id` in it.
export function folderUuidIndex(folders: readonly ApiDocFolder[]): FolderUuidIndex {
  return new Map(folders.map(f => [f.id, f.uuid]));
}

// resolveFolderUuid turns a wire `parent_id` / `folder_id` into the DTO's
// uuid. Absent id ⇒ '' (the tree root). An id the index can't resolve also
// degrades to '' rather than throwing: a page whose folder vanished
// mid-request belongs at the root, which is exactly where the server will
// have re-rooted it.
export function resolveFolderUuid(id: number | undefined, index: FolderUuidIndex): string {
  if (id === undefined) return '';
  return index.get(id) ?? '';
}

// reshapeDocFolder maps one wire folder into a DocFolder. `index` resolves
// its parent_id; pass folderUuidIndex() over the list the folder came in.
export function reshapeDocFolder(f: ApiDocFolder, index: FolderUuidIndex): DocFolder {
  return {
    uuid: f.uuid,
    name: f.name,
    parentUuid: resolveFolderUuid(f.parent_id, index),
    position: f.position,
    createdAt: f.created_at,
    updatedAt: f.updated_at,
  };
}

// reshapeDocFolders maps a whole folder list, building the id→uuid index
// from the list itself. This is the only reshape that can resolve every
// parent without a second round trip — single-folder mutation responses
// have to be given a parent uuid (see docs.http.ts).
export function reshapeDocFolders(folders: readonly ApiDocFolder[]): DocFolder[] {
  const index = folderUuidIndex(folders);
  return folders.map(f => reshapeDocFolder(f, index));
}

// reshapeDocFolderWithParent maps a single-folder response whose parent
// uuid the caller already knows — the create and move paths, where the
// parent was an argument to the request. Avoids a list round trip just to
// echo back what we asked for.
export function reshapeDocFolderWithParent(f: ApiDocFolder, parentUuid: string): DocFolder {
  return {
    uuid: f.uuid,
    name: f.name,
    parentUuid,
    position: f.position,
    createdAt: f.created_at,
    updatedAt: f.updated_at,
  };
}

// reshapeDocFolderDeletePreview flattens the wire's nested cascade counts
// onto the flat DTO the Wails side returns directly.
export function reshapeDocFolderDeletePreview(p: ApiDocFolderDeletePreview): DocFolderDeletePreview {
  return {
    uuid: p.folder.uuid,
    name: p.folder.name,
    path: p.path,
    subfolders: p.cascade.subfolders,
    documentsReRooted: p.cascade.documents_re_rooted,
  };
}

// reshapeDocSummary maps one row of GET /repos/{prefix}/documents into a
// DocSummary, renaming the snake_case fields and the nested link chips.
// `index` resolves the row's folder_id to the DTO's folderUuid — pass
// folderUuidIndex() over the repo's folder list.
export function reshapeDocSummary(d: ApiDocument, index: FolderUuidIndex): DocSummary {
  return {
    filename: d.filename,
    type: d.type,
    sizeBytes: d.size_bytes,
    updatedAt: d.updated_at,
    createdAt: d.created_at,
    archivedAt: d.archived_at,
    snippet: d.snippet,
    links: d.links?.map(l => ({
      issueKey: l.issue_key,
      featureSlug: l.feature_slug,
      description: l.description,
    })),
    folderUuid: resolveFolderUuid(d.folder_id, index),
    // A server predating the pivot omits folder_position entirely; 0 is
    // the same "unset" sort key every migrated row carries.
    folderPosition: d.folder_position ?? 0,
  };
}

// reshapeDocContent maps the single-document view into the DocContent the
// editor consumes.
export function reshapeDocContent(view: ApiDocView): DocContent {
  const d = view.document;
  return {
    filename: d.filename,
    type: d.type,
    content: d.content ?? '',
    updatedAt: d.updated_at,
  };
}
