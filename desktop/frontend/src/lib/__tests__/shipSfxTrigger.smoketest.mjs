// Smoke tests for the BACI-295 ship-SFX trigger decision — the rule
// that ties the ka-ching to the Shipped odometer rolling up rather
// than the old local cards-array → done diff. Plain Node + assert,
// same shape as odometer.smoketest.mjs / shipSfx.smoketest.mjs.
//
// App.jsx's count-watch effect plays the sound when, and only when,
// `decideOdometerAction(prevCount, nextCount)` returns a `roll` (a
// genuine increase). This file pins that mapping so a future refactor
// of the effect can't silently re-introduce a false ding on first
// load / scope switch / decrement, nor drop the ding on a real ship.
//
// Run from the worktree root:
//   node desktop/frontend/src/lib/__tests__/shipSfxTrigger.smoketest.mjs

import assert from 'node:assert/strict';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const moduleRoot = path.resolve(__dirname, '..');

const { decideOdometerAction } = await import(path.join(moduleRoot, 'odometer.ts'));

// shouldDing mirrors the App.jsx count-watch effect: the sound fires
// exactly when the odometer decides to roll. Kept tiny + local so the
// test asserts the contract, not a re-implementation.
const shouldDing = (prev, next) => decideOdometerAction(prev, next).kind === 'roll';

const tests = [];
function test(name, fn) { tests.push({ name, fn }); }

test('first load (prev null) snaps — no ding', () => {
  // An initial non-zero count on mount must not ding; the ref starts
  // null and the odometer snaps to the value.
  assert.equal(shouldDing(null, 5), false);
});

test('first load (prev undefined) also snaps — no ding', () => {
  // useRef can read undefined before the first effect; treated like null.
  assert.equal(shouldDing(undefined, 5), false);
});

test('unchanged count is a no-op — no ding', () => {
  // The poll re-fetches the same total every 10s; a steady count must
  // stay silent.
  assert.equal(shouldDing(5, 5), false);
});

test('count rising is a genuine ship — dings', () => {
  // The whole point: a card reached done, the server total climbed,
  // the odometer rolls — the ka-ching fires.
  assert.equal(shouldDing(5, 6), true);
});

test('a +N burst still dings once (one roll to the new value)', () => {
  // Two cards land in one tick → +2. The odometer rolls once to the
  // new value, so the sound plays once (BACI-295 intentionally trades
  // the pre-BACI-254 one-ding-per-card behaviour for one-per-roll).
  assert.equal(shouldDing(6, 8), true);
});

test('count falling (scope / repo / archive flip) snaps — no ding', () => {
  // We never roll backwards; a decrement must stay silent.
  assert.equal(shouldDing(6, 4), false);
});

test('scope-change reset to 0 then refill does not ding (ref nulled)', () => {
  // App.jsx nulls prevShippedCountRef alongside setShippedCount(0) on a
  // scope/repo change, so the refill reads as a first-load snap. Model
  // that here: prev null → next 3 → snap, no ding.
  assert.equal(shouldDing(null, 3), false);
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
