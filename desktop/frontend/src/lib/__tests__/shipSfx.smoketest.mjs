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

test('disabled returns false even when every other gate is open', () => {
  assert.equal(shouldPlayShipSfx(false, false, FakeAudio), false);
});

test('enabled + prefers-reduced-motion returns false (accessibility gate)', () => {
  // Reduced motion = user has asked the OS to dial back animation;
  // we extend that to "no surprise SFX either".
  assert.equal(shouldPlayShipSfx(true, true, FakeAudio), false);
});

test('enabled + no Audio constructor returns false (SSR / hardened browser)', () => {
  // The fetch-only desktop launch path or a Node smoketest sees no
  // global Audio. The gate has to refuse cleanly rather than crash.
  assert.equal(shouldPlayShipSfx(true, false, undefined), false);
});

test('every gate open returns true', () => {
  // The happy path: user has opted in, no reduced motion, browser
  // provides Audio. The hook proceeds to lazy-load and play.
  assert.equal(shouldPlayShipSfx(true, false, FakeAudio), true);
});

test('null Audio constructor is treated like undefined', () => {
  // Defensive: a polyfill that explicitly nulls Audio shouldn't
  // wedge the play path.
  assert.equal(shouldPlayShipSfx(true, false, null), false);
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
