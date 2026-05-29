import React, { useState, useEffect, useCallback, useMemo } from 'react';
import { useNavigate, useParams } from 'react-router';
import { reportError } from '../errors';
import * as api from '../api';
import DocsFacetRail from './DocsFacetRail.jsx';
import DocsList from './DocsList.jsx';
import DocsViewer from './DocsViewer.jsx';
import { filterDocs, countFacets } from '../lib/docsFilter';
import {
  readSort, persistSort,
  readSidebarCollapsed, persistSidebarCollapsed,
  DEFAULT_SORT,
} from './DocsPersistence';
import { documentPath, viewPath } from '../lib/routes';

// DocsView (BACI-204) — desktop document browser + editor, redesigned
// as a three-pane faceted library that scales to hundreds of docs
// without forcing the user to scroll past auto-generated transcripts.
// Layout:
//   <DocsFacetRail>  — left rail with search + Type/Links/Status facets
//   <DocsList>       — middle pane with toolbar + rich-row list
//   <DocsViewer>     — right pane that wraps the existing Render/Source
//                      editor triad (NotionEditor / TranscriptView / SVG)
//
// All existing surfaces (BACI-56 SVG, BACI-125 transcript) keep
// working unchanged — DocsViewer lifts that block verbatim. The
// per-repo `sort` preference and the global `sidebarCollapsed`
// preference round-trip through DocsPersistence so a reload
// preserves the user's setup.
//
// BACI-203: the selected filename is mirrored to/from the URL via
// useParams + navigate. The decode pairs with documentPath's encode
// so a filename with dots / unusual characters survives the round-trip.
//
// BACI-219: the toolbar's Hide-transcripts checkbox is gone (the
// rail's Type tablist is the single transcript-filtering control),
// and the left rail is collapsible — when collapsed, DocsList
// renders a PanelLeftOpen affordance at the start of its toolbar to
// re-open it, and the list pane flexes into the freed space.
//
// BACI-234: the rail's collapse button now collapses BOTH the filter
// rail AND the document list in one click, leaving just the viewer
// pane. The re-open affordance lives in the viewer header (DocsViewer)
// while collapsed since the list toolbar is no longer mounted. The
// persisted preference key (`sidebarCollapsed`) is unchanged for
// back-compat — it now means "both side panels collapsed".
export default function DocsView({ activeBoard, onOpenIssue }) {
  const navigate = useNavigate();
  const { slug: slugParam } = useParams();
  const decodedSlug = slugParam ? decodeURIComponent(slugParam) : null;
  const [docs, setDocs] = useState([]);
  const [selected, setSelected] = useState(decodedSlug); // filename
  const [content, setContent] = useState('');      // live editor buffer
  const [savedContent, setSavedContent] = useState(''); // last persisted body
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [showArchived, setShowArchived] = useState(false);
  // Reload counter — bumping it triggers the doc-list reload effect
  // (e.g. after an archive toggle so the row's badge updates).
  const [reloadTick, setReloadTick] = useState(0);

  // Query bag — Type/Links/Status facets, search, sort. Mirrors the
  // DocsQuery shape in lib/docsFilter.ts.
  const [query, setQuery] = useState(() => ({
    search: '',
    type: '',
    links: 'all',
    status: 'active',
    sort: DEFAULT_SORT,
  }));

  // BACI-219: sidebar collapsed/expanded preference is global (not
  // per-repo) — matches the BACI-186 ActivityTray precedent the
  // ticket explicitly cited. Seeded by a lazy useState initialiser so
  // the first paint already reflects the user's saved preference;
  // writes go through setSidebarCollapsedPersisted below.
  const [sidebarCollapsed, setSidebarCollapsed] = useState(readSidebarCollapsed);
  const setSidebarCollapsedPersisted = useCallback((v) => {
    setSidebarCollapsed(v);
    persistSidebarCollapsed(v);
  }, []);

  const repoSelected = !!activeBoard;
  const dirty = content !== savedContent;

  // Hydrate per-repo persisted preferences when the repo changes.
  useEffect(() => {
    if (!repoSelected) return;
    setQuery((q) => ({
      ...q,
      sort: readSort(activeBoard),
    }));
  }, [activeBoard, repoSelected]);

  // Read the global display.show_archived setting once per repo flip
  // so the rail's status=all facet defers to it (matches FeaturesView).
  useEffect(() => {
    if (!repoSelected) return;
    let cancelled = false;
    (async () => {
      try {
        const prefs = await api.getDisplayPreferences();
        if (!cancelled) setShowArchived(!!prefs?.showArchived);
      } catch (_) {
        if (!cancelled) setShowArchived(false);
      }
    })();
    return () => { cancelled = true; };
  }, [activeBoard, repoSelected]);

  // BACI-203: sync `selected` from useParams on URL changes. Outbound
  // writes happen in the onSelectDoc handler via navigate(documentPath(...)).
  useEffect(() => {
    if (decodedSlug && decodedSlug !== selected) {
      setSelected(decodedSlug);
    } else if (!decodedSlug && selected) {
      setSelected(null);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [decodedSlug]);

  // Reload the document list whenever the selected repo changes, or
  // a reload tick fires. Selection / content state resets on repo
  // change but is preserved across a reload tick (archive toggle is
  // the only writer today and doesn't move the cursor).
  useEffect(() => {
    setContent('');
    setSavedContent('');
    if (!repoSelected) {
      setSelected(null);
      setContent('');
      setSavedContent('');
      setDocs([]);
      return;
    }
    api.listDocs(activeBoard)
      .then(setDocs)
      .catch(err => reportError(err, { headline: "Couldn't list docs" }));
  }, [activeBoard, repoSelected, reloadTick]);

  // BACI-215: do not blast `selected` on activeBoard change. The URL
  // is the source of truth for the open document (decodedSlug above is
  // mirrored into `selected`), so an unconditional null-out here would
  // stomp the value the URL just put in place on mount — manifesting
  // as a deep link to `/documents/<filename>` rendering the bare list.
  // The doc-list reload effect above already clears the editor buffer
  // (`content`/`savedContent`) on the repo change; the selection comes
  // back from the next render of the URL-derived initial state.
  //
  // If the slug doesn't exist in the new repo, `api.getDoc` below will
  // surface a 404 — that's a separate failure mode and the right place
  // to handle it lives there, not in a blanket clear here.

  // Load the chosen document's body.
  useEffect(() => {
    if (!selected || !repoSelected) return;
    setLoading(true);
    api.getDoc(activeBoard, selected)
      .then(doc => {
        setContent(doc.content);
        setSavedContent(doc.content);
        setLoading(false);
      })
      .catch(err => {
        reportError(err, { headline: "Couldn't load document" });
        setLoading(false);
      });
  }, [selected, activeBoard, repoSelected]);

  const save = useCallback(() => {
    if (!selected || !dirty || saving) return;
    setSaving(true);
    api.saveDoc(activeBoard, selected, content)
      .then(doc => {
        setSavedContent(doc.content);
        setSaving(false);
        // Refresh the list so the row's updatedAt + snippet reflect
        // the new body without a manual reload.
        setReloadTick((t) => t + 1);
      })
      .catch(err => {
        reportError(err, { headline: "Couldn't save document" });
        setSaving(false);
      });
  }, [activeBoard, selected, content, dirty, saving]);

  // BACI-293: Cancel from the viewer's edit mode discards the live buffer
  // by resetting it back to the last persisted body. The viewer owns the
  // edit-mode flag but can't see savedContent — only the parent holds it.
  const cancelEdit = useCallback(() => {
    setContent(savedContent);
  }, [savedContent]);

  // updateQuery wraps the setter so persistence side-effects fire for
  // the fields DocsPersistence covers, and the rest are pure state.
  const updateQuery = useCallback((patch) => {
    setQuery((q) => {
      const next = { ...q, ...patch };
      if ('sort' in patch) persistSort(activeBoard, next.sort);
      return next;
    });
  }, [activeBoard]);

  // BACI-203: navigate to the document URL so reload/deep-link work.
  // The URL change also feeds back into the decodedSlug effect to keep
  // selected in sync without a double-setState. BACI-285: the doc routes
  // are scoped to the active repo prefix.
  const onSelectDoc = useCallback((filename) => {
    navigate(filename ? documentPath(activeBoard, filename) : viewPath(activeBoard, 'docs'));
  }, [navigate, activeBoard]);

  const selectedDoc = useMemo(
    () => docs.find((d) => d.filename === selected) ?? null,
    [docs, selected],
  );

  // Run the rail + toolbar query through the pure filter helper to
  // derive the visible list + per-bucket counts.
  const { visible, counts } = useMemo(
    () => filterDocs(docs, query, showArchived),
    [docs, query, showArchived],
  );

  // The rail wants absolute per-bucket counts (so chips don't collapse
  // to zero as the user narrows) — countFacets on the full doc set.
  const railCounts = useMemo(() => countFacets(docs), [docs]);

  const archiveToggle = useCallback(async () => {
    if (!selected) return;
    try {
      if (selectedDoc?.archivedAt) {
        await api.unarchiveDocument(activeBoard, selected);
      } else {
        await api.archiveDocument(activeBoard, selected);
      }
      setReloadTick((t) => t + 1);
    } catch (err) {
      reportError(err, { headline: "Couldn't update archive state" });
    }
  }, [activeBoard, selected, selectedDoc]);

  if (!repoSelected) {
    return (
      <div className="mk-docs">
        <div className="mk-docs-empty">Select a repository to view its documents.</div>
      </div>
    );
  }

  // Silence the unused counts var (only `visible` is forwarded to the
  // list; counts powers the rail via railCounts).
  void counts;

  // BACI-234: collapsing the rail also hides the document list — both
  // side panels are gone, leaving just the viewer. The expand
  // affordance moves into the viewer header so the user can re-open
  // both panels with one click from whichever viewer state they're in.
  return (
    <div className="mk-docs">
      {!sidebarCollapsed && (
        <>
          <DocsFacetRail
            counts={railCounts}
            query={query}
            onQueryChange={updateQuery}
            onCollapse={() => setSidebarCollapsedPersisted(true)}
          />
          <DocsList
            visible={visible}
            hasDocs={docs.length > 0}
            query={query}
            onQueryChange={updateQuery}
            selected={selected}
            onSelect={onSelectDoc}
          />
        </>
      )}
      <div className="mk-docs-main">
        <DocsViewer
          activeBoard={activeBoard}
          doc={selectedDoc}
          filename={selected}
          content={content}
          loading={loading}
          saving={saving}
          dirty={dirty}
          onContentChange={setContent}
          onSave={save}
          onCancelEdit={cancelEdit}
          onArchiveToggle={selected ? archiveToggle : null}
          onOpenIssue={onOpenIssue}
          panelsCollapsed={sidebarCollapsed}
          onExpandPanels={() => setSidebarCollapsedPersisted(false)}
        />
      </div>
    </div>
  );
}
