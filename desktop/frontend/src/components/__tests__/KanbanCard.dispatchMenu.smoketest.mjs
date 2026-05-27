// Smoke tests for the BACI-247 kanban zap-menu data helper in
// `components/dispatchMenuRows.ts`. Plain Node + assert — mirrors the
// pattern in `promotePrompts.smoketest.mjs`. The DOM renderer that
// wraps the tree in Radix `DropdownMenu.Item`s is exercised by the
// Playwright manual pass; the bucketing + compound-matrix *shape* is
// the bit with subtle "primary becomes a details/summary, unusual
// becomes a Show all block" semantics worth pinning here.
//
// Run from the worktree root:
//   node desktop/frontend/src/components/__tests__/KanbanCard.dispatchMenu.smoketest.mjs
//
// Source of truth for PROMPTS: `internal/model/agent.go`
// `builtinPromptStates` map (same fixture as promotePrompts.smoketest).

import assert from 'node:assert/strict';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const moduleRoot = path.resolve(__dirname, '..');

const { computeZapMenuTree } = await import(path.join(moduleRoot, 'dispatchMenuRows.ts'));

// Fixture mirrors promotePrompts.smoketest.mjs's PROMPTS. actionLabel
// is the imperative form rendered in the menu (BACI-67); we include
// it on a couple of rows to exercise the label-fallback in the
// compound matrix's "<primary>, then <follow-on>" composition.
const PROMPTS = [
  { mode: 'design',     label: 'Designing',     actionLabel: 'Design',     allowedStates: ['todo'] },
  { mode: 'implement',  label: 'Implementing',  actionLabel: 'Implement',  allowedStates: ['todo'] },
  { mode: 'plan',       label: 'Planning',      actionLabel: 'Plan',       allowedStates: ['todo'] },
  { mode: 'plan_large', label: 'Planning large', actionLabel: 'Plan large', allowedStates: ['todo'] },
  { mode: 'research',   label: 'Researching',   actionLabel: 'Research',   allowedStates: ['todo'] },
  { mode: 'scope',      label: 'Scoping',       actionLabel: 'Scope',      allowedStates: ['todo'] },
  { mode: 'review',     label: 'Reviewing',     actionLabel: 'Review',     allowedStates: ['in_review'] },
  { mode: 'ship',       label: 'Shipping',      actionLabel: 'Ship',       allowedStates: ['in_review'] },
  { mode: 'fix_review', label: 'Fixing review', actionLabel: 'Fix review', allowedStates: ['in_review'] },
];

const PREP_MODES = ['design', 'implement', 'plan', 'plan_large', 'research', 'scope'];
const REVIEW_MODES = ['review', 'ship', 'fix_review'];

const tests = [];
function test(name, fn) { tests.push({ name, fn }); }

// ---- bucketing shape ----

test('todo card: every prep mode is a primary; review modes land in unusual', () => {
  const got = computeZapMenuTree({
    visible: PROMPTS,
    promptConfig: PROMPTS,
    cardColumn: 'todo',
    hasOnDispatchChain: true,
  });
  assert.deepEqual(got.primaries.map(x => x.prompt.mode), PREP_MODES);
  assert.deepEqual(got.unusual.map(p => p.mode), REVIEW_MODES);
});

test('in_review card: review modes are primary; prep modes in unusual', () => {
  const got = computeZapMenuTree({
    visible: PROMPTS,
    promptConfig: PROMPTS,
    cardColumn: 'in_review',
    hasOnDispatchChain: true,
  });
  assert.deepEqual(got.primaries.map(x => x.prompt.mode), REVIEW_MODES);
  assert.deepEqual(got.unusual.map(p => p.mode), PREP_MODES);
});

test('done card: no primaries; everything under unusual', () => {
  // promotePrompts puts everything in unusual on a done card —
  // mirrors the open-gate behaviour (the zap button stays hidden
  // because primaries.length === 0).
  const got = computeZapMenuTree({
    visible: PROMPTS,
    promptConfig: PROMPTS,
    cardColumn: 'done',
    hasOnDispatchChain: true,
  });
  assert.equal(got.primaries.length, 0);
  assert.deepEqual(got.unusual.map(p => p.mode), PROMPTS.map(p => p.mode));
});

// ---- compound matrix shape ----

test('each primary carries a compound matrix of every other visible mode', () => {
  const got = computeZapMenuTree({
    visible: PROMPTS,
    promptConfig: PROMPTS,
    cardColumn: 'todo',
    hasOnDispatchChain: true,
  });
  // Six primaries on a todo card; each compound list is "every other
  // mode in the full prompt set" — N - 1 entries (8 entries here:
  // the five other prep modes + the three review modes).
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
    promptConfig: PROMPTS,
    cardColumn: 'todo',
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
    promptConfig: PROMPTS,
    cardColumn: 'todo',
    hasOnDispatchChain: false,
  });
  assert.equal(got.primaries.length, PREP_MODES.length);
  for (const { compounds } of got.primaries) {
    assert.equal(compounds.length, 0);
  }
});

// ---- defensive-input cases ----

test('null visible slice: zero primaries, zero unusual', () => {
  const got = computeZapMenuTree({
    visible: null,
    promptConfig: PROMPTS,
    cardColumn: 'todo',
    hasOnDispatchChain: true,
  });
  assert.equal(got.primaries.length, 0);
  assert.equal(got.unusual.length, 0);
});

test('empty cardColumn: primary stays empty (promotePrompts fallback)', () => {
  // Defensive: if a card's state is missing the helper still
  // surfaces the prompts under unusual — the menu's open-gate
  // (primaries.length > 0) then keeps the zap hidden, matching
  // legacy `validPrompts.length > 0` behaviour on a brand-new repo.
  const got = computeZapMenuTree({
    visible: PROMPTS,
    promptConfig: PROMPTS,
    cardColumn: '',
    hasOnDispatchChain: true,
  });
  assert.equal(got.primaries.length, 0);
  assert.equal(got.unusual.length, PROMPTS.length);
});

test('input order preserved within primary bucket', () => {
  // Order within primary mirrors PROMPTS — the helper never re-sorts.
  const got = computeZapMenuTree({
    visible: PROMPTS,
    promptConfig: PROMPTS,
    cardColumn: 'todo',
    hasOnDispatchChain: true,
  });
  assert.deepEqual(got.primaries.map(x => x.prompt.mode), PREP_MODES);
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
