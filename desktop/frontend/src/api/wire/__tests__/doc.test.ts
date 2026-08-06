import { describe, it, expect } from 'vitest';
import {
  reshapeDocSummary,
  reshapeDocContent,
  reshapeDocFolder,
  reshapeDocFolders,
  reshapeDocFolderWithParent,
  reshapeDocFolderDeletePreview,
  folderUuidIndex,
  resolveFolderUuid,
} from '../doc';
import type { ApiDocFolder } from '../doc';

// BACI-358: document reshapers, testable now they live outside
// api.http.ts. Cover the snake→camel rename and the nested link chips.
//
// The pivot adds the id→uuid folder bridge: the HTTP wire addresses
// folders by the numeric `parent_id` / `folder_id`, the DTO addresses them
// by uuid (the only identity that survives a sync round trip), and these
// reshapers are the only place that translation happens.

// A three-level tree: Design > API > Auth, plus a root-level sibling.
const FOLDERS: ApiDocFolder[] = [
  { id: 1, uuid: 'u-design', repo_id: 7, name: 'Design', position: 0, created_at: 'c1', updated_at: 'u1' },
  { id: 2, uuid: 'u-api', repo_id: 7, parent_id: 1, name: 'API', position: 0, created_at: 'c2', updated_at: 'u2' },
  { id: 3, uuid: 'u-auth', repo_id: 7, parent_id: 2, name: 'Auth', position: 1, created_at: 'c3', updated_at: 'u3' },
  { id: 4, uuid: 'u-ops', repo_id: 7, name: 'Ops', position: 1, created_at: 'c4', updated_at: 'u4' },
];

describe('folderUuidIndex / resolveFolderUuid', () => {
  it('maps every folder id to its uuid', () => {
    const index = folderUuidIndex(FOLDERS);
    expect(index.get(1)).toBe('u-design');
    expect(index.get(3)).toBe('u-auth');
  });

  it("reads an absent id as the tree root ('')", () => {
    // parent_id carries omitempty, so a root folder simply has no key —
    // '' is the value that means root, never a missing field.
    expect(resolveFolderUuid(undefined, folderUuidIndex(FOLDERS))).toBe('');
  });

  it("degrades an unresolvable id to the root rather than throwing", () => {
    expect(resolveFolderUuid(99, folderUuidIndex(FOLDERS))).toBe('');
  });
});

describe('reshapeDocFolders', () => {
  it('resolves parent_id to parentUuid across the whole tree', () => {
    const out = reshapeDocFolders(FOLDERS);
    expect(out.map(f => [f.uuid, f.parentUuid])).toEqual([
      ['u-design', ''],
      ['u-api', 'u-design'],
      ['u-auth', 'u-api'],
      ['u-ops', ''],
    ]);
  });

  it('renames created_at / updated_at and keeps position', () => {
    const auth = reshapeDocFolders(FOLDERS)[2];
    expect(auth).toEqual({
      uuid: 'u-auth',
      name: 'Auth',
      parentUuid: 'u-api',
      position: 1,
      createdAt: 'c3',
      updatedAt: 'u3',
    });
  });

  it('never exposes the numeric id or repo_id', () => {
    const f = reshapeDocFolders(FOLDERS)[0];
    expect(f).not.toHaveProperty('id');
    expect(f).not.toHaveProperty('repo_id');
  });
});

describe('reshapeDocFolder', () => {
  it('takes its index from the caller', () => {
    expect(reshapeDocFolder(FOLDERS[1], folderUuidIndex(FOLDERS)).parentUuid).toBe('u-design');
  });
});

describe('reshapeDocFolderWithParent', () => {
  it('echoes a known parent uuid instead of resolving one', () => {
    // The create / move paths already know the parent — they asked for it
    // by uuid — so they skip the list round trip entirely.
    const f = reshapeDocFolderWithParent(FOLDERS[1], 'u-design');
    expect(f.parentUuid).toBe('u-design');
    expect(f.uuid).toBe('u-api');
  });

  it("carries '' through for a re-root", () => {
    expect(reshapeDocFolderWithParent(FOLDERS[1], '').parentUuid).toBe('');
  });
});

describe('reshapeDocFolderDeletePreview', () => {
  it('flattens the nested cascade counts onto the Wails-shaped DTO', () => {
    expect(reshapeDocFolderDeletePreview({
      folder: FOLDERS[0],
      path: 'Design',
      cascade: { subfolders: 2, documents_re_rooted: 5 },
      would_delete: true,
    })).toEqual({
      uuid: 'u-design',
      name: 'Design',
      path: 'Design',
      subfolders: 2,
      documentsReRooted: 5,
    });
  });
});

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
    }, folderUuidIndex(FOLDERS));
    expect(s.sizeBytes).toBe(12);
    expect(s.updatedAt).toBe('u');
    expect(s.archivedAt).toBe('a');
    expect(s.snippet).toBe('sn');
    expect(s.links).toEqual([{ issueKey: 'BACI-1', featureSlug: 'feat', description: 'why' }]);
  });

  it('leaves links undefined when the row has none', () => {
    const s = reshapeDocSummary(
      { filename: 'f.md', type: 'note', size_bytes: 0, updated_at: 'u', created_at: 'c' },
      folderUuidIndex(FOLDERS),
    );
    expect(s.links).toBeUndefined();
  });

  it('resolves folder_id to folderUuid and carries folder_position', () => {
    const s = reshapeDocSummary(
      { filename: 'auth.md', type: 'note', size_bytes: 1, updated_at: 'u', created_at: 'c', folder_id: 3, folder_position: 4 },
      folderUuidIndex(FOLDERS),
    );
    expect(s.folderUuid).toBe('u-auth');
    expect(s.folderPosition).toBe(4);
  });

  it("files a page with no folder_id at the tree root", () => {
    const s = reshapeDocSummary(
      { filename: 'root.md', type: 'note', size_bytes: 1, updated_at: 'u', created_at: 'c' },
      folderUuidIndex(FOLDERS),
    );
    expect(s.folderUuid).toBe('');
    // A pre-pivot server omits folder_position entirely; 0 is the same
    // "unset" sort key every migrated row carries.
    expect(s.folderPosition).toBe(0);
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
