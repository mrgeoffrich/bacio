// DocsViewer (BACI-204) — right pane of the redesigned Documents view.
// Wraps the lifted Render/Source toggle + NotionEditor / SVG <img> /
// HTML <iframe> set + the Save / Copy-source bar that used to live inline
// in DocsView. The only behaviour additions are the new header strip
// (filename + type + linked-issue / linked-feature chips + archive
// toggle) — every editor path renders identically to the pre-refactor
// surface, so the BACI-56 (SVG) flow keeps working without a second pass.
// (BACI-307 retired the .jsonl-transcript render branch; legacy
// transcript docs now open as plain source.)
//
// The component is purely presentational: parent owns the load/save
// state (content/dirty/saving), the live editor buffer, the doc list,
// and the archive-side reload. DocsViewer only emits callbacks.

import { useEffect, useMemo, useState } from 'react';
import { NotionEditor } from './editor/NotionEditor';
import { isHtmlDoc, isSvgDoc } from '../lib/docFormat';
import { Archive, ArchiveRestore, Link2, PanelLeftOpen } from 'lucide-react';
import type { DocSummary } from '../api';

// One linked-issue / linked-feature row on a DocSummary. Derived from
// the api seam's DocSummary so the chip rendering stays in lockstep with
// the transport DTO (DocSummaryLink isn't separately re-exported).
type DocSummaryLink = NonNullable<DocSummary['links']>[number];

function typeLabel(t: string | undefined): string {
  return t ? t.replace(/_/g, ' ') : '';
}

type DocsViewerProps = {
  // activeBoard is forwarded by DocsView but not consumed here; kept on
  // the prop type so the call site stays valid.
  activeBoard: string;
  doc: DocSummary | null;
  filename: string | null;
  content: string;
  loading: boolean;
  saving: boolean;
  dirty: boolean;
  onContentChange: (content: string) => void;
  onSave: () => void;
  onArchiveToggle: (() => void) | null;
  onOpenIssue: (issueKey: string) => void;
  panelsCollapsed: boolean; // BACI-234: rail + list are hidden; render expand button
  onExpandPanels: () => void; // re-open both side panels
  onCancelEdit: () => void; // BACI-293: parent resets the lifted buffer to the loaded doc
};

