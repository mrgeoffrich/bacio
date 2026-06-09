// Document-domain wire types + reshapers (BACI-358).
//
// The snake_case `Api*` shapes mirror model.Document / its document_link
// rows as the `bacio api` server serialises them; reshapeDocSummary maps
// one list row into a DocSummary and reshapeDocContent maps the
// single-doc view into a DocContent. Moved out of api.http.ts so the
// reshapes are unit testable — see ./issue.ts for the pattern + the
// Phase 2b note.

import type { DocSummary, DocContent } from '../contract';

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
}

export interface ApiDocView {
  document: ApiDocument & { content: string };
  links: unknown[];
}

// reshapeDocSummary maps one row of GET /repos/{prefix}/documents into a
// DocSummary, renaming the snake_case fields and the nested link chips.
export function reshapeDocSummary(d: ApiDocument): DocSummary {
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
