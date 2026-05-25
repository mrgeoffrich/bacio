// Smoke tests for the BACI-186 ActivityTray collapse-persistence
// helpers in `components/activityTrayPersistence.ts`. Mirrors the
// pattern in `lib/__tests__/issueState.smoketest.mjs` — plain Node +
// assert, no Vitest dependency (no Vitest stack lives in
// `desktop/frontend` today). Each failing assertion throws; the runner
// reports the failed test and exits non-zero so a build hook can wire
// this in.
//
// Run from the worktree root:
//   node desktop/frontend/src/components/__tests__/activityTrayPersistence.smoketest.mjs

import assert from 'node:assert/strict';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const moduleRoot = path.resolve(__dirname, '..');

// makeStorageStub returns a minimal in-memory localStorage stand-in.
// `getThrows` / `setThrows` flip the matching method to throw so the
// hardened-storage paths in the helper can be exercised.
function makeStorageStub({ getThrows = false, setThrows = false } = {}) {
  const store = new Map();
  return {
    store,
    getItem(key) {
      if (getThrows) throw new Error('getItem denied');
      return store.has(key) ? store.get(key) : null;
    },
    setItem(key, value) {
      if (setThrows) throw new Error('setItem denied');
      store.set(key, String(value));
    },
    removeItem(key) { store.delete(key); },
    clear() { store.clear(); },
  };
}

// The helper module reads `globalThis.localStorage` at call time (not
// at import time), so we can install a fresh stub per test.
globalThis.localStorage = makeStorageStub();

const { readCollapsed, persistCollapsed, STORAGE_KEY } =
  await import(path.join(moduleRoot, 'activityTrayPersistence.ts'));

const tests = [];
function test(name, fn) { tests.push({ name, fn }); }

test('STORAGE_KEY is the kebab-cased bacio-prefixed key', () => {
  // Matches the convention from App.jsx (`bacio-theme`,
  // `bacio-active-repo`) and Board.jsx (`bacio-board-scroll`).
  assert.equal(STORAGE_KEY, 'bacio-activity-tray-collapsed');
});

test('readCollapsed returns false when the key is missing', () => {
  globalThis.localStorage = makeStorageStub();
  assert.equal(readCollapsed(), false);
});

test('persistCollapsed(true) then readCollapsed returns true', () => {
  globalThis.localStorage = makeStorageStub();
  persistCollapsed(true);
  assert.equal(globalThis.localStorage.store.get(STORAGE_KEY), '1');
  assert.equal(readCollapsed(), true);
});

test('persistCollapsed(false) then readCollapsed returns false', () => {
  globalThis.localStorage = makeStorageStub();
  persistCollapsed(true);
  persistCollapsed(false);
  assert.equal(globalThis.localStorage.store.get(STORAGE_KEY), '0');
  assert.equal(readCollapsed(), false);
});

test('readCollapsed returns false when getItem throws (hardened storage)', () => {
  globalThis.localStorage = makeStorageStub({ getThrows: true });
  assert.equal(readCollapsed(), false);
});

test('persistCollapsed swallows a setItem throw without raising', () => {
  globalThis.localStorage = makeStorageStub({ setThrows: true });
  // Must not throw; the preference just won't survive the next reload.
  persistCollapsed(true);
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
