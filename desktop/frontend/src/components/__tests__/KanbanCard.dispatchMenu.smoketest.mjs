// Smoke tests for the BACI-252 kanban zap-menu data helper in
// `components/dispatchMenuRows.ts`. Plain Node + assert — mirrors the
// pattern in `dispatchMenuFilter.smoketest.mjs`. The DOM renderer that
// wraps the tree in Radix `DropdownMenu.Item`s is exercised by the
// Playwright manual pass; the flat-primary + compound-matrix *shape*
// is the bit with subtle "each primary becomes a details/summary"
// semantics worth pinning here.
//
// Run from the worktree root:
//   node desktop/frontend/src/components/__tests__/KanbanCard.dispatchMenu.smoketest.mjs

import assert from 'node:assert/strict';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const moduleRoot = path.resolve(__dirname, '..');

const { computeZapMenuTree } = await import(path.join(moduleRoot, 'dispatchMenuRows.ts'));

// Fixture mirrors the built-in prompt set. actionLabel is the
// imperative form rendered in the menu (BACI-67); we include it on a
// couple of rows to exercise the label-fallback in the compound
// matrix's "<primary>, then <follow-on>" composition.
const PROMPTS = [
  { mode: 'design',     label: 'Designing',     actionLabel: 'Design' },
  { mode: 'implement',  label: 'Implementing',  actionLabel: 'Implement' },
  { mode: 'plan',       label: 'Planning',      actionLabel: 'Plan' },
  { mode: 'plan_large', label: 'Planning large', actionLabel: 'Plan large' },
  { mode: 'research',   label: 'Researching',   actionLabel: 'Research' },
  { mode: 'scope',      label: 'Scoping',       actionLabel: 'Scope' },
  { mode: 'review',     label: 'Reviewing',     actionLabel: 'Review' },
  { mode: 'ship',       label: 'Shipping',      actionLabel: 'Ship' },
  { mode: 'fix_review', label: 'Fixing review', actionLabel: 'Fix review' },
];

const ALL_MODES = PROMPTS.map(p => p.mode);

const tests = [];
function test(name, fn) { tests.push({ name, fn }); }

// ---- flat-primary shape ----

test('every visible mode becomes a primary, in input order', () => {
  const got = computeZapMenuTree({
    visible: PROMPTS,
    hasOnDispatchChain: true,
  });
  assert.deepEqual(got.primaries.map(x => x.prompt.mode), ALL_MODES);
});

test('flat list: no bucketing / no per-state filter (any state, any mode)', () => {
  // BACI-252 regression: confirm an `in_progress`-card scenario (where
  // the old promotePrompts would have buried every mode under
  // "unusual") now offers every mode as a top-level primary.
  const got = computeZapMenuTree({
    visible: PROMPTS,
    hasOnDispatchChain: true,
  });
  for (const mode of ALL_MODES) {
    assert.ok(
      got.primaries.some(x => x.prompt.mode === mode),
      `${mode} should be a primary regardless of card state`,
    );
  }
});

// ---- compound matrix shape ----

test('each primary carries a compound matrix of every other visible mode', () => {
  const got = computeZapMenuTree({
    visible: PROMPTS,
    hasOnDispatchChain: true,
  });
  for (const { prompt, compounds } of got.primaries) {
    const expected = PROMPTS.filter(q => q.mode !== prompt.mode).map(q => q.mode);
    assert.deepEqual(
      compounds.map(q => q.mode), expected,
      `compounds for primary ${prompt.mode}`,
    );
    // The primary mode itself never appears in its own compound list
    // (the BACI-209 "Plan, then Plan" pair is suppressed).
    assert.ok(
      !compounds.some(q => q.mode === prompt.mode),
      `${prompt.mode} should not chain to itself`,
    );
  }
});

test('filter narrows compounds too (visible slice drives the matrix)', () => {
  // The shell hands the helper a filter-narrowed visible slice. The
  // compound matrix should follow the same filter so typing "impl"
  // hides every "Plan, then Design"-style sibling that doesn't
  // match.
  const visible = PROMPTS.filter(p => p.mode === 'implement');
  const got = computeZapMenuTree({
    visible,
    hasOnDispatchChain: true,
  });
  assert.deepEqual(got.primaries.map(x => x.prompt.mode), ['implement']);
  // Only `implement` is visible; the compound list is empty because
  // every other mode has been filtered away.
  assert.equal(got.primaries[0].compounds.length, 0);
});

test('onDispatchChain absent: compound matrix collapses to empty', () => {
  // Callers without a chain handler still see the primary rows —
  // they're just dispatch-only with no expander payload.
  const got = computeZapMenuTree({
    visible: PROMPTS,
    hasOnDispatchChain: false,
  });
  assert.equal(got.primaries.length, ALL_MODES.length);
  for (const { compounds } of got.primaries) {
    assert.equal(compounds.length, 0);
  }
});

// ---- defensive-input cases ----

test('null visible slice: zero primaries', () => {
  const got = computeZapMenuTree({
    visible: null,
    hasOnDispatchChain: true,
  });
  assert.equal(got.primaries.length, 0);
});

test('empty visible slice: zero primaries', () => {
  const got = computeZapMenuTree({
    visible: [],
    hasOnDispatchChain: true,
  });
  assert.equal(got.primaries.length, 0);
});

test('input order preserved within primary list', () => {
  const got = computeZapMenuTree({
    visible: PROMPTS,
    hasOnDispatchChain: true,
  });
  assert.deepEqual(got.primaries.map(x => x.prompt.mode), ALL_MODES);
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
