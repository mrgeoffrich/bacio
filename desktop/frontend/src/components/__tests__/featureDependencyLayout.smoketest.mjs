// Smoke tests for the BACI-236 computeLayout helper in
// `components/featureGraphLayout.ts`. Plain Node + assert, mirrors the
// pattern in `dispatchMenuFilter.smoketest.mjs` — no Vitest / jsdom
// dependency. The component using this helper
// (FeatureDependencyGraph.jsx) is React + reactflow heavy and not
// directly covered here; the layout maths is the bit worth pinning
// without a DOM.
//
// Run from the worktree root:
//   node desktop/frontend/src/components/__tests__/featureDependencyLayout.smoketest.mjs

import assert from 'node:assert/strict';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const moduleRoot = path.resolve(__dirname, '..');

const { computeLayout, COL_WIDTH, ROW_HEIGHT } = await import(
  path.join(moduleRoot, 'featureGraphLayout.ts')
);

const tests = [];
function test(name, fn) { tests.push({ name, fn }); }

// rankOf is a tiny convenience for asserting "node X is at column N".
// Reads back from the position the layout assigned.
function rankOf(nodes, key) {
  const n = nodes.find(x => x.id === key);
  if (!n) throw new Error(`node ${key} not in layout`);
  return n.position.x / COL_WIDTH;
}

test('empty input yields empty nodes and edges', () => {
  const got = computeLayout([]);
  assert.deepEqual(got.nodes, []);
  assert.deepEqual(got.edges, []);
});

test('null input is tolerated', () => {
  const got = computeLayout(null);
  assert.deepEqual(got.nodes, []);
  assert.deepEqual(got.edges, []);
});

test('single root node sits at rank 0 and is marked ready', () => {
  const got = computeLayout([
    { key: 'MINI-1', title: 'lonely', state: 'todo' },
  ]);
  assert.equal(got.nodes.length, 1);
  assert.equal(got.edges.length, 0);
  assert.equal(got.nodes[0].position.x, 0);
  assert.equal(got.nodes[0].position.y, 0);
  assert.equal(got.nodes[0].data.rank, 0);
  assert.equal(got.nodes[0].data.ready, true);
  assert.equal(got.nodes[0].data.closed, false);
});

test('linear chain a → b → c assigns ranks 0/1/2', () => {
  const got = computeLayout([
    { key: 'MINI-1', title: 'a', state: 'todo' },
    { key: 'MINI-2', title: 'b', state: 'todo', blockedBy: ['MINI-1'] },
    { key: 'MINI-3', title: 'c', state: 'todo', blockedBy: ['MINI-2'] },
  ]);
  assert.equal(rankOf(got.nodes, 'MINI-1'), 0);
  assert.equal(rankOf(got.nodes, 'MINI-2'), 1);
  assert.equal(rankOf(got.nodes, 'MINI-3'), 2);
  // Each edge survives.
  assert.equal(got.edges.length, 2);
  assert.ok(got.edges.find(e => e.source === 'MINI-1' && e.target === 'MINI-2'));
  assert.ok(got.edges.find(e => e.source === 'MINI-2' && e.target === 'MINI-3'));
});

test('fan-out a → {b, c} puts b and c at rank 1', () => {
  const got = computeLayout([
    { key: 'MINI-1', title: 'a', state: 'todo' },
    { key: 'MINI-2', title: 'b', state: 'todo', blockedBy: ['MINI-1'] },
    { key: 'MINI-3', title: 'c', state: 'todo', blockedBy: ['MINI-1'] },
  ]);
  assert.equal(rankOf(got.nodes, 'MINI-1'), 0);
  assert.equal(rankOf(got.nodes, 'MINI-2'), 1);
  assert.equal(rankOf(got.nodes, 'MINI-3'), 1);
  // b and c should stack vertically within the rank-1 column.
  const b = got.nodes.find(n => n.id === 'MINI-2');
  const c = got.nodes.find(n => n.id === 'MINI-3');
  assert.notEqual(b.position.y, c.position.y);
});

