// Document-domain Wails calls (BACI-359): list / read / save / archive,
// plus the pivot's page lifecycle (create / rename / delete) and the
// doc-folder tree.
import { DocService } from '../../bindings/github.com/mrgeoffrich/bacio/desktop';
import type {
  DocSummary,
  DocContent,
  DocFolder,
  DocFolderDeletePreview,
} from './contract';
import { normalize } from './normalize';

export async function listDocs(repoPrefix: string, typeFilter = ''): Promise<DocSummary[]> {
  try {
    return await DocService.ListDocs(repoPrefix, typeFilter);
  } catch (err) {
    throw normalize(err);
  }
}

export async function getDoc(repoPrefix: string, filename: string): Promise<DocContent> {
  try {
    return await DocService.GetDoc(repoPrefix, filename);
  } catch (err) {
    throw normalize(err);
  }
}

export async function saveDoc(
  repoPrefix: string,
  filename: string,
  content: string,
): Promise<DocContent> {
  try {
    return await DocService.SaveDoc(repoPrefix, filename, content);
  } catch (err) {
    throw normalize(err);
  }
}

// archiveDocument / unarchiveDocument (BACI-204) are the Wails parity
// of the HTTP /documents/{filename}/{archive,unarchive} routes — added
// here so the redesigned Documents page (DocsViewer header strip)
// stays transport-agnostic.
export async function archiveDocument(
  repoPrefix: string,
  filename: string,
): Promise<void> {
  try {
    await DocService.ArchiveDoc(repoPrefix, filename);
  } catch (err) {
    throw normalize(err);
  }
}

export async function unarchiveDocument(
  repoPrefix: string,
  filename: string,
): Promise<void> {
  try {
    await DocService.UnarchiveDoc(repoPrefix, filename);
  } catch (err) {
    throw normalize(err);
  }
}

// ─── Page lifecycle (the pivot) ──────────────────────────────────────

// createDoc creates a page and returns it with its body, ready for the
// editor to open on.
//
// filename must be a single flat segment — document filenames stay
// globally unique per repo and folders are purely organisational, so '/'
// is rejected at the store boundary. docType '' falls back to the default
// user-docs type. folderUuid files the new page under a folder; '' leaves
// it at the tree root.
//
// `content` must be non-empty, and that is enforced HERE rather than left
// to the transport. The two backends genuinely disagree: the HTTP route
// rejects an empty body with 400 "content is required"
// (internal/api/handlers_documents.go's resolveDocCreateInput, deliberately
// mirroring `bacio doc add` — an agent creating a blank document is almost
// always a mistake), while Wails' DocService.CreateDoc allows it because
// store.CreateDocument validates the body with required=false. Without
// this guard "New page" would work on desktop and 400 in the browser, and
// tsc could never catch it. Callers seed a body (a title heading is the
// conventional one).
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
  try {
    return await DocService.CreateDoc(repoPrefix, filename, docType, content, folderUuid);
  } catch (err) {
    throw normalize(err);
  }
}

// renameDoc renames a page in place and returns the refreshed content
// payload under its new name. Folder membership, links and body are
// untouched — a rename is purely the filename.
export async function renameDoc(
  repoPrefix: string,
  filename: string,
  newFilename: string,
): Promise<DocContent> {
  try {
    return await DocService.RenameDoc(repoPrefix, filename, newFilename);
  } catch (err) {
    throw normalize(err);
  }
}

// deleteDoc permanently removes a page and every link pointing at it.
// Unlike archiveDocument this is NOT reversible — confirm with the user
// first.
export async function deleteDoc(repoPrefix: string, filename: string): Promise<void> {
  try {
    await DocService.DeleteDoc(repoPrefix, filename);
  } catch (err) {
    throw normalize(err);
  }
}

// moveDocToFolder files a page under a folder. folderUuid === '' moves it
// back to the tree ROOT — the root is not a folder, so '' is a meaningful
// destination rather than a missing argument.
//
// position is the sort key inside the target folder; pass null to append
// after the folder's current members. It is a sort key, NOT a dense index:
// siblings may share one and the listing tie-breaks on filename.
export async function moveDocToFolder(
  repoPrefix: string,
  filename: string,
  folderUuid: string,
  position: number | null,
): Promise<void> {
  try {
    await DocService.MoveDocToFolder(repoPrefix, filename, folderUuid, position);
  } catch (err) {
    throw normalize(err);
  }
}

// ─── Folder tree (the pivot) ─────────────────────────────────────────

// listDocFolders returns EVERY folder in the repo, not just the roots —
// the docs rail assembles the tree client-side from the flat list, and a
// full list is what makes a display path ("Design/API/Auth") derivable
// without a round trip per level.
export async function listDocFolders(repoPrefix: string): Promise<DocFolder[]> {
  try {
    return await DocService.ListDocFolders(repoPrefix);
  } catch (err) {
    throw normalize(err);
  }
}

// createDocFolder adds a folder. parentUuid === '' creates it at the tree
// root. Sibling names must be unique within one parent; the store rejects
// a duplicate rather than silently de-duplicating.
export async function createDocFolder(
  repoPrefix: string,
  name: string,
  parentUuid: string,
): Promise<DocFolder> {
  try {
    return await DocService.CreateDocFolder(repoPrefix, name, parentUuid);
  } catch (err) {
    throw normalize(err);
  }
}

// renameDocFolder renames a folder in place; its parent, children and
// documents are all untouched.
export async function renameDocFolder(
  repoPrefix: string,
  uuid: string,
  newName: string,
): Promise<DocFolder> {
  try {
    return await DocService.RenameDocFolder(repoPrefix, uuid, newName);
  } catch (err) {
    throw normalize(err);
  }
}

// moveDocFolder re-parents a folder and its whole subtree.
// newParentUuid === '' re-roots it. The store refuses a move into the
// folder's own descendant and a move that would breach the depth cap.
export async function moveDocFolder(
  repoPrefix: string,
  uuid: string,
  newParentUuid: string,
): Promise<DocFolder> {
  try {
    return await DocService.MoveDocFolder(repoPrefix, uuid, newParentUuid);
  } catch (err) {
    throw normalize(err);
  }
}

// previewDeleteDocFolder reports what a delete would take with it WITHOUT
// deleting anything — the dry run behind the confirmation dialog. Pairs
// with deleteDocFolder, which does the real write.
export async function previewDeleteDocFolder(
  repoPrefix: string,
  uuid: string,
): Promise<DocFolderDeletePreview> {
  try {
    return await DocService.PreviewDeleteDocFolder(repoPrefix, uuid);
  } catch (err) {
    throw normalize(err);
  }
}

// deleteDocFolder removes a folder for real. Descendant folders go with
// it; every document in that subtree is re-rooted rather than deleted.
export async function deleteDocFolder(repoPrefix: string, uuid: string): Promise<void> {
  try {
    await DocService.DeleteDocFolder(repoPrefix, uuid);
  } catch (err) {
    throw normalize(err);
  }
}
