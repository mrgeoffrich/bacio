// run-smoketests.mjs — runs every *.smoketest.mjs under src/ in its own
// node child process and aggregates pass/fail. Exits non-zero if any
// suite fails so `npm run test` (and CI) gates on the legacy smoke tests
// until they're migrated to Vitest. The smoke tests import the modules
// they cover by explicit `.ts` path and rely on node's built-in type
// stripping (default-on from node 22.18 / 23.6 — CI runs node 22, dev
// runs newer), so no extra flags are needed here. (BACI-353)

import { spawnSync } from 'node:child_process';
import { readdirSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const frontendRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const srcRoot = path.join(frontendRoot, 'src');

function findSmokeTests(dir) {
  const out = [];
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      out.push(...findSmokeTests(full));
    } else if (entry.isFile() && entry.name.endsWith('.smoketest.mjs')) {
      out.push(full);
    }
  }
  return out;
}

const files = findSmokeTests(srcRoot).sort();
if (files.length === 0) {
  console.error('no *.smoketest.mjs files found under src/');
  process.exit(1);
}

let failed = 0;
for (const file of files) {
  const rel = path.relative(frontendRoot, file);
  console.log(`\n# ${rel}`);
  const res = spawnSync(process.execPath, [file], { stdio: 'inherit' });
  if (res.status !== 0) {
    failed++;
    console.error(`SMOKE FAIL: ${rel}`);
  }
}

console.log(`\nsmoke suites: ${files.length}, failed: ${failed}`);
process.exit(failed > 0 ? 1 : 0);
