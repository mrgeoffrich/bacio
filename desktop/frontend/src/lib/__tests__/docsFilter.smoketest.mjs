// Smoke tests for the BACI-204 Documents filter helpers in
// `lib/docsFilter.ts`. Plain Node + assert, no Vitest dependency,
// same shape as boardCompactPersistence.smoketest.mjs.
//
// Run from the worktree root:
//   node desktop/frontend/src/lib/__tests__/docsFilter.smoketest.mjs

import assert from 'node:assert/strict';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const moduleRoot = path.resolve(__dirname, '..');

const {
  filterDocs, sortDocs, countFacets, isTranscriptDoc,
  buildTree, flattenTreeDocs, folderAncestry, findFolderNode,
  shouldFlatten, activeFacetCount,
} = await import(path.join(moduleRoot, 'docsFilter.ts'));

// makeDoc builds a minimal DocSummary-shaped row. updatedAt/createdAt
// default to a deterministic per-index timestamp so sort ties are
// predictable.
function makeDoc(over = {}) {
  const i = over.i ?? 0;
  return {
    filename: over.filename ?? `doc-${i}.md`,
    type: over.type ?? 'plan',
    sizeBytes: over.sizeBytes ?? 100,
    updatedAt: over.updatedAt ?? `2026-05-${String(20 + i).padStart(2, '0')}T00:00:00Z`,
    createdAt: over.createdAt ?? `2026-05-${String(10 + i).padStart(2, '0')}T00:00:00Z`,
    archivedAt: over.archivedAt ?? null,
    snippet: over.snippet ?? '',
    links: over.links ?? [],
    folderUuid: over.folderUuid ?? '',
    folderPosition: over.folderPosition ?? 0,
  };
}

const tests = [];
function test(name, fn) { tests.push({ name, fn }); }

// ---- isTranscriptDoc ----

test('isTranscriptDoc matches type=transcript', () => {
  assert.equal(isTranscriptDoc(makeDoc({ type: 'transcript', filename: 'whatever.md' })), true);
});

test('isTranscriptDoc matches legacy filename even when type is project_complete', () => {
  // Pre-BACI-115 transcripts were stored as project_complete; the
  // filename predicate keeps them out of the browsing surface.
  assert.equal(
    isTranscriptDoc(makeDoc({
      type: 'project_complete',
      filename: 'bacio-transcript-BACI-1-agent-deadbeef.jsonl',
    })),
    true,
  );
});

test('isTranscriptDoc rejects a hand-uploaded .jsonl without the bacio prefix', () => {
  assert.equal(isTranscriptDoc(makeDoc({ type: 'user_docs', filename: 'data.jsonl' })), false);
});

// ---- countFacets ----

test('countFacets returns absolute per-bucket totals', () => {
  const docs = [
    makeDoc({ i: 0, type: 'plan', links: [{ issueKey: 'BACI-1' }] }),
    makeDoc({ i: 1, type: 'plan', links: [] }),
    makeDoc({ i: 2, type: 'designs', links: [{ featureSlug: 'auth' }] }),
    makeDoc({ i: 3, type: 'transcript', filename: 'bacio-transcript-X-agent-abc.jsonl' }),
    makeDoc({ i: 4, type: 'plan', archivedAt: '2026-05-25T00:00:00Z' }),
  ];
  const c = countFacets(docs);
  assert.equal(c.total, 5);
  assert.equal(c.active, 4);
  assert.equal(c.archived, 1);
  assert.equal(c.withIssueLink, 1);
  assert.equal(c.withFeatureLink, 1);
  // i=1 plan, i=3 transcript, i=4 archived plan — three docs with no link rows.
  assert.equal(c.unlinked, 3);
  assert.equal(c.transcript, 1);
  assert.deepEqual(c.byType, { plan: 3, designs: 1, transcript: 1 });
});

// ---- filterDocs: facet behaviours ----

const defaultQuery = {
  search: '',
  type: '',
  links: 'all',
  status: 'active',
  sort: 'updated',
};

test('filterDocs default query returns every active row', () => {
  const docs = [
    makeDoc({ i: 0 }),
    makeDoc({ i: 1 }),
    makeDoc({ i: 2, archivedAt: '2026-05-25T00:00:00Z' }),
  ];
  const r = filterDocs(docs, defaultQuery, false);
  assert.equal(r.visible.length, 2);
  // Counts ignore the in-flight query.
  assert.equal(r.counts.total, 3);
  assert.equal(r.counts.archived, 1);
});

