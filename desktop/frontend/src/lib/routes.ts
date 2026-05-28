// BACI-203: one source of truth for the router path shapes used across
// App.jsx, Topbar, KanbanCard, ShippedPopover, ActivityTray,
// CommandPalette, IssueComposer, LinkedDocPanel, RelationsPanel,
// IssueLockBanner, FeaturesView, DocsView, and IssueWorkspace. Keys-
// only URLs (no repo prefix in the path) — the active repo lives in
// localStorage (`bacio-active-repo`) so detail routes assume the
// slug/key is valid in the current repo. See docs/web-app-mode.md
// §7a for the routing model and the SPA-fallback contract on both
// the `bacio web` asset server and the Wails AssetFileServerFS.

// NAV view ids — kept in lockstep with Topbar.jsx's NAV array. The
// `board` view is special-cased: the path is `/issues` (matches the
// "Issues" tab label) rather than `/board`.
type NavView = 'pipeline' | 'board' | 'features' | 'docs' | 'agents' | 'history';

// viewPath maps a top-nav view id onto its base route. Two view ids
// have URL aliases that match the top-nav labels rather than the
// internal name: `board` → `/issues` ("Issues" tab) and `docs` →
// `/documents` ("Documents" tab). Keeps documentPath / featurePath /
// issuePath all in plural-noun shape too.
export function viewPath(view: NavView | string): string {
  if (view === 'board') return '/issues';
  if (view === 'docs') return '/documents';
  return `/${view}`;
}

// issuePath maps an issue key (e.g. "BACI-100") onto its workspace
// route. The plan deliberately drops the repo prefix from the URL —
// the active repo lives in localStorage and a foreign key falls back
// to the workspace skeleton until the user picks the right repo.
export function issuePath(key: string): string {
  return `/issues/${key}`;
}

// featurePath maps a feature slug onto its detail route.
export function featurePath(slug: string): string {
  return `/features/${slug}`;
}

// documentPath maps a doc filename onto its detail route. Filenames
// are the canonical slug for docs (per-repo unique) and may include
// dots — encodeURIComponent keeps the path safe.
export function documentPath(filename: string): string {
  return `/documents/${encodeURIComponent(filename)}`;
}

// viewFromPath inverts viewPath for the Topbar's active-segment
// derivation: read the first path segment, map `issues` back to the
// `board` view id and `documents` back to the `docs` view id. Returns
// the empty string when the path doesn't match a known nav view so
// the segmented control simply shows nothing active (e.g. on an
// overlay screen).
export function viewFromPath(pathname: string): string {
  const first = pathname.replace(/^\/+/, '').split('/')[0] ?? '';
  if (first === 'issues') return 'board';
  if (first === 'documents') return 'docs';
  return first;
}
