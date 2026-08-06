// BACI-203 / BACI-285: one source of truth for the router path shapes
// used across App.tsx, Topbar, PipelineCard, ShippedPopover,
// CommandPalette, IssueComposer, LinkedDocPanel, RelationsPanel,
// IssueLockBanner, FeaturesView, DocsView, and IssueWorkspace.
//
// BACI-285: every page route now carries the active repo's four-letter
// prefix as its first segment (`/<PREFIX>/<page>`), so a shared link is
// self-contained — opening `/ui/BACI/pipeline` selects the BACI repo
// and the pipeline page in one go. The prefix becomes the source of
// truth for the active repo (App derives it from `location.pathname`);
// `localStorage['bacio-active-repo']` is still written so a fresh
// prefix-less `/ui/` knows where to redirect, but it's no longer the
// runtime source of truth. See docs/web-app-mode.md §7a for the routing
// model and the SPA-fallback contract on both the `bacio web` asset
// server and the Wails AssetFileServerFS.

import type { RepoKind } from '../api';

// NAV view ids — kept in lockstep with Topbar.tsx's NAV array. The
// `board` view is special-cased: the path is `/issues` (matches the
// "Issues" tab label) rather than `/board`.
type NavView = 'pipeline' | 'board' | 'features' | 'docs' | 'agents' | 'history' | 'monitor';

// homeView is the nav view a repo lands on when nothing more specific is
// asked for — a bare `/`, a repo switch off a page that doesn't exist in the
// new repo, closing an issue with an empty back stack, or the `/:prefix/*`
// catch-all.
//
// A workspace has no working tree, so a dispatched agent would have nowhere
// to work and the Agentic Pipeline nav entry is hidden for it (locked
// decision D1). Landing a workspace on `pipeline` would therefore strand the
// user on a page with no nav segment highlighted and no way back — so the
// Kanban is home there instead. `kind` is compared against the string literal
// rather than a Wails enum member: RepoKind is a string-literal union in the
// contract precisely so this comparison survives the web build.
export function homeView(kind?: RepoKind): NavView {
  return kind === 'workspace' ? 'board' : 'pipeline';
}

// viewPath maps a top-nav view id onto its base route under the active
// repo prefix. Two view ids have URL aliases that match the top-nav
// labels rather than the internal name: `board` → `/<prefix>/issues`
// ("Issues" tab) and `docs` → `/<prefix>/documents` ("Documents" tab).
// Keeps documentPath / featurePath / issuePath all in plural-noun shape
// too. BACI-337: the `monitor` nav lands on the Transcripts sub-tab —
// the primary destination — rather than the bare `/<prefix>/monitor`
// Network sub-tab. The Network sub-tab is still reachable by deep-link /
// in-page tab click; viewFromPath maps both back to the `monitor` view
// id so the top-nav segment highlights either way.
export function viewPath(prefix: string, view: NavView | string): string {
  if (view === 'board') return `/${prefix}/issues`;
  if (view === 'docs') return `/${prefix}/documents`;
  if (view === 'monitor') return monitorTranscriptsPath(prefix);
  return `/${prefix}/${view}`;
}

// issuePath maps an issue key (e.g. "BACI-100") onto its workspace
// route under the active repo prefix.
export function issuePath(prefix: string, key: string): string {
  return `/${prefix}/issues/${key}`;
}

// repoPrefixFromKey pulls the repo prefix out of a canonical issue key
// ("BACI-100" → "BACI"). BACI-371: the Shipped popover can now list
// rows from other repos, so a deep-link must route under the key's own
// prefix rather than whichever board happens to be open. Returns '' for
// anything that isn't PREFIX-N, leaving the caller to fall back.
export function repoPrefixFromKey(key: string): string {
  const match = /^([A-Za-z0-9]+)-\d+$/.exec(key ?? '');
  return match ? match[1] : '';
}

// processEditPath maps an issue key onto the BACI-294 full-screen Edit
// Process route under the active repo prefix — the editor for an
// in_pipeline card's job chain (`/<prefix>/pipeline/<key>/process`).
export function processEditPath(prefix: string, key: string): string {
  return `/${prefix}/pipeline/${key}/process`;
}

// monitorTranscriptsPath maps onto the BACI-322 Monitor Transcript sub-tab
// under the active repo prefix (`/<prefix>/monitor/transcripts`). The bare
// `/<prefix>/monitor` is the Network sub-tab; this is its Transcripts peer.
export function monitorTranscriptsPath(prefix: string): string {
  return `/${prefix}/monitor/transcripts`;
}

// transcriptPath maps a dispatch id onto the BACI-322 deep-linkable
// full-transcript route under the active repo prefix
// (`/<prefix>/monitor/transcript/<dispatchId>`). dispatch_id is the stable
// per-job key the transcript is addressed on, so the URL can be shared from
// other surfaces / comments without rotting.
export function transcriptPath(prefix: string, dispatchId: number | string): string {
  return `/${prefix}/monitor/transcript/${dispatchId}`;
}

// featurePath maps a feature slug onto its detail route under the
// active repo prefix.
export function featurePath(prefix: string, slug: string): string {
  return `/${prefix}/features/${slug}`;
}

// documentPath maps a doc filename onto its detail route under the
// active repo prefix. Filenames are the canonical slug for docs
// (per-repo unique) and may include dots — encodeURIComponent keeps the
// path safe.
export function documentPath(prefix: string, filename: string): string {
  return `/${prefix}/documents/${encodeURIComponent(filename)}`;
}

// prefixFromPath returns the first path segment — the active repo
// prefix — or the empty string when the path has no segment (bare `/`).
// App reads this off `location.pathname` to derive the active repo,
// since App sits above `<Routes>` and can't own a `:prefix` route param.
export function prefixFromPath(pathname: string): string {
  return pathname.replace(/^\/+/, '').split('/')[0] ?? '';
}

// viewFromPath inverts viewPath for the Topbar's active-segment
// derivation: skip the first (prefix) segment, read the next one as the
// view, map `issues` back to the `board` view id and `documents` back
// to the `docs` view id. Returns the empty string when the path doesn't
// match a known nav view so the segmented control simply shows nothing
// active (e.g. on an overlay screen or a bare prefix root).
export function viewFromPath(pathname: string): string {
  const segs = pathname.replace(/^\/+/, '').split('/');
  const view = segs[1] ?? '';
  if (view === 'issues') return 'board';
  if (view === 'documents') return 'docs';
  return view;
}
