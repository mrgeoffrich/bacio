// Smoke tests for the BACI-204 Documents-page persistence helpers in
// `components/DocsPersistence.ts`. Plain Node + assert, same shape as
// boardCompactPersistence.smoketest.mjs.
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
  readHideTranscripts,
  persistHideTranscripts,
  readSort,
  persistSort,
  HIDE_TRANSCRIPTS_KEY,
  SORT_KEY_KEY,
  DEFAULT_HIDE_TRANSCRIPTS,
  DEFAULT_SORT,
} = await import(path.join(moduleRoot, 'DocsPersistence.ts'));

const tests = [];
function test(name, fn) { tests.push({ name, fn }); }

test('storage keys are kebab-cased bacio-prefixed', () => {
  assert.equal(HIDE_TRANSCRIPTS_KEY, 'bacio-docs-hide-transcripts');
  assert.equal(SORT_KEY_KEY, 'bacio-docs-sort');
});

test('readHideTranscripts returns the documented default when unset', () => {
  globalThis.localStorage = makeStorageStub();
  assert.equal(readHideTranscripts('MINI'), DEFAULT_HIDE_TRANSCRIPTS);
});

test('persistHideTranscripts round-trips a non-default value per repo', () => {
  globalThis.localStorage = makeStorageStub();
  persistHideTranscripts('MINI', !DEFAULT_HIDE_TRANSCRIPTS);
  assert.equal(readHideTranscripts('MINI'), !DEFAULT_HIDE_TRANSCRIPTS);
  // Default value back trims the entry so the JSON stays compact.
  persistHideTranscripts('MINI', DEFAULT_HIDE_TRANSCRIPTS);
  const raw = globalThis.localStorage.store.get(HIDE_TRANSCRIPTS_KEY);
  const parsed = raw ? JSON.parse(raw) : {};
  assert.equal('MINI' in parsed, false);
});

test('persistHideTranscripts isolates state across repo prefixes', () => {
  globalThis.localStorage = makeStorageStub();
  persistHideTranscripts('MINI', false);
  persistHideTranscripts('BACI', true);
  assert.equal(readHideTranscripts('MINI'), false);
  assert.equal(readHideTranscripts('BACI'), true);
});

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

test('readHideTranscripts falls back to default when getItem throws', () => {
  globalThis.localStorage = makeStorageStub({ getThrows: true });
  assert.equal(readHideTranscripts('MINI'), DEFAULT_HIDE_TRANSCRIPTS);
});

test('persistHideTranscripts swallows setItem throws without raising', () => {
  globalThis.localStorage = makeStorageStub({ setThrows: true });
  persistHideTranscripts('MINI', !DEFAULT_HIDE_TRANSCRIPTS); // must not throw
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
