// Smoke tests for the BACI-241 / BACI-245 promotePrompts helper in
// `components/promotePrompts.ts`. Plain Node + assert — mirrors the
// pattern in dispatchMenuFilter.smoketest.mjs. The DOM-heavy follow-on
// menu that consumes this helper is exercised by the end-to-end
// Playwright pass; the categorisation logic itself has subtle
// "valid-from-current-state" semantics worth pinning here.
//
// Run from the worktree root:
//   node desktop/frontend/src/components/__tests__/promotePrompts.smoketest.mjs
//
// Source of truth for PROMPTS: `internal/model/agent.go`
// `builtinPromptStates` map. Update the fixture when that map changes
// — there is no auto-sync today. See BACI-245 plan ("Risks") for
// follow-up options if the drift starts biting.

import assert from 'node:assert/strict';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const moduleRoot = path.resolve(__dirname, '..');

const { promotePrompts } = await import(path.join(moduleRoot, 'promotePrompts.ts'));

// GRAPH is kept for API stability — promotePrompts ignores it under
// the BACI-245 semantic. We pass it through anyway so a future
// repurpose (e.g. ordering primary by destination distance) lights up
// the test surface, and so the tests stay close to how the caller
// actually invokes the helper.
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

// PROMPTS mirrors `builtinPromptStates` in internal/model/agent.go —
// the prep modes (scope / research / plan / plan_large / design /
// implement) are valid from `todo`; review / ship / fix_review are
// valid from `in_review`. The order here is the order App.jsx loads
// them (alphabetical within each bucket) — preserved within each
// bucket by the helper.
const PROMPTS = [
  { mode: 'design',     allowedStates: ['todo'] },
  { mode: 'implement',  allowedStates: ['todo'] },
  { mode: 'plan',       allowedStates: ['todo'] },
  { mode: 'plan_large', allowedStates: ['todo'] },
  { mode: 'research',   allowedStates: ['todo'] },
  { mode: 'scope',      allowedStates: ['todo'] },
  { mode: 'review',     allowedStates: ['in_review'] },
  { mode: 'ship',       allowedStates: ['in_review'] },
  { mode: 'fix_review', allowedStates: ['in_review'] },
];

const PREP_MODES = ['design', 'implement', 'plan', 'plan_large', 'research', 'scope'];
const REVIEW_MODES = ['review', 'ship', 'fix_review'];

const tests = [];
function test(name, fn) { tests.push({ name, fn }); }

// ---- per-state buckets — one assertion per state in graph.yaml ----

test('card in todo: every prep mode is in primary; review modes in unusual', () => {
  const got = promotePrompts(PROMPTS, 'todo', GRAPH);
  assert.deepEqual(got.primary.map(p => p.mode), PREP_MODES);
  assert.deepEqual(got.unusual.map(p => p.mode), REVIEW_MODES);
  assert.equal(got.secondary.length, 0);
});

test('card in in_progress: primary empty; everything under unusual', () => {
  // No built-in prompt admits `in_progress` in its allowedStates, so
  // primary is empty and the whole prompt list lands in unusual.
  const got = promotePrompts(PROMPTS, 'in_progress', GRAPH);
  assert.equal(got.primary.length, 0);
  assert.equal(got.secondary.length, 0);
  assert.deepEqual(got.unusual.map(p => p.mode), PROMPTS.map(p => p.mode));
});

test('card in needs_action: primary empty; everything under unusual', () => {
  const got = promotePrompts(PROMPTS, 'needs_action', GRAPH);
  assert.equal(got.primary.length, 0);
  assert.equal(got.secondary.length, 0);
  assert.deepEqual(got.unusual.map(p => p.mode), PROMPTS.map(p => p.mode));
});

test('card in in_review: review/ship/fix_review in primary; prep modes in unusual', () => {
  const got = promotePrompts(PROMPTS, 'in_review', GRAPH);
  assert.deepEqual(got.primary.map(p => p.mode), REVIEW_MODES);
  assert.deepEqual(got.unusual.map(p => p.mode), PREP_MODES);
  assert.equal(got.secondary.length, 0);
});

