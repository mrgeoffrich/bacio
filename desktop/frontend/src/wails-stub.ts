// No-op shim for @wailsio/runtime. Vite aliases the real runtime to
// this module when building for web mode (--mode web), so any
// import-time `Events.On(...)` / `Events.Emit(...)` call doesn't
// blow up — the runtime isn't there at all in a browser.
//
// Mirrors only the surface we touch from frontend code: Events.On /
// Emit. App.jsx uses Events.On for 'leaderStatus' (gated on WEB_MODE,
// but the import itself runs at module load and would otherwise
// throw). Add more entries here only if a new module-level Wails
// call is introduced.

export const Events = {
  // The real Events.On returns an unsubscribe function; mirror that
  // so callers can `const off = Events.On(...); off()` without an
  // extra typeof-check.
  On: (_event: string, _handler: (e: unknown) => void): (() => void) => () => {},
  Emit: (_event: string, _payload?: unknown): void => {},
};
