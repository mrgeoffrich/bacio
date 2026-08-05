// Smoke tests for the BACI-240 ship-SFX gate in `lib/shipSfxGate.ts`.
// Plain Node + assert, same shape as odometer.smoketest.mjs.
//
// We test the pure decision function rather than the engine —
// shipSfxEngine.ts builds an AudioContext, which Node doesn't provide;
// the gate covers every branch the engine's play path takes before it
// touches Web Audio. (The engine's own state machine is covered by the
// Vitest suite in shipSfxEngine.test.ts, which fakes the context.)
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

// Fake AudioContext constructor used to represent "Web Audio is reachable".
class FakeAudioContext {}

test('disabled returns false even when AudioContext is reachable', () => {
  assert.equal(shouldPlayShipSfx(false, FakeAudioContext), false);
});

test('enabled + AudioContext present returns true regardless of reduced motion', () => {
  // BACI-295: prefers-reduced-motion is no longer a gate. The gate
  // signature dropped the param entirely — a user who opted into the
  // ship sound hears it even on a reduced-motion profile (that
  // preference governs animation, not audio).
  assert.equal(shouldPlayShipSfx(true, FakeAudioContext), true);
});

test('enabled + no AudioContext constructor returns false (SSR / hardened browser)', () => {
  // A Node smoketest or a hardened profile sees no global AudioContext.
  // The gate has to refuse cleanly rather than crash.
  assert.equal(shouldPlayShipSfx(true, undefined), false);
});

test('null AudioContext constructor is treated like undefined', () => {
  // Defensive: a polyfill that explicitly nulls AudioContext shouldn't
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
