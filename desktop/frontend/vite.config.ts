import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import wails from "@wailsio/runtime/plugins/vite";
import path from "node:path";

// Two build modes, sharing one React tree:
//
//   vite (default) | wails3 dev | wails3 build   → Wails desktop build.
//   vite --mode web                              → browser-served bundle,
//                                                  served by `bacio api` at /ui/.
//
// In web mode we swap the api import surface to `./api.http` (fetch +
// reshape onto the REST API), and stub out `@wailsio/runtime` to a
// no-op so module-level imports don't crash. Components gate
// behaviour on `WEB_MODE` from `./env` for everything else (folder
// picker, leader chip, prompt-template Settings subsection, ...).
//
// Output goes to dist-web/ so it doesn't collide with the Wails
// build's frontend/dist/.

const apiHttpPath = path.resolve(__dirname, 'src/api.http.ts');
const wailsStubPath = path.resolve(__dirname, 'src/wails-stub.ts');

export default defineConfig(({ mode }) => {
  const isWeb = mode === 'web';
  return {
    server: {
      host: "127.0.0.1",
      // Wails wants 9245; the web dev server picks a different port so
      // both can run side-by-side without colliding.
      port: Number(process.env.WAILS_VITE_PORT) || (isWeb ? 5174 : 9245),
      strictPort: true,
    },
    resolve: {
      // Array form (vs object) so the regex `find` matches both
      // `./api` (used by App.jsx) and `../api` (used by every
      // component under components/). Web mode also stubs the
      // Wails runtime to a no-op so App.jsx's module-level
      // `import { Events } from '@wailsio/runtime'` doesn't crash.
      alias: isWeb ? [
        { find: '@wailsio/runtime', replacement: wailsStubPath },
        { find: /^(?:\.\.?\/)+api$/, replacement: apiHttpPath },
      ] : [],
    },
    // The web build is hosted at /ui/ on the bacio api server, so
    // emit a matching base path so the index.html bootstrap assets
    // resolve when the SPA is served from a subdirectory.
    base: isWeb ? '/ui/' : '/',
    build: isWeb ? { outDir: 'dist-web', emptyOutDir: true } : undefined,
    plugins: isWeb
      ? [react()]
      : [react(), wails("./bindings")],
  };
});
