import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useNavigate, useParams } from 'react-router';
import { reportError } from '../errors';
import * as api from '../api';
import type { DocContent, DocFolder, DocSummary, DisplayPreferencesDTO, RepoKind } from '../api';
import DocsViewer from './DocsViewer';
import DocsTreeRail from './docs/DocsTreeRail';
import type { RailMode } from './docs/DocsTreeRail';
import DocsFolderPage from './docs/DocsFolderPage';
import DocsNameDialog from './docs/DocsNameDialog';
import DocsDeleteDialog from './docs/DocsDeleteDialog';
import { useDocsTree } from './docs/useDocsTree';
import { useDocsActions } from './docs/useDocsActions';
import { useDocsTreeDrag } from './docs/useDocsTreeDrag';
import type { DocsDragItem, DocsDropTarget } from './docs/useDocsTreeDrag';
import type { DocsTreeActions } from './docs/DocsTreeNode';
import type { Crumb, Peer } from './docs/DocsNav';
import { countFacets, filterDocs, findFolderNode, folderAncestry, shouldFlatten } from '../lib/docsFilter';
import type { Doc, DocsQuery } from '../lib/docsFilter';
import {
  readSort, persistSort,
  SIDEBAR_COLLAPSED_KEY, sidebarCollapsedCodec,
  DEFAULT_SORT,
} from './DocsPersistence';
import { useLocalStorage } from '../lib/hooks/useLocalStorage';
import { useAsyncResource } from '../lib/hooks/useAsyncResource';
import { useActiveRepo } from '../state/RepoProvider';
import { documentPath, viewPath } from '../lib/routes';

// DocsView — the Documents page. Two panes: a page tree on the left, the
// editor on the right.
//
// The pivot replaced the three-pane faceted library (BACI-204's facet rail →
// document list → viewer) with the "Tree Rail" design. The tree is now the
// unambiguous navigator, and the 296px of chrome it costs — against the old
// 654px — is what lets the TipTap canvas sit at its designed 900px measure
// with air either side. Everything below the layout survived:
//
//   • DocsList is whole, as the body of the rail's flat mode;
//   • DocsFacetRail is demoted into a fold at the rail's foot, plus a new
//     Space group;
//   • DocsViewer keeps its whole Render/Source + TipTap + SVG/HTML triad —
//     only its header changed (breadcrumb + peer jump instead of the bare
//     filename);
//   • filterDocs / countFacets / DocsQuery are untouched and still the one
//     ranking path, which is exactly what makes flatten-on-filter cheap.
//
// **Flatten-on-filter** is the reason a tree is viable here at all. This
// repo has 208 auto-generated session retros; a tree is the wrong shape for
// those. Typing in the rail search or activating any facet auto-flips the
// body to the flat ranked list and clearing flips it back, with the
// `Tree | All docs` switch as a manual override (see `railMode` below).
//
// **Selecting a folder is never a dead end** — it opens DocsFolderPage, a
// real page in the editor pane carrying a live children index. That is the
// difference between Confluence and a file browser.
//
// The view was also the last un-modernised one in the tree: it hand-rolled
// the banned `useState + useEffect(fetch) + .catch(reportError)` triad four
// times over. All four are now `useAsyncResource` (BACI-356), and the boards
// list comes from `useActiveRepo()` rather than a prop drill.
//
// BACI-203: the selected filename is mirrored to/from the URL via
// useParams + navigate. Folder selection is deliberately NOT in the URL —
// the doc routes are keyed by filename, and a folder has no slug of its own.
//
// BACI-234: the collapse control hides the rail (one pane now, not two); the
// re-open affordance lives in the viewer / folder-page header. The persisted
// key (`sidebarCollapsed`) is unchanged for back-compat.
type DocsViewProps = {
  activeBoard: string;
  onOpenIssue: (key: string) => void;
};

