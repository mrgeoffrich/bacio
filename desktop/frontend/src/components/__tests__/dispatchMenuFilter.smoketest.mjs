// Smoke tests for the BACI-222 filterPrompts helper in
// `components/dispatchMenuFilter.ts`. Plain Node + assert, mirrors the
// pattern in `boardCollapsePersistence.smoketest.mjs` — no Vitest /
// jsdom dependency. The component using this helper
// (DispatchMenuContent.jsx) is React + DOM heavy and not directly
// covered here; the filter behaviour is the bit that has subtle
// substring / multi-field semantics worth pinning.
//
// Run from the worktree root:
//   node desktop/frontend/src/components/__tests__/dispatchMenuFilter.smoketest.mjs

import assert from 'node:assert/strict';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const moduleRoot = path.resolve(__dirname, '..');

const { filterPrompts } = await import(path.join(moduleRoot, 'dispatchMenuFilter.ts'));

// A slice of the real promptConfig shape — mode + label + actionLabel
// (BACI-67), matching what App.jsx loads off the API.
const PROMPTS = [
  { mode: 'design',     label: 'Designing',     actionLabel: 'Design' },
  { mode: 'fix_review', label: 'Fixing review', actionLabel: 'Fix review' },
  { mode: 'implement',  label: 'Implementing',  actionLabel: 'Implement' },
  { mode: 'plan',       label: 'Planning',      actionLabel: 'Plan' },
  { mode: 'plan_large', label: 'Planning large', actionLabel: 'Plan large' },
  { mode: 'research',   label: 'Researching',   actionLabel: 'Research' },
  { mode: 'review',     label: 'Reviewing',     actionLabel: 'Review' },
  { mode: 'scope',      label: 'Scoping',       actionLabel: 'Scope' },
  { mode: 'ship',       label: 'Shipping',      actionLabel: 'Ship' },
];

const tests = [];
function test(name, fn) { tests.push({ name, fn }); }

test('empty query returns the input list unchanged', () => {
  const got = filterPrompts(PROMPTS, '');
  assert.equal(got.length, PROMPTS.length);
  assert.deepEqual(got.map(p => p.mode), PROMPTS.map(p => p.mode));
});

test('whitespace-only query is treated as empty', () => {
  const got = filterPrompts(PROMPTS, '   ');
  assert.equal(got.length, PROMPTS.length);
});

test('substring match on actionLabel is case-insensitive', () => {
  // "impl" matches actionLabel "Implement" (and label "Implementing")
  const got = filterPrompts(PROMPTS, 'impl');
  assert.deepEqual(got.map(p => p.mode), ['implement']);
});

test('match against the gerund label (e.g. "planning" -> plan)', () => {
  const got = filterPrompts(PROMPTS, 'planning');
  // Both `plan` (label "Planning") and `plan_large` (label
  // "Planning large") survive.
  assert.deepEqual(got.map(p => p.mode).sort(), ['plan', 'plan_large']);
});

test('match against the raw mode slug (e.g. "fix_review")', () => {
  const got = filterPrompts(PROMPTS, 'fix_review');
  assert.deepEqual(got.map(p => p.mode), ['fix_review']);
});

test('match against the formatted action label "Fix review"', () => {
  const got = filterPrompts(PROMPTS, 'Fix Review');
  assert.deepEqual(got.map(p => p.mode), ['fix_review']);
});

test('no matches returns an empty array (not the input list)', () => {
  const got = filterPrompts(PROMPTS, 'xyzzy');
  assert.equal(got.length, 0);
});

test('match across mode + label + actionLabel — single shared substring', () => {
  // "p" appears in plan, plan_large, ship, scope, implement (mode +
  // labels). design is excluded — no letter 'p' anywhere. A one-
  // letter query is permissive but stable, which is what we want
  // (typing narrows immediately rather than waiting for a long word).
  const got = filterPrompts(PROMPTS, 'p');
  const modes = got.map(p => p.mode).sort();
  assert.deepEqual(modes, ['implement', 'plan', 'plan_large', 'scope', 'ship']);
});

test('null / undefined prompts collection is handled defensively', () => {
  assert.deepEqual(filterPrompts(null, 'plan'), []);
  assert.deepEqual(filterPrompts(undefined, 'plan'), []);
});

test('individual null / undefined entries are skipped', () => {
  const input = [null, undefined, { mode: 'plan', actionLabel: 'Plan' }];
  const got = filterPrompts(input, 'plan');
  assert.deepEqual(got.map(p => p.mode), ['plan']);
});

test('entry missing label/actionLabel still matches by mode', () => {
  const input = [{ mode: 'custom_only' }];
  const got = filterPrompts(input, 'custom');
  assert.deepEqual(got.map(p => p.mode), ['custom_only']);
});

test('return value is a new array (does not alias the input)', () => {
  const got = filterPrompts(PROMPTS, '');
  assert.notStrictEqual(got, PROMPTS);
  // ...but its elements are the same references — we don't deep-clone.
  assert.strictEqual(got[0], PROMPTS[0]);
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
