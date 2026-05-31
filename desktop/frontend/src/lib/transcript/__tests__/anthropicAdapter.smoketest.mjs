// Smoke test for the BACI-308 anthropicTranscriptToEvents adapter — the one
// new piece of render code the Monitor capture drill-down needs. Mirrors the
// plain-Node + assert pattern (no Vitest) of the sibling transcript smoke
// test; Node 24 strips the type-only imports on the .ts module.
//
// Run from the worktree root:
//   node desktop/frontend/src/lib/transcript/__tests__/anthropicAdapter.smoketest.mjs

import assert from 'node:assert/strict';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const moduleRoot = path.resolve(__dirname, '..');

const { anthropicTranscriptToEvents, anthropicTranscriptToParseResult } =
  await import(path.join(moduleRoot, 'anthropicAdapter.ts'));
const { pair } = await import(path.join(moduleRoot, 'pair.ts'));

const tests = [];
function test(name, fn) { tests.push({ name, fn }); }

// A representative captured transcript: a user prompt, an assistant turn with
// text + a tool_use, a user tool_result reply, and one auxiliary turn.
const fixture = {
  dispatch_id: 42,
  model: 'claude-opus-4',
  messages: [
    { role: 'user', content: [{ type: 'text', text: 'do the thing' }] },
    {
      role: 'assistant',
      content: [
        { type: 'text', text: 'on it' },
        { type: 'tool_use', id: 'tu_1', name: 'Bash', input: { command: 'ls' } },
      ],
    },
    {
      role: 'user',
      content: [
        { type: 'tool_result', tool_use_id: 'tu_1', is_error: false, content: 'file.txt' },
      ],
    },
  ],
  auxiliary: [
    {
      model: 'claude-haiku',
      blocks: [{ type: 'text', text: 'Title: Do the thing' }],
      usage: { input_tokens: 10, output_tokens: 4 },
    },
  ],
};

test('maps each message to the right viewer event kind, in order', () => {
  const events = anthropicTranscriptToEvents(fixture);
  // user-prompt, assistant, user-tool-result, then the auxiliary assistant.
  assert.deepEqual(events.map(e => e.kind), [
    'user-prompt',
    'assistant',
    'user-tool-result',
    'assistant',
  ]);
  // lineIndex is the running event index.
  assert.deepEqual(events.map(e => e.lineIndex), [0, 1, 2, 3]);
});

test('assistant blocks carry text + tool_use; the prompt text survives', () => {
  const events = anthropicTranscriptToEvents(fixture);
  assert.equal(events[0].text, 'do the thing');
  const asst = events[1];
  assert.equal(asst.blocks.length, 2);
  assert.equal(asst.blocks[0].type, 'text');
  assert.equal(asst.blocks[1].type, 'tool_use');
  assert.equal(asst.blocks[1].id, 'tu_1');
  assert.equal(asst.blocks[1].name, 'Bash');
  assert.deepEqual(asst.blocks[1].input, { command: 'ls' });
});

test('tool_result maps to a ToolResult with the matching tool_use_id', () => {
  const events = anthropicTranscriptToEvents(fixture);
  const tr = events[2];
  assert.equal(tr.results.length, 1);
  assert.equal(tr.results[0].toolUseId, 'tu_1');
  assert.equal(tr.results[0].isError, false);
  assert.equal(tr.results[0].content, 'file.txt');
});

test('auxiliary turn becomes a trailing assistant event with its usage', () => {
  const events = anthropicTranscriptToEvents(fixture);
  const aux = events[3];
  assert.equal(aux.kind, 'assistant');
  assert.equal(aux.usage.input_tokens, 10);
  assert.equal(aux.usage.output_tokens, 4);
  assert.equal(aux.blocks[0].text, 'Title: Do the thing');
});

test('tool pairing survives into pair(): the tool_use pairs with its result', () => {
  const events = anthropicTranscriptToEvents(fixture);
  const items = pair(events);
  const toolCall = items.find(i => i.kind === 'tool-call');
  assert.ok(toolCall, 'expected a tool-call render item');
  assert.equal(toolCall.toolName, 'Bash');
  assert.ok(toolCall.result, 'tool_use should be paired with its result');
  assert.equal(toolCall.result.content, 'file.txt');
  assert.equal(toolCall.orphanedResult, undefined);
});

test('a user turn with both a tool_result and fresh text emits two events', () => {
  const events = anthropicTranscriptToEvents({
    messages: [
      {
        role: 'user',
        content: [
          { type: 'tool_result', tool_use_id: 'tu_x', content: 'ok' },
          { type: 'text', text: 'now do the next thing' },
        ],
      },
    ],
  });
  assert.deepEqual(events.map(e => e.kind), ['user-tool-result', 'user-prompt']);
  assert.equal(events[1].text, 'now do the next thing');
});

test('anthropicTranscriptToParseResult yields events + empty warnings + rawLines', () => {
  const pr = anthropicTranscriptToParseResult(fixture);
  assert.equal(pr.events.length, 4);
  assert.deepEqual(pr.warnings, []);
  assert.equal(pr.rawLines.length, 4);
  // rawLines are JSON strings of the source objects.
  assert.doesNotThrow(() => JSON.parse(pr.rawLines[0]));
});

test('an empty transcript yields no events (and no throw)', () => {
  assert.deepEqual(anthropicTranscriptToEvents({}), []);
  assert.deepEqual(anthropicTranscriptToEvents({ messages: [] }), []);
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