test('filterDocs type facet narrows to exactly the chosen type', () => {
  const docs = [
    makeDoc({ i: 0, type: 'plan' }),
    makeDoc({ i: 1, type: 'designs' }),
    makeDoc({ i: 2, type: 'plan' }),
  ];
  const r = filterDocs(docs, { ...defaultQuery, type: 'plan' }, false);
  assert.equal(r.visible.length, 2);
  assert.deepEqual(r.visible.map(d => d.type), ['plan', 'plan']);
});

test('filterDocs links=issue / feature / unlinked each match the right subset', () => {
  const docs = [
    makeDoc({ i: 0, links: [{ issueKey: 'BACI-1' }] }),
    makeDoc({ i: 1, links: [{ featureSlug: 'auth' }] }),
    makeDoc({ i: 2, links: [] }),
    makeDoc({ i: 3, links: [{ issueKey: 'BACI-2' }, { featureSlug: 'shipping' }] }),
  ];
  assert.equal(filterDocs(docs, { ...defaultQuery, links: 'issue' }, false).visible.length, 2);
  assert.equal(filterDocs(docs, { ...defaultQuery, links: 'feature' }, false).visible.length, 2);
  assert.equal(filterDocs(docs, { ...defaultQuery, links: 'unlinked' }, false).visible.length, 1);
});

test('filterDocs status=active hides archived, =archived shows only archived, =all defers to showArchived', () => {
  const docs = [
    makeDoc({ i: 0 }),
    makeDoc({ i: 1, archivedAt: '2026-05-25T00:00:00Z' }),
  ];
  assert.equal(filterDocs(docs, { ...defaultQuery, status: 'active' }, false).visible.length, 1);
  assert.equal(filterDocs(docs, { ...defaultQuery, status: 'archived' }, false).visible.length, 1);
  // status='all' + showArchived=false still hides archived (parity with the
  // global toggle's read-side default).
  assert.equal(filterDocs(docs, { ...defaultQuery, status: 'all' }, false).visible.length, 1);
  // status='all' + showArchived=true surfaces both rows.
  assert.equal(filterDocs(docs, { ...defaultQuery, status: 'all' }, true).visible.length, 2);
});

test('filterDocs search hits filename, snippet, type, and link targets', () => {
  const docs = [
    makeDoc({ i: 0, filename: 'BACI-204-plan.md' }),
    makeDoc({ i: 1, filename: 'unrelated.md', snippet: 'Discusses BACI-204 in passing' }),
    makeDoc({ i: 2, filename: 'arch.md', links: [{ issueKey: 'BACI-204' }] }),
    makeDoc({ i: 3, filename: 'something.md', type: 'designs' }),
    makeDoc({ i: 4, filename: 'noise.md', snippet: 'nothing relevant' }),
  ];
  const r = filterDocs(docs, { ...defaultQuery, search: 'BACI-204' }, false);
  assert.equal(r.visible.length, 3);
  const rd = filterDocs(docs, { ...defaultQuery, search: 'designs' }, false);
  assert.equal(rd.visible.length, 1);
  assert.equal(rd.visible[0].filename, 'something.md');
});

test('filterDocs default query keeps transcripts in the main list — the rail Type facet is the only transcript filter (BACI-219)', () => {
  const docs = [
    makeDoc({ i: 0, type: 'plan' }),
    makeDoc({
      i: 1,
      type: 'transcript',
      filename: 'bacio-transcript-X-agent-abc.jsonl',
    }),
    makeDoc({
      i: 2,
      type: 'transcript',
      filename: 'bacio-transcript-Y-agent-def.jsonl',
    }),
  ];
  const r = filterDocs(docs, defaultQuery, false);
  assert.equal(r.visible.length, 3);
  // The rail's transcript count still reports the absolute bucket
  // size so the Type chip stays honest.
  assert.equal(r.counts.transcript, 2);
});

test('filterDocs type=plan narrows out the transcripts via the rail tablist', () => {
  const docs = [
    makeDoc({ i: 0, type: 'plan' }),
    makeDoc({
      i: 1,
      type: 'transcript',
      filename: 'bacio-transcript-X-agent-abc.jsonl',
    }),
  ];
  const r = filterDocs(docs, { ...defaultQuery, type: 'plan' }, false);
  assert.equal(r.visible.length, 1);
  assert.equal(r.visible[0].type, 'plan');
});

// ---- sortDocs ----

test('sortDocs Updated is newest-first', () => {
  const docs = [
    makeDoc({ i: 0, updatedAt: '2026-05-20T00:00:00Z', filename: 'a' }),
    makeDoc({ i: 1, updatedAt: '2026-05-25T00:00:00Z', filename: 'b' }),
    makeDoc({ i: 2, updatedAt: '2026-05-22T00:00:00Z', filename: 'c' }),
  ];
  const sorted = sortDocs(docs, 'updated');
  assert.deepEqual(sorted.map(d => d.filename), ['b', 'c', 'a']);
});

