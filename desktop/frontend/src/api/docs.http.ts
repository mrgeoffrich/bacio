// Docs-domain HTTP transport (BACI-359). Fetch wrappers + reshapers over
// the `bacio api` REST surface; the public ./api surface is the same as the
// Wails seam's. See ./client.http for the shared plumbing.
import { call } from './client.http';
import {
  reshapeDocSummary,
  reshapeDocContent,
  reshapeDocFolders,
  reshapeDocFolder,
  reshapeDocFolderWithParent,
  reshapeDocFolderDeletePreview,
  folderUuidIndex,
} from './wire/doc';
import type {
  ApiDocument,
  ApiDocView,
  ApiDocFolder,
  ApiDocFolderDeletePreview,
} from './wire/doc';
import type {
  DocSummary,
  DocContent,
  DocFolder,
  DocFolderDeletePreview,
} from './contract';

// requireRepo guards the "all repositories" pseudo-board, which has no
// document scope on either transport.
function requireRepo(repoPrefix: string, verb: string): string {
  if (!repoPrefix || repoPrefix === 'all') {
    throw new Error(`select a repository to ${verb}`);
  }
  return encodeURIComponent(repoPrefix);
}

// fetchDocFolders reads the repo's whole folder tree in wire form. Used
// both by listDocFolders and, for the id→uuid index, by listDocs.
function fetchDocFolders(prefix: string): Promise<ApiDocFolder[]> {
  return call<ApiDocFolder[]>(`/repos/${prefix}/doc-folders`);
}

// ApiDocumentLink is the raw wire shape for one document_link row
// returned alongside a doc in the list response. Field naming follows
// the Go-side snake_case the API serialises (links carry the issue
// key / feature slug pre-formatted, so the client doesn't need to
// resolve them).
export async function listDocs(repoPrefix: string, typeFilter = ''): Promise<DocSummary[]> {
  const prefix = requireRepo(repoPrefix, 'view its documents');
  // Two round trips, deliberately. The HTTP `Document` carries the numeric
  // `folder_id`; the DTO (and the Wails twin) carry `folderUuid`, so the
  // folder list is what resolves one to the other. Fetched in parallel and
  // NOT swallowed on failure: a silently-empty index would file every page
  // at the tree root, which reads as data loss rather than a failed load.
  const [docs, folders] = await Promise.all([
    call<ApiDocument[]>(`/repos/${prefix}/documents`, {
      query: { type: typeFilter || undefined },
    }),
    fetchDocFolders(prefix),
  ]);
  const index = folderUuidIndex(folders);
  return docs.map(d => reshapeDocSummary(d, index));
}

export async function getDoc(repoPrefix: string, filename: string): Promise<DocContent> {
  if (!repoPrefix || repoPrefix === 'all') {
    throw new Error('select a repository to view its documents');
  }
  const view = await call<ApiDocView>(`/repos/${repoPrefix}/documents/${filename}`);
  return reshapeDocContent(view);
}

export async function saveDoc(
  repoPrefix: string,
  filename: string,
  content: string,
): Promise<DocContent> {
  if (!repoPrefix || repoPrefix === 'all') {
    throw new Error('select a repository to save documents');
  }
  // PATCH (body-only edit) mirrors the desktop's SaveDoc → EditDocument:
  // the document already exists, so we only push the new content and leave
  // its type untouched. PUT is the upsert handler and requires `type`, so
  // saving body-only against it 400s. The edit handler returns the
  // refreshed row without its body, so re-fetch to get the content back.
  await call<unknown>(`/repos/${repoPrefix}/documents/${filename}`, {
    method: 'PATCH',
    body: { content },
  });
  return getDoc(repoPrefix, filename);
}

export async function archiveDocument(prefix: string, filename: string): Promise<unknown> {
  return call(`/repos/${encodeURIComponent(prefix)}/documents/${encodeURIComponent(filename)}/archive`, { method: 'POST' });
}

export async function unarchiveDocument(prefix: string, filename: string): Promise<unknown> {
  return call(`/repos/${encodeURIComponent(prefix)}/documents/${encodeURIComponent(filename)}/unarchive`, { method: 'POST' });
}

// ─── Page lifecycle (the pivot) ──────────────────────────────────────

