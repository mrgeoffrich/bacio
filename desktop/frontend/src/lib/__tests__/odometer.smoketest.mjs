// Smoke tests for the BACI-240 odometer snap-vs-roll decision in
// `lib/odometer.ts`. Plain Node + assert, mirrors the existing
// shipFlourish.smoketest.mjs pattern (no Vitest dependency).
//
// Run from the worktree root:
//   node desktop/frontend/src/lib/__tests__/odometer.smoketest.mjs

import assert from 'node:assert/strict';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const moduleRoot = path.resolve(__dirname, '..');

const { decideOdometerAction, digitAt } = await import(path.join(moduleRoot, 'odometer.ts'));

const tests = [];
function test(name, fn) { tests.push({ name, fn }); }

// ---- decideOdometerAction ----

test('first mount (prev === null) snaps to the new value', () => {
  // Rolling 0 → 145 on first paint would feel broken — the brief
  // explicitly calls this out as the snap case.
  assert.deepEqual(decideOdometerAction(null, 145), { kind: 'snap', value: 145 });
});

test('first mount (prev === undefined) also snaps', () => {
  // useRef starts as undefined before the first effect runs; the
  // helper has to treat undefined like null.
  assert.deepEqual(decideOdometerAction(undefined, 7), { kind: 'snap', value: 7 });
});

test('equal value is a no-op', () => {
  // The 10s poll re-renders the parent with the same shippedCount;
  // the helper must not schedule a redundant tween.
  assert.deepEqual(decideOdometerAction(42, 42), { kind: 'no-op' });
});

test('decrement snaps (we never roll backwards)', () => {
  // Scope switch from Forever (145) to Today (3) shouldn't make
  // the odometer crawl backwards.
  assert.deepEqual(decideOdometerAction(145, 3), { kind: 'snap', value: 3 });
});

test('upward increment kicks off a roll', () => {
  // The canonical case — a card just shipped, count went 8 → 9.
  assert.deepEqual(decideOdometerAction(8, 9), { kind: 'roll', from: 8, to: 9 });
});

test('larger upward delta still rolls (time-bounded, not delta-bounded)', () => {
  // Per the implementer clarification, we always roll on a genuine
  // upward delta — the duration cap in OdometerNumber keeps a big
  // delta from crawling.
  assert.deepEqual(decideOdometerAction(8, 145), { kind: 'roll', from: 8, to: 145 });
});

test('forceSnap (reduced-motion) collapses an upward delta to a snap', () => {
  // The hook caller passes true when prefers-reduced-motion is on,
  // which short-circuits the RAF tween. Decrement / first-mount
  // paths already snap regardless.
  assert.deepEqual(decideOdometerAction(8, 9, true), { kind: 'snap', value: 9 });
});

test('forceSnap leaves the no-op branch unchanged', () => {
  // Reduced-motion shouldn't transform an equal-value render into
  // an unnecessary snap re-render.
  assert.deepEqual(decideOdometerAction(8, 8, true), { kind: 'no-op' });
});

test('NaN target snaps to zero (defensive)', () => {
  // A bad fetch / API gap could in theory flow NaN through props.
  // The helper has to clamp rather than crash the topbar.
  assert.deepEqual(decideOdometerAction(8, NaN), { kind: 'snap', value: 0 });
});

test('negative target snaps to zero', () => {
  // Similar defence: a -1 from some bogus path shouldn't render
  // as a translateY by a negative digit.
  assert.deepEqual(decideOdometerAction(8, -1), { kind: 'snap', value: 0 });
});

// ---- digitAt ----

test('digitAt(145, 0) === 5 (ones place)', () => {
  assert.equal(digitAt(145, 0), 5);
});
test('digitAt(145, 1) === 4 (tens place)', () => {
  assert.equal(digitAt(145, 1), 4);
});
test('digitAt(145, 2) === 1 (hundreds place)', () => {
  assert.equal(digitAt(145, 2), 1);
});
test('digitAt(7, 2) === 0 (leading-zero pad)', () => {
  // The odometer renders 3 zero-padded digits — the 100s column
  // must be 0 for 7, not throw / NaN.
  assert.equal(digitAt(7, 2), 0);
});
test('digitAt(NaN, 0) === 0 (defensive)', () => {
  assert.equal(digitAt(NaN, 0), 0);
});
test('digitAt(-5, 0) === 0 (defensive)', () => {
  assert.equal(digitAt(-5, 0), 0);
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
