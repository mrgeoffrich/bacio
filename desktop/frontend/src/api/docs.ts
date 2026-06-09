// Document-domain Wails calls (BACI-359): list / read / save / archive.
import { DocService } from '../../bindings/github.com/mrgeoffrich/bacio/desktop';
import type { DocSummary, DocContent } from './contract';
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
