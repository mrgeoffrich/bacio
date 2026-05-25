// Persistence helpers for the ActivityTray's collapsed/expanded
// preference (BACI-186). Extends the existing per-consumer pattern
// from App.jsx (`readTheme`/`persistTheme`, `readActiveRepo`/
// `persistActiveRepo`) and Board.jsx (`readBoardScroll`/
// `persistBoardScroll`) — same try/catch shape, same kebab-cased
// `bacio-`-prefixed key, helpers live next to the component that
// reads them. A shared `useLocalStoragePref` hook is out of scope
// until a second new caller lands; one would be premature
// abstraction today.
//
// Storage shape: the raw string `'1'` for collapsed, `'0'` (or
// anything else) for expanded — mirrors how `theme` /
// `bacio-active-repo` store raw strings rather than JSON. localStorage
// is always present inside the Wails webview, but a hardened browser
// profile can throw on access, so every read/write is wrapped and
// falls back to the default.

export const STORAGE_KEY = 'bacio-activity-tray-collapsed';

export function readCollapsed(): boolean {
  try {
    return localStorage.getItem(STORAGE_KEY) === '1';
  } catch {
    return false;
  }
}

export function persistCollapsed(collapsed: boolean): void {
  try {
    localStorage.setItem(STORAGE_KEY, collapsed ? '1' : '0');
  } catch {
    /* non-fatal — the preference just won't survive a relaunch */
  }
}
