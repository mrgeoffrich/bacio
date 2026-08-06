import { describe, expect, it } from 'vitest';
import { pageFilename, seedBody } from '../useDocsActions';

// The two pure helpers behind "New page". They're small, but each encodes a
// rule that only fails at runtime if you get it wrong:
//
//   • pageFilename — filenames stay globally unique per repo and may never
//     contain '/' (folders are purely organisational), so the helper must
//     never invent a path segment.
//   • seedBody — createDoc REJECTS an empty body on the HTTP transport
//     ("content is required", mirroring `bacio doc add`). A blank seed would
//     work on desktop and 400 in the browser, which is exactly the class of
//     bug tsc cannot see.

describe('pageFilename', () => {
  it('appends .md to a bare name', () => {
    expect(pageFilename('sync-across-machines')).toBe('sync-across-machines.md');
  });

  it('leaves an existing extension alone', () => {
    expect(pageFilename('diagram.svg')).toBe('diagram.svg');
    expect(pageFilename('notes.md')).toBe('notes.md');
  });

  it('slugs spaces so a typed title lands as one filename segment', () => {
    expect(pageFilename('Sync design')).toBe('Sync-design.md');
  });

  it('trims surrounding whitespace', () => {
    expect(pageFilename('  draft  ')).toBe('draft.md');
  });

  it('never introduces a path separator', () => {
    expect(pageFilename('plain name')).not.toContain('/');
  });
});

describe('seedBody', () => {
  it('is never empty — createDoc rejects an empty body', () => {
    expect(seedBody('notes.md').length).toBeGreaterThan(0);
  });

  it('opens with a title heading derived from the filename', () => {
    expect(seedBody('sync-across-machines.md')).toBe('# sync across machines\n');
    expect(seedBody('design_notes.md')).toBe('# design notes\n');
  });

  it('falls back to the raw name when there is nothing to strip', () => {
    expect(seedBody('README')).toBe('# README\n');
  });
});
