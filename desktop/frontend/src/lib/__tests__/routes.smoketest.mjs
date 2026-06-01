// Smoke test for the BACI-203 / BACI-285 router path helpers in
// `lib/routes.ts`. Mirrors the pattern in `issueState.smoketest.mjs` /
// `shipFlourish.smoketest.mjs` — plain Node + assert, no Vitest dependency.
//
// BACI-285: every page route carries the active repo's prefix as its
// first segment, so the path builders take `prefix` as their first
// argument and `viewFromPath` skips the prefix segment before
// classifying. `prefixFromPath` reads the prefix back off a pathname.
//
// Run from the worktree root:
//   node desktop/frontend/src/lib/__tests__/routes.smoketest.mjs

import assert from 'node:assert/strict';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const moduleRoot = path.resolve(__dirname, '..');

const {
  viewPath,
  issuePath,
  featurePath,
  documentPath,
  viewFromPath,
  prefixFromPath,
  monitorTranscriptsPath,
  transcriptPath,
} = await import(path.join(moduleRoot, 'routes.ts'));

const tests = [];
function test(name, fn) { tests.push({ name, fn }); }

test('viewPath scopes routes to the prefix; board/docs keep their label aliases', () => {
  assert.equal(viewPath('BACI', 'board'), '/BACI/issues');
  assert.equal(viewPath('BACI', 'pipeline'), '/BACI/pipeline');
  assert.equal(viewPath('BACI', 'features'), '/BACI/features');
  assert.equal(viewPath('BACI', 'docs'), '/BACI/documents');
  assert.equal(viewPath('BACI', 'agents'), '/BACI/agents');
  assert.equal(viewPath('MINI', 'history'), '/MINI/history');
});

test('issuePath builds the prefixed workspace route', () => {
  assert.equal(issuePath('BACI', 'BACI-100'), '/BACI/issues/BACI-100');
});

test('featurePath builds the prefixed feature detail route', () => {
  assert.equal(featurePath('BACI', 'auth-rewrite'), '/BACI/features/auth-rewrite');
});

test('documentPath URL-encodes the filename under the prefix', () => {
  assert.equal(documentPath('BACI', 'plan-baci-203'), '/BACI/documents/plan-baci-203');
  // Filenames frequently carry dots — those don't need encoding but should pass through.
  assert.equal(documentPath('BACI', 'arch-notes.md'), '/BACI/documents/arch-notes.md');
  // A filename with a space would never come from bacio (kebab-case is enforced),
  // but the encoder still does the right thing for any edge case.
  assert.equal(documentPath('BACI', 'a b'), '/BACI/documents/a%20b');
});

test('monitorTranscriptsPath builds the prefixed Transcripts sub-tab route', () => {
  assert.equal(monitorTranscriptsPath('BACI'), '/BACI/monitor/transcripts');
  assert.equal(monitorTranscriptsPath('MINI'), '/MINI/monitor/transcripts');
});

test('transcriptPath builds the prefixed deep-link route keyed on the dispatch id', () => {
  assert.equal(transcriptPath('BACI', 42), '/BACI/monitor/transcript/42');
  // String ids pass through unchanged (the URL param arrives as a string).
  assert.equal(transcriptPath('MINI', '7'), '/MINI/monitor/transcript/7');
});

test('prefixFromPath reads the first (prefix) segment off a pathname', () => {
  assert.equal(prefixFromPath('/BACI/pipeline'), 'BACI');
  assert.equal(prefixFromPath('/BACI/issues/BACI-100'), 'BACI');
  assert.equal(prefixFromPath('/MINI'), 'MINI');
  // Bare root / empty path → empty prefix (App's loading / redirect branch).
  assert.equal(prefixFromPath('/'), '');
  assert.equal(prefixFromPath(''), '');
});

test('viewFromPath skips the prefix then inverts viewPath for the Topbar', () => {
  assert.equal(viewFromPath('/BACI/issues'), 'board');
  assert.equal(viewFromPath('/BACI/issues/BACI-100'), 'board');
  assert.equal(viewFromPath('/BACI/pipeline'), 'pipeline');
  assert.equal(viewFromPath('/BACI/features'), 'features');
  assert.equal(viewFromPath('/BACI/features/auth-rewrite'), 'features');
  assert.equal(viewFromPath('/BACI/documents'), 'docs');
  assert.equal(viewFromPath('/BACI/documents/some-doc.md'), 'docs');
  assert.equal(viewFromPath('/BACI/agents'), 'agents');
  assert.equal(viewFromPath('/BACI/history'), 'history');
  // A bare prefix root (no page segment) / unknown paths surface as
  // empty so the segmented control shows nothing active.
  assert.equal(viewFromPath('/BACI'), '');
  assert.equal(viewFromPath('/'), '');
  assert.equal(viewFromPath(''), '');
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
