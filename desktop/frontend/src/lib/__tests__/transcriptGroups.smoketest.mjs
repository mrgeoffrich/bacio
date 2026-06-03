// Smoke test for the BACI-348 Transcripts "Group by" fold in
// `lib/transcriptGroups.ts` — folding the flat per-dispatch rows into session
// / agent groups. Mirrors the pattern in `proxyStats.smoketest.mjs` /
// `docsFilter.smoketest.mjs` — plain Node + assert, no Vitest dependency.
//
// Run from the worktree root:
//   node desktop/frontend/src/lib/__tests__/transcriptGroups.smoketest.mjs

import assert from 'node:assert/strict';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const moduleRoot = path.resolve(__dirname, '..');

const { groupTranscripts, EMPTY_AGENT_KEY, EMPTY_AGENT_LABEL } =
  await import(path.join(moduleRoot, 'transcriptGroups.ts'));

const tests = [];
function test(name, fn) { tests.push({ name, fn }); }

// A minimal row factory — only the fields the fold reads.
function row(overrides) {
  return {
    dispatchId: 0,
    turnCount: 0,
    usage: { inputTokens: 0, outputTokens: 0 },
    lastSeen: '2026-06-03T00:00:00Z',
    ...overrides,
  };
}

test('dispatch mode is a passthrough sentinel — returns no groups', () => {
  const rows = [row({ dispatchId: 1, sessionId: 's1' })];
  assert.deepEqual(groupTranscripts(rows, 'dispatch'), []);
});

test('session mode folds same-session rows under one keyed group', () => {
  const rows = [
    row({ dispatchId: 2, sessionId: 's1', sessionLabel: 'swift-otter', turnCount: 5,
          usage: { inputTokens: 100, outputTokens: 20 }, lastSeen: '2026-06-03T10:00:00Z' }),
    row({ dispatchId: 1, sessionId: 's1', sessionLabel: 'swift-otter', turnCount: 3,
          usage: { inputTokens: 50, outputTokens: 10 }, lastSeen: '2026-06-03T09:00:00Z' }),
    row({ dispatchId: 3, sessionId: 's2', sessionLabel: 'eager-heron', turnCount: 1,
          usage: { inputTokens: 10, outputTokens: 2 }, lastSeen: '2026-06-03T08:00:00Z' }),
  ];
  const groups = groupTranscripts(rows, 'session');
  assert.equal(groups.length, 2, 'two distinct sessions');

  const s1 = groups.find(g => g.key === 's1');
  assert.ok(s1, 's1 group present');
  assert.equal(s1.label, 'swift-otter');
  assert.equal(s1.dispatchCount, 2);
  assert.equal(s1.turnSum, 8, '5 + 3 turns');
  assert.equal(s1.tokenSum, 180, '(100+20) + (50+10) tokens');
  assert.equal(s1.lastSeen, '2026-06-03T10:00:00Z', 'latest of the two');
  assert.deepEqual(s1.rows.map(r => r.dispatchId), [2, 1], 'member rows in arrival order');

  // First-appearance order preserves the server's newest-first ordering.
  assert.deepEqual(groups.map(g => g.key), ['s1', 's2']);
});

test('session label falls back to the (short) id when no label is set', () => {
  const rows = [row({ dispatchId: 1, sessionId: 'abc123', sessionLabel: '' })];
  const [g] = groupTranscripts(rows, 'session');
  assert.equal(g.label, 'abc123', 'falls back to sessionId');
});

test('agent mode buckets empty claudeAgentId under "(no agent id)"', () => {
  const rows = [
    row({ dispatchId: 1, claudeAgentId: 'agent-aaa', turnCount: 2 }),
    row({ dispatchId: 2, claudeAgentId: '', turnCount: 4 }),
    row({ dispatchId: 3, turnCount: 1 }), // claudeAgentId absent → same empty bucket
  ];
  const groups = groupTranscripts(rows, 'agent');
  assert.equal(groups.length, 2, 'one named agent + one empty bucket');

  const empty = groups.find(g => g.key === EMPTY_AGENT_KEY);
  assert.ok(empty, 'empty-agent bucket present');
  assert.equal(empty.label, EMPTY_AGENT_LABEL);
  assert.equal(empty.dispatchCount, 2, 'both empty/absent rows folded together');
  assert.equal(empty.turnSum, 5, '4 + 1 turns');

  const named = groups.find(g => g.key === 'agent-aaa');
  assert.ok(named, 'named agent group present');
  assert.equal(named.label, 'agent-aaa');
  assert.equal(named.dispatchCount, 1);
});

// ----- runner -----
let failed = 0;
for (const { name, fn } of tests) {
  try {
    fn();
    console.log('ok  ', name);
  } catch (err) {
    failed++;
    console.error('FAIL', name);
    console.error(err);
  }
}
if (failed > 0) {
  console.error(`\n${failed} test(s) failed`);
  process.exit(1);
}
console.log(`\nall ${tests.length} test(s) passed`);