export default function DocsViewer({
  doc,            // DocSummary row for the selected doc (carries links + archivedAt)
  filename,
  content,
  loading,
  saving,
  dirty,
  onContentChange,
  onSave,
  onArchiveToggle,
  onOpenIssue,    // (issueKey) => void — from App.jsx
  panelsCollapsed, // BACI-234: rail + list are hidden; render expand button
  onExpandPanels,  // () => void — re-open both side panels
  onCancelEdit,    // BACI-293: () => void — parent resets the lifted buffer to the loaded doc
}: DocsViewerProps) {
  const [view, setView] = useState('render');
  // BACI-293: a markdown doc lands read-only; Edit flips this true so the
  // editor becomes editable (toolbar/bubble menu appear) and the action
  // bar swaps Save → Cancel + Save.
  const [editMode, setEditMode] = useState(false);

  // Reset the Render/Source toggle and the edit-mode flag whenever a
  // different doc is opened so the user lands on the rendered, read-only
  // surface first — same precedent as the pre-refactor DocsView.
  useEffect(() => {
    setView('render');
    setEditMode(false);
  }, [filename]);

  const startEdit = () => setEditMode(true);
  // Cancel discards the live buffer (parent resets it back to the loaded
  // doc) and returns to read-only without persisting.
  const cancelEdit = () => {
    onCancelEdit?.();
    setEditMode(false);
  };
  // Save is fire-and-forget from here (parent owns the saving flag and
  // surfaces errors). Returning to read-only is correct in both the
  // happy path (content === savedContent after the save resolves) and
  // the no-op path (!dirty); a failed save leaves the buffer dirty so the
  // user can click Edit again to retry — no data is lost.
  const handleSave = () => {
    onSave?.();
    setEditMode(false);
  };

  const isSvg = useMemo(
    () => !!filename && isSvgDoc(filename, content || ''),
    [filename, content],
  );
  const isHtml = useMemo(
    () => !!filename && isHtmlDoc(filename, content || ''),
    [filename, content],
  );
  const renderable = isSvg || isHtml;

  // SVG Render tab — Blob URL paired with one revoke per create, same
  // shape as the pre-refactor effect (React StrictMode safety).
  const [svgUrl, setSvgUrl] = useState('');
  useEffect(() => {
    if (!isSvg) { setSvgUrl(''); return undefined; }
    const url = URL.createObjectURL(new Blob([content || ''], { type: 'image/svg+xml' }));
    setSvgUrl(url);
    return () => URL.revokeObjectURL(url);
  }, [isSvg, content]);

  // HTML Render tab (BACI-298) — same Blob-URL create/revoke lifecycle as
  // the SVG path, but a `text/html` Blob fed to a sandboxed <iframe>
  // (an <img> can't render an HTML document).
  const [htmlUrl, setHtmlUrl] = useState('');
  useEffect(() => {
    if (!isHtml) { setHtmlUrl(''); return undefined; }
    const url = URL.createObjectURL(new Blob([content || ''], { type: 'text/html' }));
    setHtmlUrl(url);
    return () => URL.revokeObjectURL(url);
  }, [isHtml, content]);

  const copySource = () => {
    if (!content) return;
    try { navigator.clipboard?.writeText(content); } catch (_) { /* clipboard can be blocked */ }
  };

  // BACI-234: while both side panels are collapsed, the viewer is the
  // only host for the re-open affordance — render it as the first child
  // of the header (and in the empty / loading states) so the user can
  // always get back to the rail + list with one click.
  const expandButton = panelsCollapsed && onExpandPanels ? (
    <button
      type="button"
      className="mk-icbtn mk-docs-panels-expand"
      onClick={onExpandPanels}
      title="Show filter sidebar and document list"
      aria-label="Show filter sidebar and document list"
    >
      <PanelLeftOpen size={14} strokeWidth={2} aria-hidden="true" />
    </button>
  ) : null;

  if (!filename) {
    return (
      <div className="mk-docs-empty-wrap">
        {expandButton && (
          <div className="mk-docs-empty-toolbar">{expandButton}</div>
        )}
        <div className="mk-docs-empty">Pick a document to start editing.</div>
      </div>
    );
  }
  if (loading) {
    return (
      <div className="mk-docs-empty-wrap">
        {expandButton && (
          <div className="mk-docs-empty-toolbar">{expandButton}</div>
        )}
        <div className="mk-docs-empty">Loading…</div>
      </div>
    );
  }

  const archived = !!doc?.archivedAt;
  const links = doc?.links ?? [];

  return (
    <>
      <header className="mk-docs-viewer-header">
        {expandButton}
        <div className="mk-docs-viewer-header-meta">
          <span className="mk-docs-bar-name">{filename}</span>
          {doc?.type && (
            <span className="mk-docs-item-type">{typeLabel(doc.type)}</span>
          )}
          {archived && (
            <span className="mk-docs-archived-badge" title="Archived">Archived</span>
          )}
          {links.length > 0 && (
            <span className="mk-docs-viewer-links">
              {links.map((l, i) => (
                <DocLinkChip
                  key={i}
                  link={l}
                  onOpenIssue={onOpenIssue}
                />
              ))}
            </span>
          )}
        </div>
        {renderable && (
          <div className="mk-docs-tabs" role="tablist" aria-label="View mode">
            <button
              role="tab"
              aria-selected={view === 'render'}
              className={`mk-docs-tab ${view === 'render' ? 'is-active' : ''}`}
              onClick={() => setView('render')}
            >
              Render
            </button>
            <button
              role="tab"
              aria-selected={view === 'source'}
              className={`mk-docs-tab ${view === 'source' ? 'is-active' : ''}`}
              onClick={() => setView('source')}
            >
              Source
            </button>
          </div>
        )}
        <span className="mk-docs-bar-status">{dirty ? 'Unsaved changes' : 'Saved'}</span>
        <div className="mk-docs-bar-actions">
          {renderable && (
            <button
              type="button"
              className="mk-btn-secondary"
              onClick={copySource}
              title="Copy source to clipboard"
            >
              Copy source
            </button>
          )}
          {onArchiveToggle && (
            <button
              type="button"
              className="mk-btn-secondary"
              onClick={onArchiveToggle}
              title={archived ? 'Unarchive document' : 'Archive document'}
            >
              {archived ? (
                <>
                  <ArchiveRestore size={14} strokeWidth={2} aria-hidden="true" />
                  <span>Unarchive</span>
                </>
              ) : (
                <>
                  <Archive size={14} strokeWidth={2} aria-hidden="true" />
                  <span>Archive</span>
                </>
              )}
            </button>
          )}
          {!renderable && (
            editMode ? (
              <>
                <button
                  type="button"
                  className="mk-btn-secondary"
                  onClick={cancelEdit}
                  disabled={saving}
                >
                  Cancel
                </button>
                <button
                  type="button"
                  className="mk-btn-primary"
                  onClick={handleSave}
                  disabled={!dirty || saving}
                >
                  {saving ? 'Saving…' : 'Save'}
                </button>
              </>
            ) : (
              <button
                type="button"
                className="mk-btn-primary"
                onClick={startEdit}
              >
                Edit
              </button>
            )
          )}
        </div>
      </header>
      {isHtml && view === 'render' ? (
        <div className="mk-docs-svg-pane">
          {/* sandbox="" — no allow-scripts / allow-same-origin: static
              HTML+CSS wireframes render but no script runs and the frame
              can't reach bacio's origin/cookies/storage (BACI-298). */}
          <iframe className="mk-docs-html-frame" src={htmlUrl} sandbox="" title={filename} />
        </div>
      ) : isSvg && view === 'render' ? (
        <div className="mk-docs-svg-pane">
          <img className="mk-docs-svg-img" src={svgUrl} alt={filename} />
        </div>
      ) : (
        <div className="mk-docs-editor">
          <NotionEditor content={content || ''} onChange={onContentChange} readOnly={!editMode} />
        </div>
      )}
    </>
  );
}

