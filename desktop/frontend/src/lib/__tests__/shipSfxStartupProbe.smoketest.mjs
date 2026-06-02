// Smoke tests for the BACI-336 startup-probe gate in
// `lib/shipSfxGate.ts` (shouldProbeShipSfx). Plain Node + assert, same
// shape as shipSfx.smoketest.mjs.
//
// The probe gate decides whether useShipSfx fires its once-only
// volume-0 startup autoplay probe on mount. We test the pure decision
// function rather than the React hook — the hook spins up an
// HTMLAudioElement via `new Audio(url)`, which Node doesn't provide;
// the gate covers every branch the hook's effect takes before it
// touches Audio.
//
// Run from the worktree root:
//   node desktop/frontend/src/lib/__tests__/shipSfxStartupProbe.smoketest.mjs

import assert from 'node:assert/strict';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const moduleRoot = path.resolve(__dirname, '..');

const { shouldProbeShipSfx } = await import(path.join(moduleRoot, 'shipSfxGate.ts'));

const tests = [];
function test(name, fn) { tests.push({ name, fn }); }

// Fake Audio constructor used to represent "Audio is reachable".
class FakeAudio {}

test('enabled + Audio present + not yet probed → fire the probe', () => {
  assert.equal(shouldProbeShipSfx(true, FakeAudio, false), true);
});

test('disabled → skip even with Audio reachable and unprobed', () => {
  // The user opted out of the ship sound — no reason to decode or
  // grant autoplay on their behalf.
  assert.equal(shouldProbeShipSfx(false, FakeAudio, false), false);
});

test('no Audio constructor → skip (SSR / hardened browser)', () => {
  // A Node smoketest or a fetch-only desktop launch sees no global
  // Audio; the gate refuses cleanly rather than crashing.
  assert.equal(shouldProbeShipSfx(true, undefined, false), false);
});

test('null Audio constructor is treated like undefined', () => {
  assert.equal(shouldProbeShipSfx(true, null, false), false);
});

test('already probed → skip even when every other gate passes', () => {
  // The once-only guard: the startup probe (or the first-gesture
  // unlock landing first) already primed the element, so a re-run of
  // the effect must not fire a second probe.
  assert.equal(shouldProbeShipSfx(true, FakeAudio, true), false);
});

test('already probed wins over a disabled toggle (no double-negative surprise)', () => {
  assert.equal(shouldProbeShipSfx(false, FakeAudio, true), false);
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