// DEFAULT_NEW_DOC_TYPE mirrors desktop/docservice.go's `defaultNewDocType`
// (model.DocTypeUserDocs). The Wails CreateDoc treats an empty docType as
// "use the default"; POST /repos/{p}/documents refuses an empty `type`
// outright, so the fallback is applied here to keep the two transports on
// the same behaviour for the same argument.
const DEFAULT_NEW_DOC_TYPE = 'user_docs';

// createDoc creates a page and returns it with its body, ready for the
// editor to open on. Mirrors the Wails CreateDoc's three-step shape:
// create, then file it under a folder if one was named, then re-read (the
// create response never echoes the body).
//
// `content` must be non-empty. The two backends genuinely disagree — this
// route rejects an empty body with 400 "content is required"
// (internal/api/handlers_documents.go's resolveDocCreateInput, deliberately
// mirroring `bacio doc add`), while Wails' DocService.CreateDoc allows it
// because store.CreateDocument validates with required=false. Rather than
// let that divergence reach the views, both transports reject it here with
// the same message, so the failure is identical and legible instead of
// working on desktop and 400-ing in the browser. Keep this in lockstep
// with the twin guard in docs.ts.
export async function createDoc(
  repoPrefix: string,
  filename: string,
  docType: string,
  content: string,
  folderUuid: string,
): Promise<DocContent> {
  if (content === '') {
    throw new Error('content is required');
  }
  const prefix = requireRepo(repoPrefix, 'create a document');
  await call<unknown>(`/repos/${prefix}/documents`, {
    method: 'POST',
    body: { filename, type: docType || DEFAULT_NEW_DOC_TYPE, content },
  });
  // The create route has no folder argument on either transport, so the
  // placement is a second write. A bad folder uuid therefore surfaces
  // after the page already exists — same ordering, same failure mode, as
  // the Wails path.
  if (folderUuid !== '') {
    await moveDocToFolder(repoPrefix, filename, folderUuid, null);
  }
  return getDoc(repoPrefix, filename);
}

// renameDoc renames a page in place and returns the refreshed content
// payload under its new name. Folder membership, links and body are
// untouched — a rename is purely the filename.
export async function renameDoc(
  repoPrefix: string,
  filename: string,
  newFilename: string,
): Promise<DocContent> {
  const prefix = requireRepo(repoPrefix, 'rename a document');
  // old_filename rides in the body because DocRenameInput is strict-decoded
  // and carries it; the server overwrites it from the URL path, which wins.
  // `type` is omitted so the rename never re-types the page — the same
  // "leave the type alone" the Wails RenameDoc passes.
  await call<unknown>(`/repos/${prefix}/documents/${encodeURIComponent(filename)}/rename`, {
    method: 'POST',
    body: { old_filename: filename, new_filename: newFilename },
  });
  return getDoc(repoPrefix, newFilename);
}

// deleteDoc permanently removes a page and every link pointing at it.
// Unlike archiveDocument this is NOT reversible — confirm with the user
// first.
export async function deleteDoc(repoPrefix: string, filename: string): Promise<void> {
  const prefix = requireRepo(repoPrefix, 'delete a document');
  await call<void>(`/repos/${prefix}/documents/${encodeURIComponent(filename)}`, {
    method: 'DELETE',
  });
}

// moveDocToFolder files a page under a folder. folderUuid === '' moves it
// back to the tree ROOT — the root is not a folder, so '' is a meaningful
// destination rather than a missing argument.
//
// position is the sort key inside the target folder; pass null to append.
// null maps to an ABSENT `position` on the wire, because the server's
// `*int` reads absent as "append" and 0 as "the top of the folder" — the
// two must not collapse.
export async function moveDocToFolder(
  repoPrefix: string,
  filename: string,
  folderUuid: string,
  position: number | null,
): Promise<void> {
  const prefix = requireRepo(repoPrefix, 'move a document');
  const body: { filename: string; folder_uuid: string; position?: number } = {
    filename,
    folder_uuid: folderUuid,
  };
  if (position !== null) body.position = position;
  await call<unknown>(`/repos/${prefix}/documents/${encodeURIComponent(filename)}/folder`, {
    method: 'PUT',
    body,
  });
}

// ─── Folder tree (the pivot) ─────────────────────────────────────────

