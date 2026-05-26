// Smoke test for the BACI-221 shipped scope picker helpers — the
// `scopeSinceDays` math + persistence default + round-trip. Mirrors
// the pattern in `routes.smoketest.mjs` / `issueState.smoketest.mjs`:
// plain Node + assert, no Vitest dependency.
//
// Run from the worktree root:
//   node desktop/frontend/src/lib/__tests__/shippedScope.smoketest.mjs

import assert from 'node:assert/strict';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const componentsRoot = path.resolve(__dirname, '..', '..', 'components');

const { SHIPPED_SCOPES, scopeSinceDays, scopeLabel, isShippedScope } =
  await import(path.join(componentsRoot, 'shippedScope.ts'));

// localStorage shim — Node doesn't have one. The persistence helper
// only needs getItem / setItem; everything else can stay missing.
const store = new Map();
globalThis.localStorage = {
  getItem(k) { return store.has(k) ? store.get(k) : null; },
  setItem(k, v) { store.set(k, String(v)); },
  removeItem(k) { store.delete(k); },
  clear() { store.clear(); },
};

const persistence = await import(path.join(componentsRoot, 'shippedScopePersistence.ts'));

const tests = [];
function test(name, fn) { tests.push({ name, fn }); }

test('SHIPPED_SCOPES lists the three canonical scopes in picker order', () => {
  assert.deepEqual(SHIPPED_SCOPES, ['today', 'week', 'forever']);
});

test('scopeSinceDays maps to the API sinceDays parameter', () => {
  assert.equal(scopeSinceDays('today'), 1);
  assert.equal(scopeSinceDays('week'), 7);
  // 0 is the "Forever" sentinel both the Wails binding and the HTTP
  // twin honour as "no lower bound".
  assert.equal(scopeSinceDays('forever'), 0);
});

test('scopeLabel surfaces the picker button text', () => {
  assert.equal(scopeLabel('today'), 'Today');
  assert.equal(scopeLabel('week'), 'Last Week');
  assert.equal(scopeLabel('forever'), 'Forever');
});

test('isShippedScope accepts the three canonical strings and rejects the rest', () => {
  assert.equal(isShippedScope('today'), true);
  assert.equal(isShippedScope('week'), true);
  assert.equal(isShippedScope('forever'), true);
  assert.equal(isShippedScope('yesterday'), false);
  assert.equal(isShippedScope(''), false);
  assert.equal(isShippedScope(null), false);
  assert.equal(isShippedScope(undefined), false);
});

test('readShippedScope defaults to week on an empty store', () => {
  store.clear();
  assert.equal(persistence.readShippedScope(), 'week');
});

test('readShippedScope returns a valid stored scope verbatim', () => {
  store.clear();
  persistence.persistShippedScope('today');
  assert.equal(persistence.readShippedScope(), 'today');
  persistence.persistShippedScope('forever');
  assert.equal(persistence.readShippedScope(), 'forever');
});

test('readShippedScope falls back to default on garbage in the store', () => {
  store.clear();
  // Simulate a hand-edited localStorage with a legacy / typo value.
  store.set(persistence.STORAGE_KEY, 'yesterday');
  assert.equal(persistence.readShippedScope(), persistence.DEFAULT_SCOPE);
});

let failed = 0;
for (const { name, fn } of tests) {
  try {
    await fn();
    console.log('ok   ' + name);
  } catch (err) {
    failed++;
    console.error('FAIL ' + name);
    console.error(err);
  }
}
if (failed > 0) {
  console.error(`\n${failed} test(s) failed`);
  process.exit(1);
}
console.log(`\nall ${tests.length} test(s) passed`);
