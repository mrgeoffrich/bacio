// Docs-domain HTTP transport (BACI-359). Fetch wrappers + reshapers over
// the `bacio api` REST surface; the public ./api surface is the same as the
// Wails seam's. See ./client.http for the shared plumbing.
import { call } from './client.http';
import { reshapeDocSummary, reshapeDocContent } from './wire/doc';
import type { ApiDocument, ApiDocView } from './wire/doc';
import type { DocSummary, DocContent } from './contract';

// ApiDocumentLink is the raw wire shape for one document_link row
// returned alongside a doc in the list response. Field naming follows
// the Go-side snake_case the API serialises (links carry the issue
// key / feature slug pre-formatted, so the client doesn't need to
// resolve them).
export async function listDocs(repoPrefix: string, typeFilter = ''): Promise<DocSummary[]> {
  if (!repoPrefix || repoPrefix === 'all') {
    throw new Error('select a repository to view its documents');
  }
  const docs = await call<ApiDocument[]>(`/repos/${repoPrefix}/documents`, {
    query: { type: typeFilter || undefined },
  });
  return docs.map(reshapeDocSummary);
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