test('fan-in {a, b} → c puts c at max(rank(a), rank(b)) + 1', () => {
  // b is itself blocked by a (rank 1), a is rank 0. c is blocked by
  // both, so its rank is max(0, 1) + 1 = 2.
  const got = computeLayout([
    { key: 'MINI-1', title: 'a', state: 'todo' },
    { key: 'MINI-2', title: 'b', state: 'todo', blockedBy: ['MINI-1'] },
    { key: 'MINI-3', title: 'c', state: 'todo', blockedBy: ['MINI-1', 'MINI-2'] },
  ]);
  assert.equal(rankOf(got.nodes, 'MINI-3'), 2);
  // c has two incoming edges.
  const incoming = got.edges.filter(e => e.target === 'MINI-3');
  assert.equal(incoming.length, 2);
});

test('multiple roots all sit at rank 0', () => {
  const got = computeLayout([
    { key: 'MINI-1', title: 'first', state: 'todo' },
    { key: 'MINI-2', title: 'second', state: 'todo' },
    { key: 'MINI-3', title: 'third', state: 'todo' },
  ]);
  for (const k of ['MINI-1', 'MINI-2', 'MINI-3']) {
    assert.equal(rankOf(got.nodes, k), 0);
  }
  // Distinct y-coordinates so the cards don't overlap.
  const ys = got.nodes.map(n => n.position.y);
  assert.equal(new Set(ys).size, ys.length);
});

test('closed node carries closed=true and ready=false', () => {
  const got = computeLayout([
    { key: 'MINI-1', title: 'delivered', state: 'done', closed: true },
  ]);
  assert.equal(got.nodes[0].data.closed, true);
  assert.equal(got.nodes[0].data.ready, false);
});

test('closed blocker edge survives but ready bit reflects "no live blockers"', () => {
  // MINI-1 is delivered, MINI-2 is live and blocked by MINI-1.
  // Per the BACI-236 spec we still draw the edge for context, but
  // MINI-2 should read as "Ready" because its only blocker is closed.
  const got = computeLayout([
    { key: 'MINI-1', title: 'delivered', state: 'done', closed: true },
    { key: 'MINI-2', title: 'live', state: 'todo', blockedBy: ['MINI-1'] },
  ]);
  const m2 = got.nodes.find(n => n.id === 'MINI-2');
  assert.equal(m2.data.closed, false);
  assert.equal(m2.data.ready, true);
  // Edge from MINI-1 → MINI-2 still present.
  const edge = got.edges.find(e => e.source === 'MINI-1' && e.target === 'MINI-2');
  assert.ok(edge, 'closed → open edge dropped from layout');
});

test('open blocker pins ready=false on the blocked node', () => {
  const got = computeLayout([
    { key: 'MINI-1', title: 'in flight', state: 'in_progress' },
    { key: 'MINI-2', title: 'waiting', state: 'todo', blockedBy: ['MINI-1'] },
  ]);
  const m2 = got.nodes.find(n => n.id === 'MINI-2');
  assert.equal(m2.data.ready, false);
});

test('cross-feature blocker key (not in input) is dropped from edges', () => {
  // BACI-236: the server filters out cross-feature blockers, so the
  // graph helper should never see one. But the helper is defensive
  // — if a foreign key shows up in blockedBy, it's silently dropped
  // from the edge set and doesn't contribute to the rank.
  const got = computeLayout([
    { key: 'MINI-1', title: 'lonely with foreign blocker', state: 'todo', blockedBy: ['OTHER-99'] },
  ]);
  assert.equal(got.edges.length, 0);
  assert.equal(rankOf(got.nodes, 'MINI-1'), 0);
  assert.equal(got.nodes[0].data.ready, true);
});

test('row-height and col-width come from the exported constants', () => {
  // Sanity that the spacing constants are reasonable defaults — keep
  // them in step with the CSS .mk-graph-node width and the ReactFlow
  // viewport. If either is dialled to 0 something has gone wrong
  // upstream.
  assert.ok(COL_WIDTH > 0);
  assert.ok(ROW_HEIGHT > 0);
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