// DocLinkChip renders one linked-issue / linked-feature pill. Clicking
// an issue chip opens the issue workspace via App.jsx's openIssueByKey
// (same path the kanban-blocked popover uses, BACI-114). Feature chips
// are rendered but not clickable — there's no "feature workspace" yet.
type DocLinkChipProps = {
  link: DocSummaryLink;
  onOpenIssue: (issueKey: string) => void;
};

function DocLinkChip({ link, onOpenIssue }: DocLinkChipProps) {
  const { issueKey } = link;
  if (issueKey) {
    return (
      <button
        type="button"
        className="mk-docs-link-chip mk-docs-link-chip-issue"
        onClick={() => onOpenIssue?.(issueKey)}
        title={link.description ? `${issueKey} — ${link.description}` : issueKey}
      >
        <Link2 size={12} strokeWidth={2} aria-hidden="true" />
        <span>{link.issueKey}</span>
      </button>
    );
  }
  if (link.featureSlug) {
    return (
      <span
        className="mk-docs-link-chip mk-docs-link-chip-feature"
        title={link.description ? `feature/${link.featureSlug} — ${link.description}` : `feature/${link.featureSlug}`}
      >
        <Link2 size={12} strokeWidth={2} aria-hidden="true" />
        <span>{link.featureSlug}</span>
      </span>
    );
  }
  return null;
}
