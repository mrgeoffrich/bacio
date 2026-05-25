// Smoke tests for the BACI-193 ship-flourish diff logic in
// `lib/shipFlourish.ts`. Mirrors the existing
// `issueState.smoketest.mjs` pattern — plain Node + assert, no
// Vitest dependency (the frontend has no Vitest stack today). Each
// failing assertion throws; the runner reports the failed test and
// exits non-zero so a build hook can wire this in.
//
// Run from the worktree root:
//   node desktop/frontend/src/lib/__tests__/shipFlourish.smoketest.mjs

import assert from 'node:assert/strict';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const moduleRoot = path.resolve(__dirname, '..');

const { computeShipFlight } = await import(path.join(moduleRoot, 'shipFlourish.ts'));

const tests = [];
function test(name, fn) { tests.push({ name, fn }); }

test('first render (prev === null) never fires a flight, even when done cards exist', () => {
  // The board may already include `done` cards on mount (recently
  // shipped within the 7-day window). The hook seeds its snapshot
  // on first render — it must NOT pretend those existing done
  // cards just shipped.
  const cards = [
    { key: 'TEST-1', column: 'done' },
    { key: 'TEST-2', column: 'in_progress' },
  ];
  assert.equal(computeShipFlight(null, cards), null);
});

test('transition from non-terminal column into done returns the card key', () => {
  // The canonical case: a card moved from in_progress → done
  // between two renders. Hook should pick it up.
  const prev = new Map([
    ['TEST-1', 'in_progress'],
    ['TEST-2', 'todo'],
  ]);
  const next = [
    { key: 'TEST-1', column: 'done' },
    { key: 'TEST-2', column: 'todo' },
  ];
  assert.equal(computeShipFlight(prev, next), 'TEST-1');
});

test('re-render with a card already in done does not re-fire', () => {
  // A subsequent poll arrives and TEST-1 is still in done. Hook
  // must not stamp it again — the flight has already played.
  const prev = new Map([
    ['TEST-1', 'done'],
    ['TEST-2', 'todo'],
  ]);
  const next = [
    { key: 'TEST-1', column: 'done' },
    { key: 'TEST-2', column: 'todo' },
  ];
  assert.equal(computeShipFlight(prev, next), null);
});

test('todo → cancelled does NOT fire (cancelled is out of scope)', () => {
  // The user comment scoped the flourish to done-only. A drag /
  // dispatch that lands in cancelled keeps the existing FLIP move
  // but never flies to the Shipped pill.
  const prev = new Map([['TEST-1', 'todo']]);
  const next = [{ key: 'TEST-1', column: 'cancelled' }];
  assert.equal(computeShipFlight(prev, next), null);
});

test('in_review → done fires (in_review is a non-terminal column)', () => {
  // Common path: a review-mode agent flips its issue to in_review,
  // then the dispatcher / reviewer lands it in done. The diff fires.
  const prev = new Map([['TEST-1', 'in_review']]);
  const next = [{ key: 'TEST-1', column: 'done' }];
  assert.equal(computeShipFlight(prev, next), 'TEST-1');
});

test('cancelled → done does NOT fire (was already terminal)', () => {
  // Pathological: a user reopens a cancelled card and ships it on
  // the same tick. We treat the prior cancelled as terminal so
  // we don't fly. The user can re-trigger by routing through any
  // non-terminal column. Acceptable trade-off — the alternative
  // would surface unwanted flights when board state is unstable.
  const prev = new Map([['TEST-1', 'cancelled']]);
  const next = [{ key: 'TEST-1', column: 'done' }];
  assert.equal(computeShipFlight(prev, next), null);
});

test('a brand-new card landing directly in done (no prev entry) does NOT fire', () => {
  // A card that wasn't in the prior snapshot at all (e.g. an
  // issue created server-side between two polls and immediately
  // closed) has no previous column to compare against. Treat as
  // "first sighting" — don't stamp a flight.
  const prev = new Map([['TEST-1', 'todo']]);
  const next = [
    { key: 'TEST-1', column: 'todo' },
    { key: 'TEST-99', column: 'done' },
  ];
  assert.equal(computeShipFlight(prev, next), null);
});

test('two cards transitioning in the same tick — pick the first found', () => {
  // Rare in practice (each ship is usually its own poll) but the
  // diff must not crash and must return a deterministic key.
  const prev = new Map([
    ['TEST-1', 'in_progress'],
    ['TEST-2', 'in_review'],
  ]);
  const next = [
    { key: 'TEST-1', column: 'done' },
    { key: 'TEST-2', column: 'done' },
  ];
  // Whichever appears first in the next array wins.
  assert.equal(computeShipFlight(prev, next), 'TEST-1');
});

test('a card moving between non-terminal columns does NOT fire', () => {
  // Drag from todo to in_progress is a layout-FLIP move, not a
  // ship. Only the `done` destination triggers the flourish.
  const prev = new Map([['TEST-1', 'todo']]);
  const next = [{ key: 'TEST-1', column: 'in_progress' }];
  assert.equal(computeShipFlight(prev, next), null);
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