test('card in done: primary empty; everything under unusual', () => {
  const got = promotePrompts(PROMPTS, 'done', GRAPH);
  assert.equal(got.primary.length, 0);
  assert.equal(got.secondary.length, 0);
  assert.deepEqual(got.unusual.map(p => p.mode), PROMPTS.map(p => p.mode));
});

test('card in cancelled: primary empty; everything under unusual', () => {
  const got = promotePrompts(PROMPTS, 'cancelled', GRAPH);
  assert.equal(got.primary.length, 0);
  assert.equal(got.secondary.length, 0);
  assert.deepEqual(got.unusual.map(p => p.mode), PROMPTS.map(p => p.mode));
});

// ---- BACI-245 regression: prep modes never hide on todo ----

test('BACI-245 regression: no prep mode is hidden under unusual on a todo card', () => {
  const got = promotePrompts(PROMPTS, 'todo', GRAPH);
  for (const mode of PREP_MODES) {
    assert.ok(
      got.primary.some(p => p.mode === mode),
      `${mode} should be in primary on a todo card, not behind Show all`,
    );
    assert.ok(
      !got.unusual.some(p => p.mode === mode),
      `${mode} should NOT be in unusual on a todo card`,
    );
  }
});

// ---- defensive-input cases ----

test('empty prompts list: every bucket is empty', () => {
  const got = promotePrompts([], 'todo', GRAPH);
  assert.equal(got.primary.length, 0);
  assert.equal(got.secondary.length, 0);
  assert.equal(got.unusual.length, 0);
});

test('null prompts list: every bucket is empty', () => {
  const got = promotePrompts(null, 'todo', GRAPH);
  assert.equal(got.primary.length, 0);
  assert.equal(got.secondary.length, 0);
  assert.equal(got.unusual.length, 0);
});

test('prompt with no allowedStates falls under unusual', () => {
  const got = promotePrompts(
    [{ mode: 'custom' }, { mode: 'implement', allowedStates: ['todo'] }],
    'todo',
    GRAPH,
  );
  assert.deepEqual(got.unusual.map(p => p.mode), ['custom']);
  assert.deepEqual(got.primary.map(p => p.mode), ['implement']);
});

test('input order preserved within each bucket', () => {
  // Two primaries for the same state, in input order.
  const list = [
    { mode: 'implement', allowedStates: ['todo'] },
    { mode: 'implement_2', allowedStates: ['todo'] },
  ];
  const got = promotePrompts(list, 'todo', GRAPH);
  assert.deepEqual(got.primary.map(p => p.mode), ['implement', 'implement_2']);
});

test('multi-state allowedStates: prompt promoted whenever currentState matches any', () => {
  // fix_review with allowedStates [in_review, needs_action] — admits
  // both. From either state it lands in primary.
  const list = [{ mode: 'fix_review', allowedStates: ['in_review', 'needs_action'] }];
  for (const state of ['in_review', 'needs_action']) {
    const got = promotePrompts(list, state, GRAPH);
    assert.deepEqual(
      got.primary.map(p => p.mode), ['fix_review'],
      `fix_review should land in primary from ${state}`,
    );
  }
  // From an unrelated state, it falls to unusual.
  const got = promotePrompts(list, 'todo', GRAPH);
  assert.deepEqual(got.unusual.map(p => p.mode), ['fix_review']);
  assert.equal(got.primary.length, 0);
});

test('currentState empty → fallback to unusual-only', () => {
  // Defensive: a brand-new repo / corrupt state column shouldn't
  // shape the menu into the wrong buckets.
  const got = promotePrompts(PROMPTS, '', GRAPH);
  assert.equal(got.primary.length, 0);
  assert.equal(got.secondary.length, 0);
  assert.equal(got.unusual.length, PROMPTS.length);
});

test('graph absent: still buckets by currentState (graph param is unused)', () => {
  // Under the BACI-245 semantic the graph is no longer consulted, so
  // passing null doesn't degrade the categorisation — primary still
  // contains the prep modes for a todo card.
  const got = promotePrompts(PROMPTS, 'todo', null);
  assert.deepEqual(got.primary.map(p => p.mode), PREP_MODES);
  assert.deepEqual(got.unusual.map(p => p.mode), REVIEW_MODES);
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
