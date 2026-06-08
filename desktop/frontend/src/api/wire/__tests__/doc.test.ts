import { describe, it, expect } from 'vitest';
import { reshapeDocSummary, reshapeDocContent } from '../doc';

// BACI-358: document reshapers, testable now they live outside
// api.http.ts. Cover the snake→camel rename and the nested link chips.

describe('reshapeDocSummary', () => {
  it('renames size_bytes / updated_at and maps the link chips', () => {
    const s = reshapeDocSummary({
      filename: 'f.md',
      type: 'plan',
      size_bytes: 12,
      updated_at: 'u',
      created_at: 'c',
      archived_at: 'a',
      snippet: 'sn',
      links: [{ issue_key: 'BACI-1', feature_slug: 'feat', description: 'why' }],
    });
    expect(s.sizeBytes).toBe(12);
    expect(s.updatedAt).toBe('u');
    expect(s.archivedAt).toBe('a');
    expect(s.snippet).toBe('sn');
    expect(s.links).toEqual([{ issueKey: 'BACI-1', featureSlug: 'feat', description: 'why' }]);
  });

  it('leaves links undefined when the row has none', () => {
    const s = reshapeDocSummary({ filename: 'f.md', type: 'note', size_bytes: 0, updated_at: 'u', created_at: 'c' });
    expect(s.links).toBeUndefined();
  });
});

describe('reshapeDocContent', () => {
  it('unwraps the document and defaults empty content', () => {
    const c = reshapeDocContent({
      document: { filename: 'f.md', type: 'plan', size_bytes: 0, updated_at: 'u', created_at: 'c', content: 'body' },
      links: [],
    });
    expect(c).toEqual({ filename: 'f.md', type: 'plan', content: 'body', updatedAt: 'u' });
  });
});
