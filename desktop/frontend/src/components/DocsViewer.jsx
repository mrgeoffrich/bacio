// DocsViewer (BACI-204) — right pane of the redesigned Documents view.
// Wraps the lifted Render/Source toggle + NotionEditor / TranscriptView /
// SVG <img> triad + the Save / Copy-source bar that used to live inline
// in DocsView. The only behaviour additions are the new header strip
// (filename + type + linked-issue / linked-feature chips + archive
// toggle) — every editor path renders identically to the pre-refactor
// surface, so the BACI-56 (SVG) and BACI-125 (transcript) flows keep
// working without a second pass.
//
// The component is purely presentational: parent owns the load/save
// state (content/dirty/saving), the live editor buffer, the doc list,
// and the archive-side reload. DocsViewer only emits callbacks.

import React, { useEffect, useMemo, useState } from 'react';
import { NotionEditor } from './editor/NotionEditor';
import TranscriptView from '../lib/transcript/TranscriptView';
import { isJsonlTranscriptDoc, isSvgDoc } from '../lib/docFormat';
import { Archive, ArchiveRestore, Link2, PanelLeftOpen } from 'lucide-react';

function typeLabel(t) {
  return t ? t.replace(/_/g, ' ') : '';
}

export default function DocsViewer({
  activeBoard,
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
}) {
  const [view, setView] = useState('render');

  // Reset the Render/Source toggle whenever a different doc is opened
  // so the user lands on the rendered surface first — same precedent
  // as the pre-refactor DocsView.
  useEffect(() => {
    setView('render');
  }, [filename]);

  const isSvg = useMemo(
    () => !!filename && isSvgDoc(filename, content || ''),
    [filename, content],
  );
  const isTranscript = useMemo(
    () => !!filename && isJsonlTranscriptDoc(filename),
    [filename],
  );
  const renderable = isSvg || isTranscript;

  // SVG Render tab — Blob URL paired with one revoke per create, same
  // shape as the pre-refactor effect (React StrictMode safety).
  const [svgUrl, setSvgUrl] = useState('');
  useEffect(() => {
    if (!isSvg) { setSvgUrl(''); return undefined; }
    const url = URL.createObjectURL(new Blob([content || ''], { type: 'image/svg+xml' }));
    setSvgUrl(url);
    return () => URL.revokeObjectURL(url);
  }, [isSvg, content]);

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
          <button
            type="button"
            className="mk-btn-primary"
            onClick={onSave}
            disabled={!dirty || saving}
          >
            {saving ? 'Saving…' : 'Save'}
          </button>
        </div>
      </header>
      {isSvg && view === 'render' ? (
        <div className="mk-docs-svg-pane">
          <img className="mk-docs-svg-img" src={svgUrl} alt={filename} />
        </div>
      ) : isTranscript && view === 'render' ? (
        <div className="mk-docs-transcript-pane">
          <TranscriptView source={content || ''} filename={filename} sizeBytes={(content || '').length} />
        </div>
      ) : (
        <div className="mk-docs-editor">
          <NotionEditor content={content || ''} onChange={onContentChange} />
        </div>
      )}
    </>
  );
}

// DocLinkChip renders one linked-issue / linked-feature pill. Clicking
// an issue chip opens the issue workspace via App.jsx's openIssueByKey
// (same path the kanban-blocked popover uses, BACI-114). Feature chips
// are rendered but not clickable — there's no "feature workspace" yet.
function DocLinkChip({ link, onOpenIssue }) {
  if (link.issueKey) {
    return (
      <button
        type="button"
        className="mk-docs-link-chip mk-docs-link-chip-issue"
        onClick={() => onOpenIssue?.(link.issueKey)}
        title={link.description ? `${link.issueKey} — ${link.description}` : link.issueKey}
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