// listDocFolders returns EVERY folder in the repo, not just the roots.
// There is deliberately no single-folder GET on the server, which is also
// why this is the one call that can resolve every `parent_id` to a uuid
// from the payload it just received.
export async function listDocFolders(repoPrefix: string): Promise<DocFolder[]> {
  const prefix = requireRepo(repoPrefix, 'view its document folders');
  return reshapeDocFolders(await fetchDocFolders(prefix));
}

// createDocFolder adds a folder. parentUuid === '' creates it at the tree
// root. Sibling names must be unique within one parent; the store rejects
// a duplicate (409) rather than silently de-duplicating.
export async function createDocFolder(
  repoPrefix: string,
  name: string,
  parentUuid: string,
): Promise<DocFolder> {
  const prefix = requireRepo(repoPrefix, 'create a document folder');
  const body: { name: string; parent_uuid?: string } = { name };
  if (parentUuid !== '') body.parent_uuid = parentUuid;
  const folder = await call<ApiDocFolder>(`/repos/${prefix}/doc-folders`, {
    method: 'POST',
    body,
  });
  // The response carries the numeric parent_id, but we asked for this
  // parent by uuid — echo it back rather than spending a list round trip
  // to re-derive what we already know.
  return reshapeDocFolderWithParent(folder, parentUuid);
}

// renameDocFolder renames a folder in place; its parent, children and
// documents are all untouched.
export async function renameDocFolder(
  repoPrefix: string,
  uuid: string,
  newName: string,
): Promise<DocFolder> {
  const prefix = requireRepo(repoPrefix, 'rename a document folder');
  // PATCH is a presence map: the key that is PRESENT selects the
  // operation. `name` alone renames; sending `parent_uuid` too is a 400.
  const folder = await call<ApiDocFolder>(
    `/repos/${prefix}/doc-folders/${encodeURIComponent(uuid)}`,
    { method: 'PATCH', body: { name: newName } },
  );
  // A rename preserves the parent, so unlike create/move the caller has no
  // uuid to echo. Resolve it — but only when the folder actually has a
  // parent, so a root-level rename stays one round trip.
  if (folder.parent_id === undefined) {
    return reshapeDocFolderWithParent(folder, '');
  }
  return reshapeDocFolder(folder, folderUuidIndex(await fetchDocFolders(prefix)));
}

// moveDocFolder re-parents a folder and its whole subtree.
// newParentUuid === '' re-roots it. The server refuses a move into the
// folder's own descendant and a move that would breach the depth cap
// (409).
export async function moveDocFolder(
  repoPrefix: string,
  uuid: string,
  newParentUuid: string,
): Promise<DocFolder> {
  const prefix = requireRepo(repoPrefix, 'move a document folder');
  // Present-but-empty parent_uuid is the tree root — the only way to
  // re-root a folder — so this key is always sent, never omitted.
  const folder = await call<ApiDocFolder>(
    `/repos/${prefix}/doc-folders/${encodeURIComponent(uuid)}`,
    { method: 'PATCH', body: { parent_uuid: newParentUuid } },
  );
  return reshapeDocFolderWithParent(folder, newParentUuid);
}

// previewDeleteDocFolder reports what a delete would take with it WITHOUT
// deleting anything — the dry run behind the confirmation dialog.
export async function previewDeleteDocFolder(
  repoPrefix: string,
  uuid: string,
): Promise<DocFolderDeletePreview> {
  const prefix = requireRepo(repoPrefix, 'delete a document folder');
  // Same DELETE route as the real write; `dry_run=true` flips it to a 200
  // with the preview body instead of a 204.
  const preview = await call<ApiDocFolderDeletePreview>(
    `/repos/${prefix}/doc-folders/${encodeURIComponent(uuid)}`,
    { method: 'DELETE', query: { dry_run: true } },
  );
  return reshapeDocFolderDeletePreview(preview);
}

// deleteDocFolder removes a folder for real. Descendant folders go with
// it; every document in that subtree is re-rooted rather than deleted.
export async function deleteDocFolder(repoPrefix: string, uuid: string): Promise<void> {
  const prefix = requireRepo(repoPrefix, 'delete a document folder');
  await call<void>(`/repos/${prefix}/doc-folders/${encodeURIComponent(uuid)}`, {
    method: 'DELETE',
  });
}
