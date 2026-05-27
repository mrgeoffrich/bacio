// Smoke tests for the BACI-231 ActivityTray auto-grouping helper in
// `components/activityTrayGrouping.ts`. Plain Node + assert, no
// Vitest dependency — same pattern as the sibling smoketests.
//
// Run from the worktree root:
//   node desktop/frontend/src/components/__tests__/activityTrayGrouping.smoketest.mjs

import assert from 'node:assert/strict';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const moduleRoot = path.resolve(__dirname, '..');

const { groupRowsByBranch } = await import(path.join(moduleRoot, 'activityTrayGrouping.ts'));

const tests = [];
function test(name, fn) { tests.push({ name, fn }); }

// Helpers — build pinned/entry rows and a cardsByKey map.
const pinned = (key, card) => ({ kind: 'pinned', key, card: { key, ...card } });
const entry = (key, extras = {}) => ({ kind: 'entry', key, entry: { key, ...extras } });
const cards = (...entries) => {
  const m = new Map();
  for (const c of entries) m.set(c.key, c);
  return m;
};

test('flat when every entry resolves to the same branch', () => {
  const rows = [entry('A-1'), entry('A-2')];
  const byKey = cards({ key: 'A-1', featureBranchName: 'feat/auth' }, { key: 'A-2', featureBranchName: 'feat/auth' });
  const got = groupRowsByBranch(rows, byKey);
  assert.equal(got.multiBranch, false);
  assert.equal(got.groups.length, 1);
  assert.equal(got.groups[0].branch, '');
  assert.deepEqual(got.groups[0].rows.map(r => r.key), ['A-1', 'A-2']);
});

test('flat when nothing has a featureBranchName at all', () => {
  const rows = [entry('A-1'), entry('A-2')];
  const byKey = cards({ key: 'A-1' }, { key: 'A-2' });
  const got = groupRowsByBranch(rows, byKey);
  assert.equal(got.multiBranch, false);
  assert.deepEqual(got.groups[0].rows.map(r => r.key), ['A-1', 'A-2']);
});

test('multi-branch: feature branches first (alphabetical), main bucket last', () => {
  const rows = [
    entry('M-1'),
    entry('B-1'),
    entry('A-1'),
    entry('B-2'),
    entry('M-2'),
    entry('A-2'),
  ];
  const byKey = cards(
    { key: 'M-1' /* no branch → main bucket */ },
    { key: 'B-1', featureBranchName: 'feat/bravo' },
    { key: 'A-1', featureBranchName: 'feat/alpha' },
    { key: 'B-2', featureBranchName: 'feat/bravo' },
    { key: 'M-2' /* main */ },
    { key: 'A-2', featureBranchName: 'feat/alpha' },
  );
  const got = groupRowsByBranch(rows, byKey);
  assert.equal(got.multiBranch, true);
  // Three groups: feat/alpha, feat/bravo, '' (main) — in that order.
  assert.deepEqual(got.groups.map(g => g.branch), ['feat/alpha', 'feat/bravo', '']);
  // Per-bucket insertion order preserved.
  assert.deepEqual(got.groups[0].rows.map(r => r.key), ['A-1', 'A-2']);
  assert.deepEqual(got.groups[1].rows.map(r => r.key), ['B-1', 'B-2']);
  assert.deepEqual(got.groups[2].rows.map(r => r.key), ['M-1', 'M-2']);
});

test('multi-branch with no main rows: only feature buckets, no empty group', () => {
  const rows = [entry('A-1'), entry('B-1')];
  const byKey = cards(
    { key: 'A-1', featureBranchName: 'feat/alpha' },
    { key: 'B-1', featureBranchName: 'feat/bravo' },
  );
  const got = groupRowsByBranch(rows, byKey);
  assert.equal(got.multiBranch, true);
  assert.deepEqual(got.groups.map(g => g.branch), ['feat/alpha', 'feat/bravo']);
});

test('pinned rows lead in a headerless block (branch=null)', () => {
  const rows = [
    pinned('P-1', { featureBranchName: 'feat/alpha' }),
    pinned('P-2', { featureBranchName: 'feat/bravo' }),
    entry('A-1'),
    entry('B-1'),
  ];
  const byKey = cards(
    { key: 'P-1', featureBranchName: 'feat/alpha' },
    { key: 'P-2', featureBranchName: 'feat/bravo' },
    { key: 'A-1', featureBranchName: 'feat/alpha' },
    { key: 'B-1', featureBranchName: 'feat/bravo' },
  );
  const got = groupRowsByBranch(rows, byKey);
  assert.equal(got.multiBranch, true);
  // First group is pinned, no header (branch=null), preserves the pinned order.
  assert.equal(got.groups[0].branch, null);
  assert.deepEqual(got.groups[0].rows.map(r => r.key), ['P-1', 'P-2']);
  // Then feature buckets, alphabetical.
  assert.deepEqual(got.groups.slice(1).map(g => g.branch), ['feat/alpha', 'feat/bravo']);
});

test('pinned rows in the single-branch / flat case stay leading', () => {
  // Single feature branch across entries → flat shape. Pinned rows
  // sit at the top of the unified rows list as before.
  const rows = [
    pinned('P-1', {}),
    entry('A-1'),
    entry('A-2'),
  ];
  const byKey = cards(
    { key: 'P-1' },
    { key: 'A-1', featureBranchName: 'feat/alpha' },
    { key: 'A-2', featureBranchName: 'feat/alpha' },
  );
  const got = groupRowsByBranch(rows, byKey);
  assert.equal(got.multiBranch, false);
  assert.deepEqual(got.groups[0].rows.map(r => r.key), ['P-1', 'A-1', 'A-2']);
});

test('missing cardsByKey is tolerated (everything falls into the main bucket)', () => {
  const rows = [entry('A-1'), entry('A-2')];
  const got = groupRowsByBranch(rows, null);
  assert.equal(got.multiBranch, false);
  assert.deepEqual(got.groups[0].rows.map(r => r.key), ['A-1', 'A-2']);
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
