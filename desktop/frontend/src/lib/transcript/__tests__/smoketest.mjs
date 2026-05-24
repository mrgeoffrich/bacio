// Smoke tests for the transcript parser + pairer + token summary
// (BACI-125). Runs under plain Node — no Vitest dependency added in
// this PR (the plan calls full Vitest wiring a follow-up). Each
// `assert*` failure throws; the runner reports the failed assertion's
// line and exits non-zero so a build hook can wire this in.
//
// Run from the worktree root:
//   node desktop/frontend/src/lib/transcript/__tests__/smoketest.mjs
//
// The tests import the .ts modules directly — Node 22+'s
// `--experimental-strip-types` (auto-enabled on plain .mjs imports
// of .ts in Node 24) handles it.

import assert from 'node:assert/strict';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const moduleRoot = path.resolve(__dirname, '..');

// Dynamically import the .ts modules. Node's TS stripping requires the
// `.ts` extension on the import path.
const { parse } = await import(path.join(moduleRoot, 'parse.ts'));
const { pair } = await import(path.join(moduleRoot, 'pair.ts'));
const { summariseTokens, formatKilo } = await import(path.join(moduleRoot, 'tokenSummary.ts'));
const { extractDispatchTags } = await import(path.join(moduleRoot, 'dispatchTags.ts'));
// The full per-tool registry (tools/index.ts) re-exports TSX
// components — Node's strip-types won't load JSX. The component
// surface is covered by the integration smoke test in §11 of the
// plan (Playwright through `bacio web`); this script focuses on the
// pure parser/pairing/format/tokens logic.
const { flattenResultContent, countLines, formatTimeDelta } = await import(path.join(moduleRoot, 'format.ts'));
const { resolveAnchor, evalEventRefFor } = await import(path.join(moduleRoot, 'evalAnchor.ts'));

// ---- helpers ----
const tests = [];
function test(name, fn) { tests.push({ name, fn }); }

function makeLine(obj) { return JSON.stringify(obj); }

// ---- shared fixtures ----
const dispatchPromptLine = makeLine({
  type: 'user',
  timestamp: '2026-05-24T12:00:00.000Z',
  message: {
    role: 'user',
    content: 'Hi.\n\n<issue_id>BACI-125</issue_id>\n<mode>implement</mode>\n<dispatch_id>491</dispatch_id>',
  },
});

const attachmentLine = makeLine({
  type: 'attachment',
  timestamp: '2026-05-24T12:00:00.500Z',
  attachment: { type: 'deferred_tools_delta' },
});

const assistantWithToolUse = makeLine({
  type: 'assistant',
  timestamp: '2026-05-24T12:00:01.000Z',
  message: {
    role: 'assistant',
    content: [
      { type: 'text', text: 'Looking now.' },
      { type: 'tool_use', id: 'toolu_abc', name: 'Bash', input: { command: 'ls', description: 'list' } },
    ],
    usage: {
      input_tokens: 100,
      output_tokens: 20,
      cache_read_input_tokens: 50,
      cache_creation_input_tokens: 25,
    },
  },
});

const userToolResult = makeLine({
  type: 'user',
  timestamp: '2026-05-24T12:00:01.500Z',
  message: {
    role: 'user',
    content: [
      { type: 'tool_result', tool_use_id: 'toolu_abc', content: 'file1\nfile2\n', is_error: false },
    ],
  },
});

const errorToolResult = makeLine({
  type: 'user',
  timestamp: '2026-05-24T12:00:02.000Z',
  message: {
    role: 'user',
    content: [
      { type: 'tool_result', tool_use_id: 'toolu_err', content: 'boom', is_error: true },
    ],
  },
});

const assistantErrorCall = makeLine({
  type: 'assistant',
  timestamp: '2026-05-24T12:00:01.900Z',
  message: {
    role: 'assistant',
    content: [{ type: 'tool_use', id: 'toolu_err', name: 'Bash', input: { command: 'false' } }],
    usage: { input_tokens: 10, output_tokens: 2 },
  },
});

const userMeta = makeLine({
  type: 'user',
  timestamp: '2026-05-24T12:00:03.000Z',
  isMeta: true,
  message: { role: 'user', content: 'System reminder text.' },
});

const sampleTranscript = [
  dispatchPromptLine,
  attachmentLine,
  assistantWithToolUse,
  userToolResult,
  assistantErrorCall,
  errorToolResult,
  userMeta,
].join('\n');

