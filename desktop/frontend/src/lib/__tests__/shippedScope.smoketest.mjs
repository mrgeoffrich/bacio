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

const {
  SHIPPED_SCOPES,
  scopeSinceDays,
  scopeSinceParams,
  scopeLabel,
  isShippedScope,
  SHIPPED_REPO_SCOPES,
  isShippedRepoScope,
  repoScopeLabel,
  shippedPrefix,
} = await import(path.join(componentsRoot, 'shippedScope.ts'));

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

test('scopeSinceParams: week/forever keep the relative-window path', () => {
  // BACI-312: only `today` graduated to an absolute cutoff; week/forever
  // still carry sinceDays and an empty sinceTs.
  assert.deepEqual(scopeSinceParams('week', 'Australia/Sydney'), { sinceDays: 7, sinceTs: '' });
  assert.deepEqual(scopeSinceParams('forever', 'Australia/Sydney'), { sinceDays: 0, sinceTs: '' });
});

test('scopeSinceParams: today returns local-midnight-in-tz as an absolute RFC3339 cutoff', () => {
  const { sinceDays, sinceTs } = scopeSinceParams('today', 'Australia/Sydney');
  assert.equal(sinceDays, 0, 'today must not carry a relative window');
  assert.ok(sinceTs, 'today must carry an absolute sinceTs');
  // The cutoff, rendered back in the same zone, must read as 00:00:00 on
  // the zone's current calendar day — i.e. it IS local midnight.
  const wall = new Intl.DateTimeFormat('en-CA', {
    timeZone: 'Australia/Sydney',
    hourCycle: 'h23',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  }).format(new Date(sinceTs));
  assert.equal(wall, '00:00:00', `sinceTs should be local midnight in Sydney, got ${wall}`);
});

test('scopeSinceParams: UTC midnight round-trips to 00:00:00Z', () => {
  const { sinceTs } = scopeSinceParams('today', 'UTC');
  assert.ok(sinceTs.endsWith('T00:00:00.000Z') || sinceTs.endsWith('T00:00:00Z'),
    `UTC midnight should end the day at 00:00:00Z, got ${sinceTs}`);
});

test('scopeSinceParams: empty tz falls back to the host zone without throwing', () => {
  // The setting is unset before auto-detect fills it — "Today" must still
  // resolve to *a* midnight rather than crashing.
  const { sinceDays, sinceTs } = scopeSinceParams('today', '');
  assert.equal(sinceDays, 0);
  assert.ok(sinceTs, 'empty tz must still yield an absolute cutoff');
  assert.doesNotThrow(() => new Date(sinceTs).toISOString());
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

// ── BACI-371 repo scope ────────────────────────────────────────────────

test('SHIPPED_REPO_SCOPES lists the two repo scopes, all-repos first', () => {
  assert.deepEqual(SHIPPED_REPO_SCOPES, ['all', 'repo']);
});

test('isShippedRepoScope accepts the two canonical strings and rejects the rest', () => {
  assert.equal(isShippedRepoScope('all'), true);
  assert.equal(isShippedRepoScope('repo'), true);
  assert.equal(isShippedRepoScope('current'), false);
  assert.equal(isShippedRepoScope(''), false);
  assert.equal(isShippedRepoScope(null), false);
  assert.equal(isShippedRepoScope(undefined), false);
});

test('repoScopeLabel wears the active prefix on the narrowing button', () => {
  assert.equal(repoScopeLabel('all', 'BACI'), 'All repos');
  assert.equal(repoScopeLabel('repo', 'BACI'), 'BACI');
  // With no board open there is no prefix to show — the button is
  // disabled anyway, so the generic label is the fallback.
  assert.equal(repoScopeLabel('repo', ''), 'This repo');
});

test('shippedPrefix resolves the (repoScope, activeBoard) pair', () => {
  assert.equal(shippedPrefix('all', 'BACI'), 'all');
  assert.equal(shippedPrefix('repo', 'BACI'), 'BACI');
  // No board to narrow to → collapse back to the cross-repo sentinel so
  // the pill still counts something on a prefix-less URL.
  assert.equal(shippedPrefix('repo', ''), 'all');
  assert.equal(shippedPrefix('repo', 'all'), 'all');
});

test('readShippedRepoScope defaults to all on an empty store', () => {
  store.clear();
  assert.equal(persistence.readShippedRepoScope(), 'all');
  assert.equal(persistence.DEFAULT_REPO_SCOPE, 'all');
});

test('readShippedRepoScope round-trips a stored scope', () => {
  store.clear();
  persistence.persistShippedRepoScope('repo');
  assert.equal(persistence.readShippedRepoScope(), 'repo');
  persistence.persistShippedRepoScope('all');
  assert.equal(persistence.readShippedRepoScope(), 'all');
});

test('readShippedRepoScope falls back to default on garbage in the store', () => {
  store.clear();
  store.set(persistence.REPO_SCOPE_STORAGE_KEY, 'current');
  assert.equal(persistence.readShippedRepoScope(), persistence.DEFAULT_REPO_SCOPE);
});

test('the two scope axes persist on separate keys', () => {
  store.clear();
  persistence.persistShippedScope('today');
  persistence.persistShippedRepoScope('repo');
  assert.equal(persistence.readShippedScope(), 'today');
  assert.equal(persistence.readShippedRepoScope(), 'repo');
  assert.notEqual(persistence.STORAGE_KEY, persistence.REPO_SCOPE_STORAGE_KEY);
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