test('sortDocs Name is case-insensitive', () => {
  const docs = [
    makeDoc({ i: 0, filename: 'Zoo.md' }),
    makeDoc({ i: 1, filename: 'apple.md' }),
    makeDoc({ i: 2, filename: 'Banana.md' }),
  ];
  const sorted = sortDocs(docs, 'name');
  assert.deepEqual(sorted.map(d => d.filename), ['apple.md', 'Banana.md', 'Zoo.md']);
});

test('sortDocs Size is largest-first', () => {
  const docs = [
    makeDoc({ i: 0, sizeBytes: 100, filename: 'a' }),
    makeDoc({ i: 1, sizeBytes: 500, filename: 'b' }),
    makeDoc({ i: 2, sizeBytes: 50, filename: 'c' }),
  ];
  const sorted = sortDocs(docs, 'size');
  assert.deepEqual(sorted.map(d => d.filename), ['b', 'a', 'c']);
});

test('sortDocs is stable on ties (preserves input order)', () => {
  const ts = '2026-05-25T00:00:00Z';
  const docs = [
    makeDoc({ i: 0, updatedAt: ts, filename: 'x' }),
    makeDoc({ i: 1, updatedAt: ts, filename: 'y' }),
    makeDoc({ i: 2, updatedAt: ts, filename: 'z' }),
  ];
  const sorted = sortDocs(docs, 'updated');
  // All timestamps equal — input order preserved.
  assert.deepEqual(sorted.map(d => d.filename), ['x', 'y', 'z']);
});

// ---- the page tree (the pivot) ----

function makeFolder(over = {}) {
  return {
    uuid: over.uuid ?? 'u',
    name: over.name ?? 'Folder',
    parentUuid: over.parentUuid ?? '',
    position: over.position ?? 0,
  };
}

const names = (nodes) => nodes.map(n => (n.kind === 'folder' ? `+${n.name}` : n.filename));

test('buildTree puts loose pages at the root and nests filed ones', () => {
  const folders = [makeFolder({ uuid: 'arch', name: 'Architecture' })];
  const docs = [
    makeDoc({ i: 0, filename: 'README.md' }),
    makeDoc({ i: 1, filename: 'data-model.md', folderUuid: 'arch' }),
  ];
  const tree = buildTree(docs, folders);
  assert.deepEqual(names(tree), ['+Architecture', 'README.md']);
  assert.deepEqual(names(tree[0].children), ['data-model.md']);
});

test('buildTree sorts folders before pages at every level', () => {
  const folders = [
    makeFolder({ uuid: 'a', name: 'Alpha' }),
    makeFolder({ uuid: 'b', name: 'Beta' }),
  ];
  const docs = [makeDoc({ i: 0, filename: 'aaa.md' })];
  assert.deepEqual(names(buildTree(docs, folders)), ['+Alpha', '+Beta', 'aaa.md']);
});

test('buildTree orders folders by position then name, pages by folderPosition then filename', () => {
  const folders = [
    makeFolder({ uuid: 'z', name: 'Zeta', position: 0 }),
    makeFolder({ uuid: 'a', name: 'Alpha', position: 1 }),
  ];
  const docs = [
    makeDoc({ i: 0, filename: 'b.md', folderPosition: 0 }),
    makeDoc({ i: 1, filename: 'a.md', folderPosition: 0 }),
    makeDoc({ i: 2, filename: 'first.md', folderPosition: -1 }),
  ];
  assert.deepEqual(
    names(buildTree(docs, folders)),
    ['+Zeta', '+Alpha', 'first.md', 'a.md', 'b.md'],
  );
});

test('buildTree docCount counts the whole subtree, not just direct children', () => {
  const folders = [
    makeFolder({ uuid: 'arch', name: 'Architecture' }),
    makeFolder({ uuid: 'store', name: 'Storage', parentUuid: 'arch' }),
  ];
  const docs = [
    makeDoc({ i: 0, filename: 'top.md', folderUuid: 'arch' }),
    makeDoc({ i: 1, filename: 'sqlite.md', folderUuid: 'store' }),
    makeDoc({ i: 2, filename: 'migrations.md', folderUuid: 'store' }),
  ];
  const tree = buildTree(docs, folders);
  assert.equal(tree[0].docCount, 3);
  assert.equal(tree[0].children[0].docCount, 2);
});

test('buildTree re-roots a page filed under an unknown folder rather than dropping it', () => {
  const tree = buildTree([makeDoc({ i: 0, filename: 'orphan.md', folderUuid: 'gone' })], []);
  assert.deepEqual(names(tree), ['orphan.md']);
});

