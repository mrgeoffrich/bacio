// Smoke test for the BACI-203 router path helpers in `lib/routes.ts`.
// Mirrors the pattern in `issueState.smoketest.mjs` / `shipFlourish.smoketest.mjs`
// — plain Node + assert, no Vitest dependency.
//
// Run from the worktree root:
//   node desktop/frontend/src/lib/__tests__/routes.smoketest.mjs

import assert from 'node:assert/strict';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const moduleRoot = path.resolve(__dirname, '..');

const { viewPath, issuePath, featurePath, documentPath, viewFromPath } =
  await import(path.join(moduleRoot, 'routes.ts'));

const tests = [];
function test(name, fn) { tests.push({ name, fn }); }

test('viewPath maps board and docs to label-style routes; the rest are one-to-one', () => {
  assert.equal(viewPath('board'), '/issues');
  assert.equal(viewPath('features'), '/features');
  assert.equal(viewPath('docs'), '/documents');
  assert.equal(viewPath('agents'), '/agents');
  assert.equal(viewPath('history'), '/history');
});

test('issuePath builds the workspace route', () => {
  assert.equal(issuePath('BACI-100'), '/issues/BACI-100');
});

test('featurePath builds the feature detail route', () => {
  assert.equal(featurePath('auth-rewrite'), '/features/auth-rewrite');
});

test('documentPath URL-encodes the filename', () => {
  assert.equal(documentPath('plan-baci-203'), '/documents/plan-baci-203');
  // Filenames frequently carry dots — those don't need encoding but should pass through.
  assert.equal(documentPath('arch-notes.md'), '/documents/arch-notes.md');
  // A filename with a space would never come from bacio (kebab-case is enforced),
  // but the encoder still does the right thing for any edge case.
  assert.equal(documentPath('a b'), '/documents/a%20b');
});

test('viewFromPath inverts viewPath for the Topbar derivation', () => {
  assert.equal(viewFromPath('/issues'), 'board');
  assert.equal(viewFromPath('/issues/BACI-100'), 'board');
  assert.equal(viewFromPath('/features'), 'features');
  assert.equal(viewFromPath('/features/auth-rewrite'), 'features');
  assert.equal(viewFromPath('/documents'), 'docs');
  assert.equal(viewFromPath('/documents/some-doc.md'), 'docs');
  assert.equal(viewFromPath('/agents'), 'agents');
  assert.equal(viewFromPath('/history'), 'history');
  // Unknown paths surface as empty so the segmented control shows nothing active.
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
