// Smoke tests for the BACI-191 Board compact-persistence helpers in
// `components/boardCompactPersistence.ts`. Mirrors the pattern in
// `boardCollapsePersistence.smoketest.mjs` (BACI-188) — plain Node +
// assert, no Vitest dependency. Each failing assertion throws; the
// runner reports the failed test and exits non-zero so a build hook
// can wire this in.
//
// Run from the worktree root:
//   node desktop/frontend/src/components/__tests__/boardCompactPersistence.smoketest.mjs

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

const { readCompact, persistCompact, STORAGE_KEY } =
  await import(path.join(moduleRoot, 'boardCompactPersistence.ts'));

const tests = [];
function test(name, fn) { tests.push({ name, fn }); }

test('STORAGE_KEY is the kebab-cased bacio-prefixed key', () => {
  // Matches the convention from App.jsx (`bacio-theme`,
  // `bacio-active-repo`) and Board.jsx (`bacio-board-scroll`).
  assert.equal(STORAGE_KEY, 'bacio-board-compact-columns');
});

test('readCompact returns an empty Set when the key is missing', () => {
  globalThis.localStorage = makeStorageStub();
  const got = readCompact('MINI');
  assert.ok(got instanceof Set);
  assert.equal(got.size, 0);
});

test('readCompact returns an empty Set when the repo argument is empty', () => {
  globalThis.localStorage = makeStorageStub();
  // Pre-seed something for a real repo so we can confirm the empty-repo
  // call still short-circuits to empty rather than reading the map.
  globalThis.localStorage.store.set(STORAGE_KEY, JSON.stringify({ MINI: ['todo'] }));
  const got = readCompact('');
  assert.equal(got.size, 0);
});

test('persistCompact round-trips a populated Set for one repo', () => {
  globalThis.localStorage = makeStorageStub();
  persistCompact('MINI', new Set(['todo', 'done']));
  // On-disk shape is JSON with arrays (Set is not JSON-serialisable).
  const raw = globalThis.localStorage.store.get(STORAGE_KEY);
  const parsed = JSON.parse(raw);
  assert.deepEqual(parsed.MINI.sort(), ['done', 'todo']);
  // Read returns a Set with the same membership.
  const got = readCompact('MINI');
  assert.equal(got.size, 2);
  assert.ok(got.has('todo'));
  assert.ok(got.has('done'));
});

test('persistCompact isolates state across repo prefixes', () => {
  globalThis.localStorage = makeStorageStub();
  persistCompact('MINI', new Set(['todo']));
  persistCompact('BACI', new Set(['done', 'cancelled']));
  // Reading MINI doesn't leak BACI entries (and vice versa).
  assert.deepEqual(Array.from(readCompact('MINI')).sort(), ['todo']);
  assert.deepEqual(Array.from(readCompact('BACI')).sort(), ['cancelled', 'done']);
});

test('persistCompact(empty Set) clears the repo entry', () => {
  globalThis.localStorage = makeStorageStub();
  persistCompact('MINI', new Set(['todo']));
  persistCompact('MINI', new Set());
  const raw = globalThis.localStorage.store.get(STORAGE_KEY);
  const parsed = JSON.parse(raw);
  // Empty set deletes the per-repo key so the JSON blob doesn't bloat
  // with `{ MINI: [], BACI: [], ... }` over time.
  assert.equal('MINI' in parsed, false);
  assert.equal(readCompact('MINI').size, 0);
});

test('readCompact returns empty when the on-disk JSON is malformed', () => {
  globalThis.localStorage = makeStorageStub();
  globalThis.localStorage.store.set(STORAGE_KEY, 'not-valid-json');
  const got = readCompact('MINI');
  assert.equal(got.size, 0);
});

test('readCompact returns empty when the per-repo value is not an array', () => {
  globalThis.localStorage = makeStorageStub();
  globalThis.localStorage.store.set(STORAGE_KEY, JSON.stringify({ MINI: 'not-an-array' }));
  const got = readCompact('MINI');
  assert.equal(got.size, 0);
});

test('readCompact filters non-string entries out of the array', () => {
  globalThis.localStorage = makeStorageStub();
  globalThis.localStorage.store.set(STORAGE_KEY, JSON.stringify({ MINI: ['todo', 42, null, 'done'] }));
  const got = readCompact('MINI');
  assert.deepEqual(Array.from(got).sort(), ['done', 'todo']);
});

test('readCompact returns empty when getItem throws (hardened storage)', () => {
  globalThis.localStorage = makeStorageStub({ getThrows: true });
  const got = readCompact('MINI');
  assert.equal(got.size, 0);
});

test('persistCompact swallows a setItem throw without raising', () => {
  globalThis.localStorage = makeStorageStub({ setThrows: true });
  // Must not throw; the preference just won't survive the next reload.
  persistCompact('MINI', new Set(['todo']));
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
