import { useEffect, useRef } from 'react';

// navGuard — a one-slot registry that lets a mounted route veto a
// navigation started somewhere else in the shell (the Topbar tabs, the
// repo picker, a deep-link out of the notification bell).
//
// Why this exists rather than react-router's `useBlocker`: that hook
// requires a DATA router (`createBrowserRouter`), and main.tsx mounts a
// plain `<BrowserRouter>` — calling it throws "useBlocker must be used
// within a data router" on mount. Migrating the router is a far larger
// change than the one page that needs a guard, so the shell's own
// navigation entry points funnel through `requestNavigation` instead.
//
// The contract is deliberately inverted from "return false to cancel":
// a guard that intercepts takes OWNERSHIP of the pending navigation and
// is responsible for either running `proceed` later (the user confirmed)
// or dropping it (the user cancelled). That way the caller never has to
// hold the intent, and a guard can put a modal between the click and the
// navigation without any of the callers knowing a modal exists.
//
// One slot is enough: only one guarded route is mounted at a time. The
// unregister restores whatever was installed before it, so an unmount in
// an unexpected order can't strand a stale guard.
//
// ── What this does NOT cover ─────────────────────────────────────────
//
// It is an allowlist of in-app entry points, so it only sees navigations
// that call `requestNavigation`. Every current shell exit does — the
// Topbar tabs, the Settings gear, the Sync pill, the repo picker, the
// create menu, `openIssue` / `openCard` — but a HISTORY POP does not, and
// cannot: browser Back/Forward, the mouse back button and a trackpad
// swipe all reach the router without passing through any of our code.
// `popstate` fires after the entry has already changed, so the only way
// to "cancel" one is to push a compensating entry back, which corrupts
// the back stack for every unguarded page and fights `navigate(-1)` in
// `closeIssue`. `beforeunload` (in EditEpicPage) covers reload and tab
// close; the back button is the remaining hole.
//
// Closing it properly means a DATA ROUTER: replace `<BrowserRouter>` in
// main.tsx with `createBrowserRouter` + `<RouterProvider>`, move the
// `<Routes>` tree in App.tsx into a route-object array, and swap this
// module for `useBlocker`, which the router calls for pops as well as
// pushes. The work is in the move: the providers currently sit above
// `<Routes>` and read `useLocation`, so they would have to become a
// layout route, and the Settings overlay branch that swaps `<Routes>` out
// wholesale would have to become a real route first.

// A guard returns true when it has taken the navigation over, false when
// navigation may continue immediately.
export type NavGuard = (proceed: () => void) => boolean;

let current: NavGuard | null = null;

export function registerNavGuard(guard: NavGuard): () => void {
  const previous = current;
  current = guard;
  return () => {
    if (current === guard) current = previous;
  };
}

// requestNavigation is the shell-side entry point. Every navigation that
// can strand unsaved work goes through it; an unguarded app is one extra
// function call, not a behaviour change.
export function requestNavigation(proceed: () => void): void {
  const guard = current;
  if (guard && guard(proceed)) return;
  proceed();
}

// Test-only: module state outlives a component tree, so a suite that
// unmounts mid-guard would leak into the next case.
export function resetNavGuards(): void {
  current = null;
}

// useNavGuard arms `onBlocked` for as long as `armed` is true. The
// callback is held in a ref (the latest-ref pattern the repo already uses
// for memoised list items) so an inline arrow doesn't re-register the
// guard on every keystroke of the form it is protecting.
export function useNavGuard(armed: boolean, onBlocked: (proceed: () => void) => void): void {
  const latest = useRef(onBlocked);
  useEffect(() => {
    latest.current = onBlocked;
  });
  useEffect(() => {
    if (!armed) return;
    return registerNavGuard((proceed) => {
      latest.current(proceed);
      return true;
    });
  }, [armed]);
}