// ---- parse tests ----
test('parse classifies each event kind', () => {
  const r = parse(sampleTranscript);
  assert.equal(r.warnings.length, 0);
  assert.equal(r.events.length, 7);
  assert.equal(r.events[0].kind, 'user-prompt');
  assert.equal(r.events[1].kind, 'attachment');
  assert.equal(r.events[2].kind, 'assistant');
  assert.equal(r.events[3].kind, 'user-tool-result');
  assert.equal(r.events[4].kind, 'assistant');
  assert.equal(r.events[5].kind, 'user-tool-result');
  assert.equal(r.events[6].kind, 'user-meta');
});

test('parse surfaces malformed lines as warnings, not throws', () => {
  const r = parse('not json\n' + dispatchPromptLine);
  assert.equal(r.warnings.length, 1);
  assert.equal(r.events.length, 1);
  assert.equal(r.events[0].kind, 'user-prompt');
});

test('parse decodes assistant usage', () => {
  const r = parse(assistantWithToolUse);
  assert.equal(r.events.length, 1);
  const ev = r.events[0];
  assert.equal(ev.kind, 'assistant');
  assert.equal(ev.usage.input_tokens, 100);
  assert.equal(ev.usage.output_tokens, 20);
  assert.equal(ev.usage.cache_read_input_tokens, 50);
  assert.equal(ev.usage.cache_creation_input_tokens, 25);
});

test('parse decodes a tool_use block with input', () => {
  const r = parse(assistantWithToolUse);
  const ev = r.events[0];
  const tu = ev.blocks.find(b => b.type === 'tool_use');
  assert.ok(tu);
  assert.equal(tu.name, 'Bash');
  assert.equal(tu.input.command, 'ls');
});

test('parse rawLines preserves original lines', () => {
  const r = parse('a\nb\nc');
  assert.deepEqual(r.rawLines, ['a', 'b', 'c']);
});

// ---- pair tests ----
test('pair folds tool_use + tool_result into one tool-call item', () => {
  const r = parse(sampleTranscript);
  const items = pair(r.events);
  const toolCalls = items.filter(i => i.kind === 'tool-call');
  assert.equal(toolCalls.length, 2);
  assert.equal(toolCalls[0].toolName, 'Bash');
  assert.equal(toolCalls[0].result?.content, 'file1\nfile2\n');
  assert.equal(toolCalls[1].toolName, 'Bash');
  assert.equal(toolCalls[1].result?.isError, true);
});

test('pair emits an assistant item for text-only blocks ahead of the tool call', () => {
  const r = parse(sampleTranscript);
  const items = pair(r.events);
  // Order: dispatch, attachment, assistant (text), tool-call (Bash), tool-call (error), system-reminder
  assert.equal(items[0].kind, 'dispatch');
  assert.equal(items[1].kind, 'attachment');
  assert.equal(items[2].kind, 'assistant');
  assert.equal(items[3].kind, 'tool-call');
  assert.equal(items[4].kind, 'tool-call');
  assert.equal(items[5].kind, 'system-reminder');
});

test('pair flags orphaned results', () => {
  const orphanResult = makeLine({
    type: 'user',
    timestamp: '2026-05-24T12:00:10.000Z',
    message: {
      role: 'user',
      content: [
        { type: 'tool_result', tool_use_id: 'never-called', content: 'leftover', is_error: false },
      ],
    },
  });
  const r = parse(dispatchPromptLine + '\n' + orphanResult);
  const items = pair(r.events);
  const orphans = items.filter(i => i.kind === 'tool-call' && i.orphanedResult);
  assert.equal(orphans.length, 1);
  assert.equal(orphans[0].result?.content, 'leftover');
});

test('pair handles concurrent tool calls in one user-tool-result event', () => {
  const a = makeLine({
    type: 'assistant', timestamp: 't1',
    message: { role: 'assistant', content: [
      { type: 'tool_use', id: 'a', name: 'Bash', input: { command: 'a' } },
      { type: 'tool_use', id: 'b', name: 'Bash', input: { command: 'b' } },
    ], usage: {} },
  });
  const r = makeLine({
    type: 'user', timestamp: 't2',
    message: { role: 'user', content: [
      { type: 'tool_result', tool_use_id: 'a', content: 'ra' },
      { type: 'tool_result', tool_use_id: 'b', content: 'rb' },
    ] },
  });
  const parsed = parse(a + '\n' + r);
  const items = pair(parsed.events);
  const calls = items.filter(i => i.kind === 'tool-call');
  assert.equal(calls.length, 2);
  assert.equal(calls[0].result?.content, 'ra');
  assert.equal(calls[1].result?.content, 'rb');
  // The standalone tool-result event should be dropped from the stream.
  assert.equal(items.filter(i => i.kind === 'user-tool-result').length, 0);
});

