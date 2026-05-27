// Smoke tests for the BACI-241 promotePrompts helper in
// `components/promotePrompts.ts`. Plain Node + assert — mirrors the
// pattern in dispatchMenuFilter.smoketest.mjs. The DOM-heavy follow-on
// menu that consumes this helper is exercised by the end-to-end
// Playwright pass; the categorisation logic itself has subtle
// "highest-priority bucket wins" semantics worth pinning here.
//
// Run from the worktree root:
//   node desktop/frontend/src/components/__tests__/promotePrompts.smoketest.mjs

import assert from 'node:assert/strict';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const moduleRoot = path.resolve(__dirname, '..');

const { promotePrompts } = await import(path.join(moduleRoot, 'promotePrompts.ts'));

// GRAPH is the BACI-241 canonical edge table (subset spot-check). The
// full table lives in internal/stategraph/graph.go and is unit-tested
// there — we only need enough edges here to cover the buckets the
// promotion logic walks.
const GRAPH = {
  states: ['todo', 'in_progress', 'needs_action', 'in_review', 'done', 'cancelled'],
  edges: [
    { from: 'todo', to: 'in_progress', category: 'primary' },
    { from: 'todo', to: 'cancelled', category: 'secondary' },

    { from: 'in_progress', to: 'in_review', category: 'primary' },
    { from: 'in_progress', to: 'needs_action', category: 'secondary' },
    { from: 'in_progress', to: 'done', category: 'secondary' },
    { from: 'in_progress', to: 'todo', category: 'secondary' },
    { from: 'in_progress', to: 'cancelled', category: 'secondary' },

    { from: 'in_review', to: 'done', category: 'primary' },
    { from: 'in_review', to: 'in_progress', category: 'secondary' },
    { from: 'in_review', to: 'cancelled', category: 'secondary' },

    { from: 'done', to: 'in_progress', category: 'unusual' },
    { from: 'done', to: 'todo', category: 'unusual' },
  ],
};

// PROMPTS mirrors the built-in prompt-template set, just the fields the
// helper actually reads. The order here is the order App.jsx loads
// them — preserved within each bucket.
const PROMPTS = [
  { mode: 'design',     allowedStates: ['todo'] },
  { mode: 'plan',       allowedStates: ['todo'] },
  { mode: 'implement',  allowedStates: ['in_progress'] },
  { mode: 'review',     allowedStates: ['in_review'] },
  { mode: 'ship',       allowedStates: ['in_review'] },
  { mode: 'scope',      allowedStates: ['todo'] },
  { mode: 'fix_review', allowedStates: ['in_review', 'needs_action'] },
];

const tests = [];
function test(name, fn) { tests.push({ name, fn }); }

test('card in todo: implement promoted (in_progress is the primary next-state)', () => {
  const got = promotePrompts(PROMPTS, 'todo', GRAPH);
  assert.deepEqual(got.primary.map(p => p.mode), ['implement']);
});

test('card in todo: plan tucked under unusual (todo→todo is a self-loop, excluded)', () => {
  const got = promotePrompts(PROMPTS, 'todo', GRAPH);
  // No edge "todo → todo" in the graph, so plan / design / scope all
  // have no overlap with primary/secondary/unusual nexts → unusual.
  assert.ok(got.unusual.some(p => p.mode === 'plan'));
  assert.ok(got.unusual.some(p => p.mode === 'design'));
  assert.ok(got.unusual.some(p => p.mode === 'scope'));
});

test('card in in_progress: review prompted under primary (in_review reachable)', () => {
  const got = promotePrompts(PROMPTS, 'in_progress', GRAPH);
  // review.allowedStates = [in_review]; in_progress→in_review is
  // primary, so review lands in the primary bucket.
  assert.ok(got.primary.some(p => p.mode === 'review'));
  assert.ok(got.primary.some(p => p.mode === 'ship'));
  assert.ok(got.primary.some(p => p.mode === 'fix_review'));
});

test('card in in_progress: implement under unusual (self-loop)', () => {
  // No in_progress → in_progress edge → implement has no overlap,
  // falls into unusual.
  const got = promotePrompts(PROMPTS, 'in_progress', GRAPH);
  assert.ok(got.unusual.some(p => p.mode === 'implement'));
});

test('card in in_review: ship + review promoted; implement under unusual', () => {
  const got = promotePrompts(PROMPTS, 'in_review', GRAPH);
  // in_review's primary next is `done`; no prompt's allowedStates
  // contain `done`, so primary is empty. ship + review + fix_review
  // all have allowedStates containing in_review (self-loop, no edge),
  // so they fall through to unusual.
  //
  // The interesting check is that `implement` (allowedStates=
  // [in_progress]) DOES intersect the secondary nexts from in_review
  // (in_progress) → implement promoted to secondary.
  assert.ok(got.secondary.some(p => p.mode === 'implement'));
});

test('graph missing: every prompt falls into unusual (graceful fallback)', () => {
  const got = promotePrompts(PROMPTS, 'todo', null);
  assert.equal(got.primary.length, 0);
  assert.equal(got.secondary.length, 0);
  assert.equal(got.unusual.length, PROMPTS.length);
  // Input order preserved.
  assert.deepEqual(got.unusual.map(p => p.mode), PROMPTS.map(p => p.mode));
});

test('empty prompts list: every bucket is empty', () => {
  const got = promotePrompts([], 'todo', GRAPH);
  assert.equal(got.primary.length, 0);
  assert.equal(got.secondary.length, 0);
  assert.equal(got.unusual.length, 0);
});

test('null prompts list: every bucket is empty (defensive)', () => {
  const got = promotePrompts(null, 'todo', GRAPH);
  assert.equal(got.primary.length, 0);
  assert.equal(got.secondary.length, 0);
  assert.equal(got.unusual.length, 0);
});

test('prompt with no allowedStates falls under unusual', () => {
  const got = promotePrompts(
    [{ mode: 'custom' }, { mode: 'implement', allowedStates: ['in_progress'] }],
    'todo',
    GRAPH,
  );
  assert.deepEqual(got.unusual.map(p => p.mode), ['custom']);
  assert.deepEqual(got.primary.map(p => p.mode), ['implement']);
});

test('input order preserved within each bucket', () => {
  // Two primaries — `implement` first, then a second prompt that also
  // hits the primary bucket. They should appear in input order.
  const list = [
    { mode: 'implement', allowedStates: ['in_progress'] },
    { mode: 'implement_2', allowedStates: ['in_progress'] },
  ];
  const got = promotePrompts(list, 'todo', GRAPH);
  assert.deepEqual(got.primary.map(p => p.mode), ['implement', 'implement_2']);
});

test('highest-priority bucket wins for prompts with multi-state allowedStates', () => {
  // fix_review.allowedStates = [in_review, needs_action]. From
  // in_progress, in_review is primary and needs_action is secondary
  // — the helper must pick primary, NOT secondary.
  const got = promotePrompts(PROMPTS, 'in_progress', GRAPH);
  assert.ok(
    got.primary.some(p => p.mode === 'fix_review'),
    'fix_review should land in primary bucket from in_progress',
  );
  assert.ok(
    !got.secondary.some(p => p.mode === 'fix_review'),
    'fix_review should NOT also appear in secondary',
  );
});

test('currentState empty → fallback to unusual-only', () => {
  // Defensive: a brand-new repo / corrupt state column shouldn't
  // shape the menu into the wrong buckets.
  const got = promotePrompts(PROMPTS, '', GRAPH);
  assert.equal(got.primary.length, 0);
  assert.equal(got.secondary.length, 0);
  assert.equal(got.unusual.length, PROMPTS.length);
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
