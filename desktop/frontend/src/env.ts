// WEB_MODE is true when the bundle is built with `vite --mode web`,
// i.e. served by `bacio api` at /ui/ instead of hosted by the Wails
// runtime. Components and the api shim read this to swap behaviour for
// the surfaces that only work inside the desktop app (folder picker,
// leader-election chip, prompt-template settings, agent dispatch).
//
// Keep it boolean — not a feature matrix — so it stays grep-friendly.
export const WEB_MODE = import.meta.env.MODE === 'web';