// ---- tokenSummary tests ----
test('summariseTokens sums across multiple assistant events', () => {
  const r = parse(sampleTranscript);
  const t = summariseTokens(r.events);
  assert.equal(t.totalInput, 110);
  assert.equal(t.totalOutput, 22);
  assert.equal(t.totalCacheRead, 50);
  assert.equal(t.totalCacheCreate, 25);
  // 50 / (50 + 25 + 110) = 50/185 ≈ 0.27
  assert.ok(t.cacheHitRatio > 0.2 && t.cacheHitRatio < 0.3);
});

test('formatKilo renders small and large values', () => {
  assert.equal(formatKilo(0), '0');
  assert.equal(formatKilo(412), '412');
  assert.equal(formatKilo(18234), '18.2k');
});

// ---- dispatch tag extraction ----
test('extractDispatchTags pulls issue / mode / dispatch_id', () => {
  const tags = extractDispatchTags(
    'preamble\n<issue_id>BACI-99</issue_id>\n<mode>plan</mode>\n<dispatch_id>123</dispatch_id>\n',
  );
  assert.equal(tags.issue, 'BACI-99');
  assert.equal(tags.mode, 'plan');
  assert.equal(tags.dispatchId, '123');
});

// ---- format helpers ----
test('flattenResultContent reduces block lists to plain text', () => {
  assert.equal(flattenResultContent('hello'), 'hello');
  assert.equal(
    flattenResultContent([
      { type: 'text', text: 'a' },
      { type: 'image' },
      { type: 'text', text: 'b' },
    ]),
    'a\n[image]\nb',
  );
});

test('countLines is a line count, not a length', () => {
  assert.equal(countLines(''), 0);
  assert.equal(countLines('a'), 1);
  assert.equal(countLines('a\nb'), 2);
  assert.equal(countLines('a\nb\nc\n'), 4);
});

test('formatTimeDelta handles seconds and minutes', () => {
  assert.equal(formatTimeDelta(), '');
  assert.equal(
    formatTimeDelta('2026-05-24T12:00:01.000Z', '2026-05-24T12:00:00.500Z'),
    '+500ms',
  );
  assert.equal(
    formatTimeDelta('2026-05-24T12:00:02.000Z', '2026-05-24T12:00:00.500Z'),
    '+1.5s',
  );
});

// ---- BACI-141: eval-anchor resolution ----
test('resolveAnchor matches tool_use_id to the tool-call card', () => {
  const r = parse(sampleTranscript);
  const items = pair(r.events);
  const hit = resolveAnchor(items, 'tool_use_id:toolu_abc');
  assert.ok(hit, 'expected resolveAnchor to find the Bash tool-call');
  assert.equal(hit.kind, 'tool-call');
  assert.equal(hit.use.id, 'toolu_abc');
});

test('resolveAnchor matches line_index to the originating event', () => {
  const r = parse(sampleTranscript);
  const items = pair(r.events);
  // The dispatch prompt is the first line — line_index 0.
  const hit = resolveAnchor(items, 'line_index:0');
  assert.ok(hit, 'expected resolveAnchor to land on the dispatch prompt');
  assert.equal(hit.kind, 'dispatch');
});

test('resolveAnchor returns null for unknown refs', () => {
  const r = parse(sampleTranscript);
  const items = pair(r.events);
  assert.equal(resolveAnchor(items, 'tool_use_id:no-such-id'), null);
  assert.equal(resolveAnchor(items, 'line_index:999'), null);
  assert.equal(resolveAnchor(items, ''), null);
  assert.equal(resolveAnchor(items, 'garbage'), null);
});

test('evalEventRefFor prefers tool_use_id on tool-call cards', () => {
  const r = parse(sampleTranscript);
  const items = pair(r.events);
  const tool = items.find(i => i.kind === 'tool-call');
  assert.ok(tool);
  assert.equal(evalEventRefFor(tool), 'tool_use_id:toolu_abc');
});

test('evalEventRefFor falls back to line_index on non-tool cards', () => {
  const r = parse(sampleTranscript);
  const items = pair(r.events);
  const dispatch = items.find(i => i.kind === 'dispatch');
  assert.ok(dispatch);
  assert.equal(evalEventRefFor(dispatch), 'line_index:0');
});

// ---- runner ----
let failed = 0;
for (const t of tests) {
  try {
    await t.fn();
    process.stdout.write(`ok  ${t.name}\n`);
  } catch (err) {
    failed++;
    process.stdout.write(`FAIL ${t.name}\n  ${err?.stack || err}\n`);
  }
}
process.stdout.write(`\n${tests.length - failed}/${tests.length} tests passed\n`);
process.exit(failed === 0 ? 0 : 1);
