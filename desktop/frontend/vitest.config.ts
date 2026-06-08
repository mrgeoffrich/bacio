import { defineConfig } from 'vitest/config';
import react from '@vitejs/plugin-react';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

// Vitest runs the React tree under jsdom. We resolve the `./api` seam to
// the HTTP transport (api.http.ts) and stub @wailsio/runtime — mirroring
// vite.config.ts's web-mode aliases. jsdom is a browser-like env and
// api.http.ts is pure fetch (no Wails runtime to load), so this is both
// the natural transport for tests and the only one that imports cleanly
// without the native Wails bindings (which aren't present in a test
// env). See docs/web-app-mode.md and .claude/rules/frontend-typescript.md.

const dir = path.dirname(fileURLToPath(import.meta.url));
const apiHttpPath = path.resolve(dir, 'src/api.http.ts');
const wailsStubPath = path.resolve(dir, 'src/wails-stub.ts');

export default defineConfig({
  plugins: [react()],
  resolve: {
    // Array form (not object) so the regex matches both `./api` (App.tsx)
    // and `../api` (every component under components/) — same shape as the
    // vite.config.ts web alias.
    alias: [
      { find: '@wailsio/runtime', replacement: wailsStubPath },
      { find: /^(?:\.\.?\/)+api$/, replacement: apiHttpPath },
    ],
  },
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./vitest.setup.ts'],
    // Only the Vitest specs; the legacy *.smoketest.mjs run via the
    // separate `test:smoke` node runner.
    include: ['src/**/*.test.{ts,tsx}'],
  },
});
