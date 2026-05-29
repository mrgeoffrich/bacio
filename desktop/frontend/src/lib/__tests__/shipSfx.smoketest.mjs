// Smoke tests for the BACI-240 ship-SFX gate in `lib/shipSfxGate.ts`.
// Plain Node + assert, same shape as odometer.smoketest.mjs.
//
// We test the pure decision function rather than the React hook —
// the hook spins up an HTMLAudioElement via `new Audio(url)`, which
// Node doesn't provide; the gate covers every branch the hook's
// imperative path takes before it touches Audio.
//
// Run from the worktree root:
//   node desktop/frontend/src/lib/__tests__/shipSfx.smoketest.mjs

import assert from 'node:assert/strict';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const moduleRoot = path.resolve(__dirname, '..');

const { shouldPlayShipSfx } = await import(path.join(moduleRoot, 'shipSfxGate.ts'));

const tests = [];
function test(name, fn) { tests.push({ name, fn }); }

// Fake Audio constructor used to represent "Audio is reachable".
class FakeAudio {}

test('disabled returns false even when Audio is reachable', () => {
  assert.equal(shouldPlayShipSfx(false, FakeAudio), false);
});

test('enabled + Audio present returns true regardless of reduced motion', () => {
  // BACI-295: prefers-reduced-motion is no longer a gate. The gate
  // signature dropped the param entirely — a user who opted into the
  // ship sound hears it even on a reduced-motion profile (that
  // preference governs animation, not audio).
  assert.equal(shouldPlayShipSfx(true, FakeAudio), true);
});

test('enabled + no Audio constructor returns false (SSR / hardened browser)', () => {
  // The fetch-only desktop launch path or a Node smoketest sees no
  // global Audio. The gate has to refuse cleanly rather than crash.
  assert.equal(shouldPlayShipSfx(true, undefined), false);
});

test('null Audio constructor is treated like undefined', () => {
  // Defensive: a polyfill that explicitly nulls Audio shouldn't
  // wedge the play path.
  assert.equal(shouldPlayShipSfx(true, null), false);
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