export default function DocsView({ activeBoard, onOpenIssue }: DocsViewProps) {
  const navigate = useNavigate();
  const { slug: slugParam } = useParams();
  const decodedSlug = slugParam ? decodeURIComponent(slugParam) : null;
  // Global repo state, read from the provider rather than prop-drilled: the
  // Space facet group lists every space and picking one navigates there.
  const { boards, pickBoard } = useActiveRepo();

  const [selected, setSelected] = useState<string | null>(decodedSlug); // filename
  // The selected FOLDER (uuid), mutually exclusive with `selected`: the
  // editor pane shows either a page or a folder page, never both.
  const [selectedFolder, setSelectedFolder] = useState<string | null>(null);
  const [content, setContent] = useState('');      // live editor buffer
  const [savedContent, setSavedContent] = useState(''); // last persisted body
  const [saving, setSaving] = useState(false);
  // Manual override for the rail body; null = follow shouldFlatten(query).
  const [modeOverride, setModeOverride] = useState<RailMode | null>(null);

  // Query bag — Type/Links/Status facets, search, sort. Mirrors the
  // DocsQuery shape in lib/docsFilter.ts.
  const [query, setQuery] = useState<DocsQuery>(() => ({
    search: '',
    type: '',
    links: 'all',
    status: 'active',
    sort: DEFAULT_SORT,
  }));

  // BACI-219: sidebar collapsed/expanded preference is global (not
  // per-repo) — matches the BACI-186 ActivityTray precedent. useLocalStorage
  // (BACI-355) seeds the first paint from the saved preference and persists
  // every change, with the '1'/'0' on-disk format preserved by
  // sidebarCollapsedCodec.
  const [sidebarCollapsed, setSidebarCollapsed] = useLocalStorage<boolean>(
    SIDEBAR_COLLAPSED_KEY,
    false,
    sidebarCollapsedCodec,
  );

  const repoSelected = !!activeBoard;
  const dirty = content !== savedContent;

  // ─── Resources ────────────────────────────────────────────────────────
  // Four loads, all on useAsyncResource so the stale-load guard, the
  // loading flag and the error policy live in one place each.

  const docsRes = useAsyncResource<DocSummary[]>(
    () => api.listDocs(activeBoard),
    [],
    [activeBoard],
    { enabled: repoSelected, errorHeadline: "Couldn't list docs" },
  );

  const foldersRes = useAsyncResource<DocFolder[]>(
    () => api.listDocFolders(activeBoard),
    [],
    [activeBoard],
    { enabled: repoSelected, errorHeadline: "Couldn't list document folders" },
  );

  // The global display.show_archived setting the Status facet's `all` mode
  // defers to. A failure is swallowed to `false` rather than surfaced —
  // preserving the pre-pivot behaviour, where an unavailable preference
  // simply meant "don't show archived" instead of a modal on page open.
  const prefsRes = useAsyncResource<DisplayPreferencesDTO | null>(
    () => api.getDisplayPreferences(),
    null,
    [activeBoard],
    { enabled: repoSelected, onError: () => { /* best-effort: default to hiding archived */ } },
  );
  const showArchived = !!prefsRes.data?.showArchived;

  const docRes = useAsyncResource<DocContent | null>(
    () => (selected ? api.getDoc(activeBoard, selected) : Promise.resolve(null)),
    null,
    [activeBoard, selected],
    { enabled: repoSelected && !!selected, errorHeadline: "Couldn't load document" },
  );

  const docs = docsRes.data;
  const folders = foldersRes.data;

  const refreshDocs = docsRes.refresh;
  const refreshFolders = foldersRes.refresh;

  // Hydrate the per-repo sort preference when the repo changes.
  useEffect(() => {
    if (!repoSelected) return;
    setQuery((q) => ({ ...q, sort: readSort(activeBoard) }));
  }, [activeBoard, repoSelected]);

  // BACI-203: sync `selected` from useParams on URL changes. Outbound
  // writes happen in openDoc via navigate(documentPath(...)). Opening a page
  // also clears any folder selection — the two are mutually exclusive.
  useEffect(() => {
    if (decodedSlug && decodedSlug !== selected) {
      setSelected(decodedSlug);
      setSelectedFolder(null);
    } else if (!decodedSlug && selected) {
      setSelected(null);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [decodedSlug]);

  // Seed the editable buffer from the loaded body. The filename check is the
  // guard that matters: while a newly-selected page is still in flight,
  // docRes.data still holds the PREVIOUS page, and seeding that would show
  // one document's body under another's name the moment loading cleared.
  useEffect(() => {
    if (!selected) {
      setContent('');
      setSavedContent('');
      return;
    }
    const loaded = docRes.data;
    if (!loaded || loaded.filename !== selected) return;
    setContent(loaded.content);
    setSavedContent(loaded.content);
  }, [selected, docRes.data]);

  // BACI-215: do not blast `selected` on an activeBoard change. The URL is
  // the source of truth for the open document, so an unconditional null-out
  // here would stomp the value the URL just put in place on mount —
  // manifesting as a deep link to `/documents/<filename>` rendering the bare
  // tree. If the slug doesn't exist in the new repo, api.getDoc surfaces a
  // 404, which is the right place to handle it.

  // ─── Selection & navigation ───────────────────────────────────────────

  // BACI-203: navigate to the document URL so reload / deep-link work. The
  // URL change feeds back into the decodedSlug effect. BACI-285: the doc
  // routes are scoped to the active repo prefix.
  const openDoc = useCallback((filename: string) => {
    setSelectedFolder(null);
    navigate(filename ? documentPath(activeBoard, filename) : viewPath(activeBoard, 'docs'));
  }, [navigate, activeBoard]);

  const selectFolder = useCallback((uuid: string) => {
    setSelectedFolder(uuid);
    // Drop the doc slug from the URL — the folder page is now what's open.
    if (decodedSlug) navigate(viewPath(activeBoard, 'docs'));
  }, [activeBoard, decodedSlug, navigate]);

  const clearSelection = useCallback(() => {
    setSelectedFolder(null);
    navigate(viewPath(activeBoard, 'docs'));
  }, [activeBoard, navigate]);

  // ─── Derived: filter → tree ───────────────────────────────────────────

  // The rail + facet query runs through the same pure filter helper it
  // always has; the tree is then a GROUPING of that same result, so there is
  // only ever one index to reason about (and status / show-archived
  // handling is inherited rather than reimplemented).
  const { visible } = useMemo(
    () => filterDocs(docs, query, showArchived),
    [docs, query, showArchived],
  );

  // The fold wants absolute per-bucket counts (so chips don't collapse to
  // zero as the user narrows) — countFacets on the full doc set.
  const railCounts = useMemo(() => countFacets(docs), [docs]);

  const { tree, treeDocs, expanded, toggleFolder, expandFolders } = useDocsTree({
    repo: activeBoard,
    docs: visible,
    folders,
    selectedDoc: selected,
    selectedFolder,
  });

  // Flatten-on-filter. `autoMode` is the derived answer; `modeOverride` is
  // the user overruling it from the segmented switch. The override is
  // dropped whenever the derived answer CHANGES, so the switch overrules the
  // current situation rather than pinning the rail forever — type a search
  // and you get results, clear it and the tree comes back.
  const autoMode: RailMode = shouldFlatten(query) ? 'flat' : 'tree';
  const prevAutoRef = useRef(autoMode);
  useEffect(() => {
    if (prevAutoRef.current === autoMode) return;
    prevAutoRef.current = autoMode;
    setModeOverride(null);
  }, [autoMode]);
  const railMode: RailMode = modeOverride ?? autoMode;

  const selectedDoc = useMemo(
    () => docs.find((d) => d.filename === selected) ?? null,
    [docs, selected],
  );

  // A folder can go missing under you (deleted in another window, or its
  // whole branch filtered away) — fall back to the page surface rather than
  // rendering a folder page for a folder that isn't there.
  const folderNode = useMemo(
    () => (selectedFolder ? findFolderNode(tree, selectedFolder) : null),
    [tree, selectedFolder],
  );

  // ─── Mutations ────────────────────────────────────────────────────────

  const actions = useDocsActions({
    repo: activeBoard,
    folders,
    refreshDocs,
    refreshFolders,
    openDoc,
    clearSelection,
    selectFolder,
    selectedDoc: selected,
    selectedFolder,
    expandFolders,
  });

  const onDropMove = useCallback((item: DocsDragItem, target: DocsDropTarget) => {
    if (item.kind === 'doc') {
      // `into` appends (position null); `before` takes the target row's
      // slot, which is a sort key rather than a dense index — siblings may
      // share one and the listing tie-breaks on filename.
      actions.moveDoc(item.filename, target.folderUuid, target.kind === 'into' ? null : target.index);
      return;
    }
    // Folders only ever re-parent: the seam has no sibling-order mutator for
    // them (moveDocFolder takes a new parent, nothing else).
    if (target.kind === 'into') actions.moveFolder(item.uuid, target.folderUuid);
  }, [actions]);

  const drag = useDocsTreeDrag({ folders, onMove: onDropMove });

  const save = useCallback(() => {
    if (!selected || !dirty || saving) return;
    setSaving(true);
    api.saveDoc(activeBoard, selected, content)
      .then(doc => {
        setSavedContent(doc.content);
        setSaving(false);
        // Refresh the list so the row's updatedAt + snippet reflect the new
        // body without a manual reload.
        refreshDocs();
      })
      .catch(err => {
        reportError(err, { headline: "Couldn't save document" });
        setSaving(false);
      });
  }, [activeBoard, selected, content, dirty, saving, refreshDocs]);

  // BACI-293: Cancel from the viewer's edit mode discards the live buffer by
  // resetting it back to the last persisted body.
  const cancelEdit = useCallback(() => {
    setContent(savedContent);
  }, [savedContent]);

  const archiveToggle = useCallback(async () => {
    if (!selected) return;
    try {
      if (selectedDoc?.archivedAt) {
        await api.unarchiveDocument(activeBoard, selected);
      } else {
        await api.archiveDocument(activeBoard, selected);
      }
      refreshDocs();
    } catch (err) {
      reportError(err, { headline: "Couldn't update archive state" });
    }
  }, [activeBoard, selected, selectedDoc, refreshDocs]);

  // updateQuery wraps the setter so persistence side-effects fire for the
  // fields DocsPersistence covers, and the rest are pure state.
  const updateQuery = useCallback((patch: Partial<DocsQuery>) => {
    setQuery((q) => {
      const next = { ...q, ...patch };
      if ('sort' in patch) persistSort(activeBoard, next.sort);
      return next;
    });
  }, [activeBoard]);

  // ─── Derived: chrome ──────────────────────────────────────────────────

  const activeSpace = useMemo(
    () => boards.find(b => b.prefix === activeBoard) ?? null,
    [boards, activeBoard],
  );
  const spaces = useMemo(
    () => boards.map(b => ({ prefix: b.prefix, name: b.name, kind: b.kind })),
    [boards],
  );

  // Where a rail-level "New page" / "New folder" lands: inside whatever is
  // open. Creating from the foot button while reading a page in Architecture
  // should put the new page in Architecture, not at the root.
  const contextFolder = selectedFolder ?? selectedDoc?.folderUuid ?? '';

  // Peer navigation walks the rail's own render order, so "next" always
  // means "the next row you can see" — including across a folder boundary in
  // tree mode, and down the ranked list in flat mode.
  const peerOrder: Doc[] = railMode === 'tree' ? treeDocs : visible;
  const peerIndex = selected ? peerOrder.findIndex(d => d.filename === selected) : -1;
  const peerPrev: Peer = peerIndex > 0
    ? { label: peerOrder[peerIndex - 1].filename, onSelect: () => openDoc(peerOrder[peerIndex - 1].filename) }
    : null;
  const peerNext: Peer = peerIndex >= 0 && peerIndex < peerOrder.length - 1
    ? { label: peerOrder[peerIndex + 1].filename, onSelect: () => openDoc(peerOrder[peerIndex + 1].filename) }
    : null;

  const spaceCrumb = useCallback((): Crumb => ({
    key: 'space',
    label: activeSpace?.name || activeBoard,
    onClick: clearSelection,
  }), [activeSpace, activeBoard, clearSelection]);

  const docCrumbs = useMemo<Crumb[]>(() => {
    const out: Crumb[] = [spaceCrumb()];
    for (const f of folderAncestry(folders, selectedDoc?.folderUuid ?? '')) {
      out.push({ key: f.uuid, label: f.name, onClick: () => selectFolder(f.uuid) });
    }
    out.push({ key: 'here', label: selected ?? '', mono: true });
    return out;
  }, [folders, selectedDoc, selected, selectFolder, spaceCrumb]);

  const folderCrumbs = useMemo<Crumb[]>(() => {
    const out: Crumb[] = [spaceCrumb()];
    const chain = folderAncestry(folders, selectedFolder ?? '');
    chain.forEach((f, i) => {
      const last = i === chain.length - 1;
      out.push({
        key: f.uuid,
        label: f.name,
        onClick: last ? undefined : () => selectFolder(f.uuid),
      });
    });
    return out;
  }, [folders, selectedFolder, selectFolder, spaceCrumb]);

  const treeActions = useMemo<DocsTreeActions>(() => ({
    onSelectDoc: openDoc,
    onSelectFolder: selectFolder,
    onNewPage: actions.requestNewPage,
    onNewFolder: actions.requestNewFolder,
    onRenameDoc: actions.requestRenameDoc,
    onDeleteDoc: actions.requestDeleteDoc,
    onRenameFolder: actions.requestRenameFolder,
    onDeleteFolder: actions.requestDeleteFolder,
    onMoveDocToRoot: (filename: string) => actions.moveDoc(filename, '', null),
  }), [openDoc, selectFolder, actions]);

  if (!repoSelected) {
    return (
      <div className="mk-docs">
        <div className="mk-docs-empty">Select a repository to view its documents.</div>
      </div>
    );
  }

  return (
    <div className="mk-docs">
      {!sidebarCollapsed && (
        <DocsTreeRail
          onNewPage={() => actions.requestNewPage(contextFolder)}
          onNewFolder={() => actions.requestNewFolder(contextFolder)}
          onCollapse={() => setSidebarCollapsed(true)}
          query={query}
          onQueryChange={updateQuery}
          mode={railMode}
          onModeChange={setModeOverride}
          totalDocs={railCounts.total}
          spaceName={activeSpace?.name || activeBoard}
          spaceKind={activeSpace?.kind ?? ('git' as RepoKind)}
          tree={tree}
          expanded={expanded}
          onToggleFolder={toggleFolder}
          selectedDoc={selected}
          selectedFolder={selectedFolder}
          actions={treeActions}
          drag={drag}
          visible={visible}
          hasDocs={docs.length > 0}
          counts={railCounts}
          spaces={spaces}
          activeSpace={activeBoard}
          onPickSpace={pickBoard}
        />
      )}
      <div className="mk-docs-main">
        {folderNode ? (
          <DocsFolderPage
            folderUuid={folderNode.uuid}
            folderName={folderNode.name}
            crumbs={folderCrumbs}
            nodes={folderNode.children}
            onSelectDoc={openDoc}
            onSelectFolder={selectFolder}
            onNewPage={actions.requestNewPage}
            onNewFolder={actions.requestNewFolder}
            onRenameFolder={actions.requestRenameFolder}
            onDeleteFolder={actions.requestDeleteFolder}
            onOpenIssue={onOpenIssue}
            panelsCollapsed={sidebarCollapsed}
            onExpandPanels={() => setSidebarCollapsed(false)}
          />
        ) : (
          <DocsViewer
            activeBoard={activeBoard}
            doc={selectedDoc}
            filename={selected}
            content={content}
            loading={!!selected && docRes.loading}
            saving={saving}
            dirty={dirty}
            onContentChange={setContent}
            onSave={save}
            onCancelEdit={cancelEdit}
            onArchiveToggle={selected ? archiveToggle : null}
            onOpenIssue={onOpenIssue}
            panelsCollapsed={sidebarCollapsed}
            onExpandPanels={() => setSidebarCollapsed(false)}
            crumbs={docCrumbs}
            peerPrev={peerPrev}
            peerNext={peerNext}
          />
        )}
      </div>

      <DocsNameDialog {...actions.nameDialog} />
      <DocsDeleteDialog {...actions.deleteDialog} />
    </div>
  );
}
