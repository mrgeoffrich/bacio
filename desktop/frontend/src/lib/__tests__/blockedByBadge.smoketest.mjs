// Smoke tests for the BACI-310 blocked-by classifier in
// `lib/blockedByBadge.ts` — the pure decision behind the Pipeline card's
// blocked-by indicator (single key chip vs multi-count popover vs no
// badge). Mirrors the pattern in `issueState.smoketest.mjs` — plain Node
// + assert, no Vitest dependency (no Vitest stack lives in
// `desktop/frontend` today). Each failing assertion throws; the runner
// reports the failed test and exits non-zero so a build hook can wire
// this in.
//
// Run from the worktree root:
//   node desktop/frontend/src/lib/__tests__/blockedByBadge.smoketest.mjs

import assert from 'node:assert/strict';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const moduleRoot = path.resolve(__dirname, '..');

const { blockedByMode } = await import(path.join(moduleRoot, 'blockedByBadge.ts'));

const tests = [];
function test(name, fn) { tests.push({ name, fn }); }

test('absent / empty blockedBy → none', () => {
  // No blockers (the server filters done/cancelled out, so a once-blocked
  // card that is now clear arrives here) → no badge at all.
  assert.equal(blockedByMode(undefined), 'none');
  assert.equal(blockedByMode(null), 'none');
  assert.equal(blockedByMode([]), 'none');
});

test('exactly one open blocker → single', () => {
  // One blocker → render its key directly as a chip; no popover.
  assert.equal(blockedByMode([{ key: 'TEST-1', state: 'todo' }]), 'single');
});

test('two or more open blockers → multi', () => {
  // Two+ blockers → a count chip that opens the popover listing them.
  assert.equal(
    blockedByMode([
      { key: 'TEST-1', state: 'todo' },
      { key: 'TEST-2', state: 'in_progress' },
    ]),
    'multi',
  );
  assert.equal(
    blockedByMode([
      { key: 'TEST-1', state: 'todo' },
      { key: 'TEST-2', state: 'in_progress' },
      { key: 'TEST-3', state: 'in_review' },
    ]),
    'multi',
  );
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
