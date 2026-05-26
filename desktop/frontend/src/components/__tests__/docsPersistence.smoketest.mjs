// Smoke tests for the BACI-204 / BACI-219 Documents-page persistence
// helpers in `components/DocsPersistence.ts`. Plain Node + assert,
// same shape as boardCompactPersistence.smoketest.mjs and
// activityTrayPersistence.smoketest.mjs.
//
// Run from the worktree root:
//   node desktop/frontend/src/components/__tests__/docsPersistence.smoketest.mjs

import assert from 'node:assert/strict';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const moduleRoot = path.resolve(__dirname, '..');

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

globalThis.localStorage = makeStorageStub();

const {
  readSort,
  persistSort,
  readSidebarCollapsed,
  persistSidebarCollapsed,
  SORT_KEY_KEY,
  SIDEBAR_COLLAPSED_KEY,
  DEFAULT_SORT,
} = await import(path.join(moduleRoot, 'DocsPersistence.ts'));

const tests = [];
function test(name, fn) { tests.push({ name, fn }); }

test('storage keys are kebab-cased bacio-prefixed', () => {
  assert.equal(SORT_KEY_KEY, 'bacio-docs-sort');
  assert.equal(SIDEBAR_COLLAPSED_KEY, 'bacio-docs-sidebar-collapsed');
});

// ---- sort (per-repo) ----

test('readSort returns the documented default when unset', () => {
  globalThis.localStorage = makeStorageStub();
  assert.equal(readSort('MINI'), DEFAULT_SORT);
});

test('persistSort round-trips a valid sort key per repo', () => {
  globalThis.localStorage = makeStorageStub();
  persistSort('MINI', 'name');
  assert.equal(readSort('MINI'), 'name');
});

test('readSort rejects an unknown sort key on disk and falls back to default', () => {
  globalThis.localStorage = makeStorageStub();
  globalThis.localStorage.store.set(SORT_KEY_KEY, JSON.stringify({ MINI: 'gibberish' }));
  assert.equal(readSort('MINI'), DEFAULT_SORT);
});

test('persistSort isolates state across repo prefixes', () => {
  globalThis.localStorage = makeStorageStub();
  persistSort('MINI', 'name');
  persistSort('BACI', 'size');
  assert.equal(readSort('MINI'), 'name');
  assert.equal(readSort('BACI'), 'size');
});

// ---- sidebarCollapsed (global) ----

test('readSidebarCollapsed returns false when the key is missing', () => {
  globalThis.localStorage = makeStorageStub();
  assert.equal(readSidebarCollapsed(), false);
});

test('persistSidebarCollapsed(true) then readSidebarCollapsed returns true', () => {
  globalThis.localStorage = makeStorageStub();
  persistSidebarCollapsed(true);
  assert.equal(globalThis.localStorage.store.get(SIDEBAR_COLLAPSED_KEY), '1');
  assert.equal(readSidebarCollapsed(), true);
});

test('persistSidebarCollapsed(false) then readSidebarCollapsed returns false', () => {
  globalThis.localStorage = makeStorageStub();
  persistSidebarCollapsed(true);
  persistSidebarCollapsed(false);
  assert.equal(globalThis.localStorage.store.get(SIDEBAR_COLLAPSED_KEY), '0');
  assert.equal(readSidebarCollapsed(), false);
});

test('readSidebarCollapsed returns false when getItem throws (hardened storage)', () => {
  globalThis.localStorage = makeStorageStub({ getThrows: true });
  assert.equal(readSidebarCollapsed(), false);
});

test('persistSidebarCollapsed swallows a setItem throw without raising', () => {
  globalThis.localStorage = makeStorageStub({ setThrows: true });
  // Must not throw; the preference just won't survive the next reload.
  persistSidebarCollapsed(true);
});

// ---- hardened storage on sort ----

test('readSort falls back to default when getItem throws', () => {
  globalThis.localStorage = makeStorageStub({ getThrows: true });
  assert.equal(readSort('MINI'), DEFAULT_SORT);
});

test('persistSort swallows setItem throws without raising', () => {
  globalThis.localStorage = makeStorageStub({ setThrows: true });
  persistSort('MINI', 'name'); // must not throw
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
