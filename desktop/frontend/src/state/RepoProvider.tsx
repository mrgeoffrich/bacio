import { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react';
import type { ReactNode } from 'react';
import { useLocation, useNavigate } from 'react-router';
import * as api from '../api';
import type { Board, BoardColumn, BoardCard, AddRepositoryPayload } from '../api';
import { reportError } from '../errors';
import { readLocalStorage, writeLocalStorage } from '../lib/hooks/useLocalStorage';
import { viewPath, issuePath, processEditPath, viewFromPath, prefixFromPath, repoPrefixFromKey } from '../lib/routes';
import { navFor, homeView, DEFAULT_SURFACES } from '../lib/nav';
import type { NavSurfaces } from '../lib/nav';
import { requestNavigation } from '../lib/navGuard';

// RepoProvider (BACI-361) owns the repo-selection layer App.tsx used to
// carry: the boards/columns list, the BACI-285 URL-derived active repo and
// its prefix/redirect logic, and the navigation helpers every page reaches
// for. It sits below PreferencesProvider (preferences are repo-independent)
// and above AgentsProvider / CardsProvider (both key off `activeBoard`).
//
// The active repo is URL-derived: App sits above <Routes>, so the prefix is
// parsed off `location.pathname` rather than owned as a `:prefix` route
// param. RepoProvider lives inside <BrowserRouter> (main.tsx) so it can call
// useLocation / useNavigate.

const REPO_KEY = 'bacio-active-repo'; // persisted preference: last-selected repo prefix

// BACI-285: the prefix-less page words a stale legacy link
// (`/ui/pipeline`, `/ui/issues/BACI-1`, ...) can lead with. The first path
// segment landing on one of these — instead of a known repo prefix — is
// treated as a soft-redirect (rebase under the active repo's prefix)
// rather than a hard 404 (RepoNotFound).
//
// Both `epics` (the current Epics URL) and `features` (its pre-rename
// spelling) are listed: a prefix-less legacy link is rebased under the
// active prefix here, and App's LegacyFeaturesRedirect then forwards the
// `/features` form on to `/epics`.
const LEGACY_PAGE_WORDS = new Set(['pipeline', 'issues', 'epics', 'features', 'documents', 'agents', 'history', 'monitor']);

// surfacesForPrefix resolves one space's nav-surface gates — which top-nav
// tabs it exposes — for the home-view and redirect decisions below. A
// prefix with no board yet (still loading, or unknown) falls back to
// DEFAULT_SURFACES rather than the all-off zero value, so a slow load
// never redirects away from a page that is actually available.
function surfacesForPrefix(boards: Board[], prefix: string): NavSurfaces {
  const space = boards.find(b => b.prefix === prefix);
  return space
    ? { showAgentSurfaces: space.showAgentSurfaces, showKanban: space.showKanban }
    : DEFAULT_SURFACES;
}

// legacyPageWord reports whether the URL's first segment is one of the
// recognised prefix-less page words — the signal that an unmatched prefix
// is a stale legacy link to soft-redirect rather than a genuinely-unknown
// repo to hard-404.
function legacyPageWord(first: string) {
  return LEGACY_PAGE_WORDS.has(first);
}

export type ActiveRepoContextValue = {
  boards: Board[];
  columns: BoardColumn[];
  loading: boolean;
  // BACI-285: the raw URL prefix (may be empty on bare `/`, or unknown on a
  // stale / mistyped link) and the validated active repo derived from it.
  urlPrefix: string;
  activeBoard: string;
  prefixUnknown: boolean;
  fallbackPrefix: string;
  legacyRedirectTarget: string;
  // The active view derived from the URL path (board / pipeline / agents /…).
  activeView: string;
  pickBoard: (prefix: string) => void;
  addRepository: (payload?: AddRepositoryPayload) => Promise<Board | undefined>;
  refreshBoards: () => void;
  // patchBoard merges a partial update into one board in place — the
  // optimistic-write path for per-space settings, so the nav reflects a
  // toggle in the same tick without a refetch.
  patchBoard: (prefix: string, patch: Partial<Board>) => void;
  // Navigation helpers. openCard / openIssue route to a workspace; closeIssue
  // navigates back; openProcessEditor routes to the Edit Process screen.
  openIssue: (key: string) => void;
  openCard: (card: BoardCard) => void;
  openProcessEditor: (key: string) => void;
  closeIssue: () => void;
};

const ActiveRepoContext = createContext<ActiveRepoContextValue | null>(null);

export function RepoProvider({ children }: { children: ReactNode }) {
  const navigate = useNavigate();
  const location = useLocation();

  const [boards, setBoards] = useState<Board[]>([]);
  const [columns, setColumns] = useState<BoardColumn[]>([]);
  const [loading, setLoading] = useState(true);
  // BACI-368: the repo the process was launched from. Loaded alongside the
  // boards so `loading` covers it too — Shell renders "Loading…" rather than
  // <Routes> while loading, so the bare-`/` redirect can't fire on a stale
  // fallback and flash the wrong repo.
  const [launchPrefix, setLaunchPrefix] = useState('');

  // Load the repository list + columns once on mount. A mount-time failure
  // no longer blanks the renderer (BACI-43): the modal surfaces the error,
  // the Topbar stays usable, and the views render their own empty states
  // until the data lands.
  useEffect(() => {
    // The launch-repo fetch is caught individually: an older or remote
    // server without the route 404s, which should degrade to the
    // remembered pick rather than blank the boards list.
    Promise.all([api.listBoards(), api.listColumns(), api.getLaunchRepo().catch(() => '')])
      .then(([bs, cols, launch]) => {
        setBoards(bs);
        setColumns(cols);
        setLaunchPrefix(launch);
        setLoading(false);
      })
      .catch(err => {
        reportError(err, { headline: "Couldn't load boards" });
        setLoading(false);
      });
  }, []);

  // BACI-285: the URL's first segment is the active repo prefix — the single
  // source of truth. Match it to a known board case-insensitively so a
  // lowercased shared link resolves; emit the canonical (board.prefix)
  // casing so generated URLs and the localStorage seed stay canonical. ""
  // until boards load or when the prefix is unknown.
  const urlPrefix = prefixFromPath(location.pathname);
  const matchedBoard = boards.find(b => b.prefix.toLowerCase() === urlPrefix.toLowerCase());
  const activeBoard = matchedBoard?.prefix ?? '';
  // A non-empty URL prefix that doesn't match any known board — and isn't a
  // recognised prefix-less legacy page word — is a hard 404 once boards have
  // loaded. While loading we defer (boards aren't in yet).
  const prefixUnknown =
    !loading && !!urlPrefix && !matchedBoard && !legacyPageWord(urlPrefix);
  // The repo a prefix-less / bare path should redirect to. Precedence, and
  // don't "tidy" it back (BACI-368): the repo the app was launched from wins
  // over the remembered pick — opening bacio inside a repo should land you in
  // *that* repo, not wherever you happened to be last time. The remembered
  // pick still governs when there's no launch repo (Finder / bare shell),
  // which is the ticket's explicit out-of-scope case. Match the launch prefix
  // case-insensitively and emit the canonical board.prefix casing, as the URL
  // matcher above already does.
  const fallbackPrefix = (() => {
    const launched = boards.find(b => b.prefix.toLowerCase() === launchPrefix.toLowerCase());
    if (launchPrefix && launched) return launched.prefix;
    const remembered = readLocalStorage(REPO_KEY) ?? '';
    if (boards.some(b => b.prefix === remembered)) return remembered;
    return boards[0]?.prefix ?? '';
  })();
  // BACI-285: where a bare `/` or prefix-less legacy path redirects. Bare `/`
  // → the fallback space's home view, which follows the nav surfaces that
  // space actually exposes (landing on a hidden tab would strand the user
  // with nothing highlighted). A legacy page link keeps its page path,
  // rebased under the fallback prefix. An empty fallback (no repos) lands
  // on bare `/` so the empty-state renders rather than a `//` path.
  const legacyRedirectTarget = (() => {
    if (!fallbackPrefix) return '/';
    const rest = location.pathname.replace(/^\/+/, '');
    if (rest && legacyPageWord(urlPrefix)) return `/${fallbackPrefix}/${rest}`;
    return viewPath(fallbackPrefix, homeView(surfacesForPrefix(boards, fallbackPrefix)));
  })();

  // BACI-203: derive the active view from the URL path. The Topbar reads the
  // same value (via useLocation()), so the providers and the Topbar stay in
  // lockstep without a prop ping-pong.
  const activeView = viewFromPath(location.pathname) || 'board';

  // Remember the selected repo so the app reopens on the same one.
  useEffect(() => {
    if (activeBoard) writeLocalStorage(REPO_KEY, activeBoard);
  }, [activeBoard]);

  // BACI-285: repo switch — picking a space routes to it. Land on the same
  // page in the new space where that page exists; the trailing entity
  // segment on a detail route is naturally dropped because viewPath emits
  // the list/page root only.
  //
  // "Where it exists" is now the general rule rather than the old Pipeline
  // special case: switching from any tab onto a space that doesn't expose
  // it lands on that space's home view, instead of a page with nothing
  // highlighted and no way back.
  // Both of the shell-wide navigation helpers route through
  // `requestNavigation`, which is what lets a mounted route holding unsaved
  // work (the Edit Epic page) confirm first. Guarding the two funnels here
  // covers the repo picker, the notification bell, the Shipped popover and
  // the kanban deep-links in one place rather than at each call site.
  const pickBoard = useCallback((prefix: string) => {
    if (!prefix) return;
    const surfaces = surfacesForPrefix(boards, prefix);
    const current = viewFromPath(location.pathname);
    const survives = navFor(surfaces).some(item => item.view === current);
    requestNavigation(() => navigate(viewPath(prefix, survives ? current : homeView(surfaces))));
  }, [navigate, location.pathname, boards]);

  // BACI-203: navigate-by-key for prev/next sibling jumps, the kanban
  // blocked-popover link, and the Shipped pill / notification deep-links.
  // BACI-371: route under the key's own prefix, not the active board —
  // the Shipped popover's cross-repo scope hands us keys from other
  // repos, and opening those under the current prefix would 404. A key
  // that doesn't parse falls back to the active board (the pre-BACI-371
  // behaviour), same as useNotifications' already-cross-repo deep-link.
  const openIssue = useCallback((key: string) => {
    if (!key) return;
    requestNavigation(() => navigate(issuePath(repoPrefixFromKey(key) || activeBoard, key)));
  }, [navigate, activeBoard]);

  // Open a card's workspace by routing to /issues/:key. Settings dismissal
  // (when reached from the command palette over the Settings overlay) is
  // handled by Shell, which owns the overlay flag. Guarded like its two
  // siblings above — the command palette opens over any page, including one
  // holding unsaved work.
  const openCard = useCallback((card: BoardCard) => {
    if (!card?.key) return;
    requestNavigation(() => navigate(issuePath(activeBoard, card.key)));
  }, [navigate, activeBoard]);

  // BACI-294: open the full-screen Edit Process editor for an in_pipeline
  // card's job chain.
  const openProcessEditor = useCallback((key: string) => {
    navigate(processEditPath(activeBoard, key));
  }, [navigate, activeBoard]);

  // Close the workspace: navigate back. With BrowserRouter the browser's
  // back stack handles "back to the previous view"; navigate(-1) goes one
  // step back, falling through to the repo's home board if there's nothing
  // on the back stack (a deep-link refresh). The in-progress brief /
  // descEditing tear down with the route-scoped useOpenIssue hook on unmount.
  const closeIssue = useCallback(() => {
    if (window.history.state && window.history.length > 1) {
      navigate(-1);
    } else {
      navigate(viewPath(activeBoard, homeView(surfacesForPrefix(boards, activeBoard))));
    }
  }, [navigate, activeBoard, boards]);

  // refreshBoards re-fetches the repository list (e.g. after a sync-driven
  // change). The mount load + addRepository keep `boards` seeded otherwise.
  const refreshBoards = useCallback(() => {
    api.listBoards()
      .then(setBoards)
      .catch(err => reportError(err, { headline: "Couldn't load boards" }));
  }, []);

  // patchBoard merges a partial update into one board in place. The
  // Settings per-space toggles use it to flip a nav-surface gate
  // optimistically (and to roll back on a failed write) so the topbar
  // updates in the same tick as the switch.
  //
  // refreshBoards would also work, but in web mode listBoards is N+1 —
  // an issue-list fetch per repo (see api/board.http.ts) — which is far
  // too much traffic for flipping a display preference.
  const patchBoard = useCallback((prefix: string, patch: Partial<Board>) => {
    setBoards(bs => bs.map(b => (b.prefix === prefix ? { ...b, ...patch } : b)));
  }, []);

  // Add a repository. Desktop pops a native folder picker (Wails); web mode
  // hands the path-input modal's submission through as a payload (BACI-50).
  // On success, refresh the board list and jump to the new repo; an empty
  // prefix means the user cancelled the dialog (desktop only).
  const addRepository = useCallback((payload?: AddRepositoryPayload) => {
    return api.addRepository(payload)
      .then(board => {
        if (!board.prefix) return undefined;
        return api.listBoards().then(bs => {
          setBoards(bs);
          // BACI-285: the active repo is URL-derived — route to the new
          // repo's home board so it becomes active.
          navigate(viewPath(board.prefix, homeView({
            showAgentSurfaces: board.showAgentSurfaces,
            showKanban: board.showKanban,
          })));
          return board;
        });
      })
      .catch(err => {
        reportError(err, { headline: "Couldn't add repository" });
        throw err;
      });
  }, [navigate]);

  const value = useMemo<ActiveRepoContextValue>(
    () => ({
      boards,
      columns,
      loading,
      urlPrefix,
      activeBoard,
      prefixUnknown,
      fallbackPrefix,
      legacyRedirectTarget,
      activeView,
      pickBoard,
      addRepository,
      refreshBoards,
      patchBoard,
      openIssue,
      openCard,
      openProcessEditor,
      closeIssue,
    }),
    [
      boards,
      columns,
      loading,
      urlPrefix,
      activeBoard,
      prefixUnknown,
      fallbackPrefix,
      legacyRedirectTarget,
      activeView,
      pickBoard,
      addRepository,
      refreshBoards,
      patchBoard,
      openIssue,
      openCard,
      openProcessEditor,
      closeIssue,
    ],
  );

  return <ActiveRepoContext.Provider value={value}>{children}</ActiveRepoContext.Provider>;
}

// useActiveRepo reads the repo-selection state + nav helpers. Throws when
// called outside the provider.
export function useActiveRepo(): ActiveRepoContextValue {
  const ctx = useContext(ActiveRepoContext);
  if (!ctx) throw new Error('useActiveRepo must be used within a RepoProvider');
  return ctx;
}
