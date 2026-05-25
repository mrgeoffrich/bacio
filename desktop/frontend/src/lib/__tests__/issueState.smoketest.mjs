// Smoke tests for the BACI-146 terminal-state helpers in
// `lib/issueState.ts`. Mirrors the pattern in
// `lib/transcript/__tests__/smoketest.mjs` — plain Node + assert, no
// Vitest dependency (no Vitest stack lives in `desktop/frontend`
// today). Each failing assertion throws; the runner reports the
// failed test and exits non-zero so a build hook can wire this in.
//
// Run from the worktree root:
//   node desktop/frontend/src/lib/__tests__/issueState.smoketest.mjs

import assert from 'node:assert/strict';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const moduleRoot = path.resolve(__dirname, '..');

const { isTerminalState, stripBlockerFromCards, restoreBlockedByFromSnapshot, TERMINAL_STATES } =
  await import(path.join(moduleRoot, 'issueState.ts'));

const tests = [];
function test(name, fn) { tests.push({ name, fn }); }

test('TERMINAL_STATES matches the Go boardcards rule', () => {
  // boardcards.IsCompletedColumn returns true for done | cancelled.
  // Keep this list in lockstep with internal/boardcards/cards.go:80.
  assert.deepEqual([...TERMINAL_STATES], ['done', 'cancelled']);
});

test('isTerminalState is true for done / cancelled and false otherwise', () => {
  assert.equal(isTerminalState('done'), true);
  assert.equal(isTerminalState('cancelled'), true);
  assert.equal(isTerminalState('todo'), false);
  assert.equal(isTerminalState('in_progress'), false);
  assert.equal(isTerminalState('needs_action'), false);
  assert.equal(isTerminalState('in_review'), false);
  assert.equal(isTerminalState(''), false);
  assert.equal(isTerminalState(undefined), false);
  assert.equal(isTerminalState(null), false);
});

test('stripBlockerFromCards drops the moved key from every blockedBy entry', () => {
  // The kanban scenario: TEST-1 just landed in Done. Every card that
  // listed TEST-1 in its blockedBy should drop that entry; cards
  // with other blockers keep them.
  const cards = [
    { key: 'TEST-1', column: 'done', blockedBy: [] },
    { key: 'TEST-2', column: 'todo', blockedBy: [{ key: 'TEST-1', state: 'todo' }] },
    { key: 'TEST-3', column: 'todo', blockedBy: [{ key: 'TEST-1', state: 'todo' }, { key: 'TEST-9', state: 'in_progress' }] },
    { key: 'TEST-4', column: 'todo', blockedBy: [{ key: 'TEST-9', state: 'in_progress' }] },
    { key: 'TEST-5', column: 'todo' }, // no blockedBy at all
  ];

  const out = stripBlockerFromCards(cards, 'TEST-1');

  // TEST-2 lost its only blocker.
  assert.deepEqual(out[1].blockedBy, []);
  // TEST-3 lost TEST-1 but kept TEST-9.
  assert.deepEqual(out[2].blockedBy, [{ key: 'TEST-9', state: 'in_progress' }]);
  // TEST-4 untouched — its blockedBy didn't list TEST-1.
  assert.equal(out[3], cards[3], 'TEST-4 should be passed through by reference (memo-friendly)');
  // TEST-5 untouched — no blockedBy property to mutate.
  assert.equal(out[4], cards[4], 'TEST-5 should be passed through by reference (memo-friendly)');
  // TEST-1 itself untouched — moved card has empty blockedBy, not the one being stripped from.
  assert.equal(out[0], cards[0], 'TEST-1 should be passed through by reference (memo-friendly)');
});

test('restoreBlockedByFromSnapshot puts back the pre-move blockedBy on rollback', () => {
  // Simulates the rollback path: setIssueState rejects, so we
  // restore each card we previously stripped from. Snapshot only
  // carries the cards we touched.
  const originalBB2 = [{ key: 'TEST-1', state: 'todo' }];
  const originalBB3 = [{ key: 'TEST-1', state: 'todo' }, { key: 'TEST-9', state: 'in_progress' }];
  const snapshot = new Map([
    ['TEST-2', originalBB2],
    ['TEST-3', originalBB3],
  ]);

  // Post-strip state (what stripBlockerFromCards would have produced).
  const stripped = [
    { key: 'TEST-1', column: 'done', blockedBy: [] },
    { key: 'TEST-2', column: 'todo', blockedBy: [] },
    { key: 'TEST-3', column: 'todo', blockedBy: [{ key: 'TEST-9', state: 'in_progress' }] },
    { key: 'TEST-4', column: 'todo', blockedBy: [{ key: 'TEST-9', state: 'in_progress' }] },
  ];

  const restored = restoreBlockedByFromSnapshot(stripped, snapshot);

  assert.deepEqual(restored[1].blockedBy, originalBB2);
  assert.deepEqual(restored[2].blockedBy, originalBB3);
  // TEST-1 / TEST-4 not in snapshot — passed through by reference.
  assert.equal(restored[0], stripped[0]);
  assert.equal(restored[3], stripped[3]);
});

test('stripBlockerFromCards returns the same array references when nothing matches', () => {
  // No card lists TEST-99 — every entry should pass through by ref
  // so React.memo on KanbanCard skips the unchanged rows.
  const cards = [
    { key: 'TEST-1', column: 'todo', blockedBy: [{ key: 'TEST-7', state: 'todo' }] },
    { key: 'TEST-2', column: 'todo', blockedBy: [] },
  ];
  const out = stripBlockerFromCards(cards, 'TEST-99');
  assert.equal(out[0], cards[0]);
  assert.equal(out[1], cards[1]);
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