test('buildTree re-roots a folder whose parent is missing', () => {
  const folders = [makeFolder({ uuid: 'child', name: 'Child', parentUuid: 'gone' })];
  assert.deepEqual(names(buildTree([], folders)), ['+Child']);
});

test('buildTree survives a parent cycle without hanging', () => {
  const folders = [
    makeFolder({ uuid: 'a', name: 'A', parentUuid: 'b' }),
    makeFolder({ uuid: 'b', name: 'B', parentUuid: 'a' }),
  ];
  // Both are re-rooted rather than lost; the point is that it terminates.
  assert.deepEqual(names(buildTree([], folders)).sort(), ['+A', '+B']);
});

test('flattenTreeDocs walks in render order, across folder boundaries', () => {
  const folders = [
    makeFolder({ uuid: 'arch', name: 'Architecture', position: 0 }),
    makeFolder({ uuid: 'store', name: 'Storage', parentUuid: 'arch' }),
  ];
  const docs = [
    makeDoc({ i: 0, filename: 'README.md' }),
    makeDoc({ i: 1, filename: 'top.md', folderUuid: 'arch' }),
    makeDoc({ i: 2, filename: 'sqlite.md', folderUuid: 'store' }),
  ];
  const order = flattenTreeDocs(buildTree(docs, folders)).map(d => d.filename);
  assert.deepEqual(order, ['sqlite.md', 'top.md', 'README.md']);
});

test('folderAncestry returns the root→leaf chain', () => {
  const folders = [
    makeFolder({ uuid: 'a', name: 'A' }),
    makeFolder({ uuid: 'b', name: 'B', parentUuid: 'a' }),
    makeFolder({ uuid: 'c', name: 'C', parentUuid: 'b' }),
  ];
  assert.deepEqual(folderAncestry(folders, 'c').map(f => f.name), ['A', 'B', 'C']);
  assert.deepEqual(folderAncestry(folders, ''), []);
  assert.deepEqual(folderAncestry(folders, 'nope'), []);
});

test('folderAncestry terminates on a cycle', () => {
  const folders = [
    makeFolder({ uuid: 'a', name: 'A', parentUuid: 'b' }),
    makeFolder({ uuid: 'b', name: 'B', parentUuid: 'a' }),
  ];
  assert.ok(folderAncestry(folders, 'a').length <= 2);
});

test('findFolderNode locates a nested folder and returns null for a missing one', () => {
  const folders = [
    makeFolder({ uuid: 'arch', name: 'Architecture' }),
    makeFolder({ uuid: 'store', name: 'Storage', parentUuid: 'arch' }),
  ];
  const tree = buildTree([], folders);
  assert.equal(findFolderNode(tree, 'store').name, 'Storage');
  assert.equal(findFolderNode(tree, 'gone'), null);
  assert.equal(findFolderNode(tree, ''), null);
});

// ---- flatten-on-filter ----

const neutralQuery = { search: '', type: '', links: 'all', status: 'active', sort: 'updated' };

test('shouldFlatten is false for the neutral query', () => {
  assert.equal(shouldFlatten(neutralQuery), false);
});

test('shouldFlatten is true once the user narrows in any way', () => {
  assert.equal(shouldFlatten({ ...neutralQuery, search: 'sync' }), true);
  assert.equal(shouldFlatten({ ...neutralQuery, type: 'plan' }), true);
  assert.equal(shouldFlatten({ ...neutralQuery, links: 'issue' }), true);
  assert.equal(shouldFlatten({ ...neutralQuery, status: 'archived' }), true);
});

test('shouldFlatten ignores whitespace-only search and any sort change', () => {
  assert.equal(shouldFlatten({ ...neutralQuery, search: '   ' }), false);
  assert.equal(shouldFlatten({ ...neutralQuery, sort: 'name' }), false);
});

test('activeFacetCount counts the narrowing chips, not the search box', () => {
  assert.equal(activeFacetCount(neutralQuery), 0);
  assert.equal(activeFacetCount({ ...neutralQuery, search: 'x' }), 0);
  assert.equal(activeFacetCount({ ...neutralQuery, type: 'plan', links: 'issue' }), 2);
  assert.equal(activeFacetCount({ ...neutralQuery, type: 'plan', links: 'issue', status: 'all' }), 3);
});

// ---- runner ----
let failed = 0;
for (const t of tests) {
  try {
    await t.fn();
    console.log(`ok  - ${t.name}`);
  } catch (err) {
    failed++;
    console.log(`FAIL - ${t.name}`);
    console.log(err.stack || err.message || String(err));
  }
}
if (failed > 0) {
  console.log(`\n${failed} test(s) failed`);
  process.exit(1);
}
console.log(`\nall ${tests.length} test(s) passed`);
