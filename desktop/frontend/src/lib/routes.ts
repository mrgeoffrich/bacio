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

// NAV view ids — kept in lockstep with Topbar.tsx's NAV array. Three
// view ids are special-cased in viewPath below: their URL matches the
// tab label rather than the internal name (`board` → `/issues`,
// `docs` → `/documents`, `features` → `/epics`). The ids themselves are
// internal and deliberately unchanged by the Features → Epics rename —
// they are load-bearing across App's routes, RepoProvider's legacy page
// words, the `mk-features-*` CSS and every component filename.
export type NavView = 'pipeline' | 'board' | 'features' | 'docs' | 'agents' | 'history' | 'monitor';

// homeView lives in ./nav — it is defined in terms of which nav entries
// a space actually exposes, so it belongs beside the nav data rather
// than beside the path shapes.

// viewPath maps a top-nav view id onto its base route under the active
// repo prefix. Three view ids have URL aliases that match the top-nav
// labels rather than the internal name: `board` → `/<prefix>/issues`
// ("Issues" tab), `docs` → `/<prefix>/documents` ("Documents" tab) and
// `features` → `/<prefix>/epics` ("Epics" tab). Keeps documentPath /
// featurePath / issuePath all in plural-noun shape too. The `features`
// alias is the whole mechanism behind the Epics URL rename: the view id
// stays `features` everywhere internally and only the emitted path
// changes. BACI-337: the `monitor` nav lands on the Transcripts sub-tab —
// the primary destination — rather than the bare `/<prefix>/monitor`
// Network sub-tab. The Network sub-tab is still reachable by deep-link /
// in-page tab click; viewFromPath maps both back to the `monitor` view
// id so the top-nav segment highlights either way.
export function viewPath(prefix: string, view: NavView | string): string {
  if (view === 'board') return `/${prefix}/issues`;
  if (view === 'docs') return `/${prefix}/documents`;
  if (view === 'features') return `/${prefix}/epics`;
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
// active repo prefix. The URL segment is `epics` (the tab label); the
// slug itself is unchanged — it is the canonical per-repo feature key
// on the wire, in the CLI and in the sync layout.
export function featurePath(prefix: string, slug: string): string {
  return `/${prefix}/epics/${slug}`;
}

// newEpicPath is the full-screen New Epic form under the active repo
// prefix — the same relationship processEditPath has to the Pipeline.
//
// `new` is a STATIC segment at the same depth as featurePath's `:slug`,
// and react-router ranks a static segment above a dynamic one regardless
// of declaration order, so this route always wins. The hazard runs the
// other way: `store.Slugify("New")` is `"new"`, so an epic literally
// slugged `new` would emit this very path from featurePath and become
// unreachable. That is reserved CLIENT-SIDE only — the create form
// refuses the slug and NewEpicPage bounces to the detail route if such an
// epic already exists — because reserving it at the store boundary would
// also constrain `bacio feature add` and could reject an already-existing
// row on its next edit.
export function newEpicPath(prefix: string): string {
  return `/${prefix}/epics/new`;
}

// editEpicPath is the full-screen Edit Epic form for one epic. No
// collision hazard here: ValidateSlug forbids `/`, so a slug can never
// swallow the trailing segment, and the deeper path outranks the
// shallower `/:prefix/epics/:slug` unconditionally.
export function editEpicPath(prefix: string, slug: string): string {
  return `/${prefix}/epics/${slug}/edit`;
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
// view, then map the three URL aliases back to their view ids (`issues`
// → `board`, `documents` → `docs`, `epics` → `features`). Returns the
// empty string when the path doesn't match a known nav view so the
// segmented control simply shows nothing active (e.g. on an overlay
// screen or a bare prefix root).
//
// A legacy `/<prefix>/features` path falls through the default branch
// and returns `features` as itself, so the frame rendered before App's
// LegacyFeaturesRedirect fires still highlights the Epics segment
// rather than flashing an unhighlighted nav.
export function viewFromPath(pathname: string): string {
  const segs = pathname.replace(/^\/+/, '').split('/');
  const view = segs[1] ?? '';
  if (view === 'issues') return 'board';
  if (view === 'documents') return 'docs';
  if (view === 'epics') return 'features';
  return view;
}
