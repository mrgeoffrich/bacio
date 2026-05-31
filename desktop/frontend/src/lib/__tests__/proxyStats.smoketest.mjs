// Smoke test for the BACI-304 Monitor proxy-stats helpers in
// `lib/proxyStats.ts` — the byte / error-rate / ms formatters and the
// scope → sinceDays mapping. Mirrors the pattern in
// `shippedScope.smoketest.mjs` / `routes.smoketest.mjs` — plain Node +
// assert, no Vitest dependency.
//
// Run from the worktree root:
//   node desktop/frontend/src/lib/__tests__/proxyStats.smoketest.mjs

import assert from 'node:assert/strict';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const moduleRoot = path.resolve(__dirname, '..');

const { MONITOR_SCOPES, scopeToSinceDays, formatBytes, formatErrorRate, formatMs } =
  await import(path.join(moduleRoot, 'proxyStats.ts'));

const tests = [];
function test(name, fn) { tests.push({ name, fn }); }

test('formatBytes renders B / KB / MB with one decimal above KB', () => {
  assert.equal(formatBytes(0), '0 B');
  assert.equal(formatBytes(512), '512 B');
  assert.equal(formatBytes(1024), '1.0 KB');
  assert.equal(formatBytes(1536), '1.5 KB');
  assert.equal(formatBytes(1024 * 1024), '1.0 MB');
  assert.equal(formatBytes(3.5 * 1024 * 1024), '3.5 MB');
  // Non-finite / negative → em dash, never NaN.
  assert.equal(formatBytes(-1), '—');
  assert.equal(formatBytes(NaN), '—');
});

test('formatErrorRate renders a fraction as a whole-number percentage', () => {
  assert.equal(formatErrorRate(0), '0%');
  assert.equal(formatErrorRate(0.5), '50%');
  assert.equal(formatErrorRate(1), '100%');
  assert.equal(formatErrorRate(1 / 3), '33%');
  assert.equal(formatErrorRate(-1), '—');
});

test('formatMs keeps sub-second in ms, seconds above 1000ms', () => {
  assert.equal(formatMs(0), '0 ms');
  assert.equal(formatMs(250), '250 ms');
  assert.equal(formatMs(999), '999 ms');
  assert.equal(formatMs(1000), '1.0 s');
  assert.equal(formatMs(2500), '2.5 s');
  assert.equal(formatMs(-5), '—');
});

test('scopeToSinceDays maps the three Monitor scopes; All-time is the 0 sentinel', () => {
  assert.equal(scopeToSinceDays('day'), 1);
  assert.equal(scopeToSinceDays('week'), 7);
  assert.equal(scopeToSinceDays('all'), 0);
  // An unknown / hand-edited id falls back to All-time (0), never throws.
  assert.equal(scopeToSinceDays('bogus'), 0);
});

test('MONITOR_SCOPES is the ordered Last 24h / Last 7d / All-time list', () => {
  assert.deepEqual(MONITOR_SCOPES.map(s => s.id), ['day', 'week', 'all']);
  assert.deepEqual(MONITOR_SCOPES.map(s => s.label), ['Last 24h', 'Last 7d', 'All-time']);
  assert.deepEqual(MONITOR_SCOPES.map(s => s.sinceDays), [1, 7, 0]);
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
