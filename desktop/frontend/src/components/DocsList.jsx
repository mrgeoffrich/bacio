// DocsList (BACI-204) — middle pane of the redesigned Documents view.
// Sticky toolbar (sort dropdown, hide-transcripts checkbox, total
// count) sits over a vanilla scrolling list of rich rows. Each row
// renders the filename, type chip, linked-issue / linked-feature
// chips, a relative date, the size, and a ~140-char snippet preview
// from the body. Transcripts fold into a collapsed group at the
// bottom when `hideTranscripts` is on (the default — audit material,
// not browsing material).
//
// 327 rows × ~80 px = ~26 000 px scroll height; native scrolling is
// fine without react-window (no entry in desktop/frontend/package.json).
// If a future repo hits the tens-of-thousands the rail's facets will
// have narrowed the visible set first.

import React, { useState } from 'react';
import { ChevronDown, ChevronRight, FileText, Link2 } from 'lucide-react';
import { formatWhen } from '../lib/formatWhen';

const SORT_OPTIONS = [
  { id: 'updated', label: 'Updated' },
  { id: 'created', label: 'Created' },
  { id: 'name',    label: 'Name' },
  { id: 'size',    label: 'Size' },
];

function typeLabel(t) {
  return t ? t.replace(/_/g, ' ') : '';
}

function formatSize(n) {
  if (n == null) return '';
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}

// Snippet line truncated at ~140 chars for the row; the underlying
// store-side snippet caps at 200, so this is a per-row display trim
// rather than a hard truncation of the data.
function snippetForRow(s) {
  if (!s) return '';
  const collapsed = s.replace(/\s+/g, ' ').trim();
  if (collapsed.length <= 140) return collapsed;
  return collapsed.slice(0, 140).trimEnd() + '…';
}

export default function DocsList({
  visible,
  transcripts,
  hasDocs,
  query,
  onQueryChange,
  selected,
  onSelect,
}) {
  const [transcriptsOpen, setTranscriptsOpen] = useState(false);
  const setQuery = (patch) => onQueryChange(patch);

  return (
    <div className="mk-docs-list-pane">
      <div className="mk-docs-toolbar">
        <span className="mk-docs-toolbar-count">
          {visible.length} {visible.length === 1 ? 'document' : 'documents'}
        </span>
        <label className="mk-docs-toolbar-sort">
          <span className="mk-docs-toolbar-sort-label">Sort</span>
          <select
            className="mk-docs-toolbar-sort-select"
            value={query.sort}
            onChange={(e) => setQuery({ sort: e.target.value })}
          >
            {SORT_OPTIONS.map((o) => (
              <option key={o.id} value={o.id}>{o.label}</option>
            ))}
          </select>
        </label>
        <label className="mk-docs-toolbar-checkbox">
          <input
            type="checkbox"
            checked={query.hideTranscripts}
            onChange={(e) => setQuery({ hideTranscripts: e.target.checked })}
          />
          <span>Hide transcripts</span>
        </label>
      </div>

      <div className="mk-docs-list">
        {!hasDocs ? (
          <div className="mk-docs-list-empty">No documents in this repository.</div>
        ) : visible.length === 0 && transcripts.length === 0 ? (
          <div className="mk-docs-list-empty">No documents match the current filters.</div>
        ) : (
          <>
            {visible.map((d) => (
              <DocRow
                key={d.filename}
                doc={d}
                active={selected === d.filename}
                onSelect={() => onSelect(d.filename)}
              />
            ))}
            {transcripts.length > 0 && (
              <div className="mk-docs-transcript-fold">
                <button
                  type="button"
                  className="mk-docs-transcript-fold-toggle"
                  onClick={() => setTranscriptsOpen((o) => !o)}
                  aria-expanded={transcriptsOpen}
                >
                  {transcriptsOpen
                    ? <ChevronDown size={14} strokeWidth={2} aria-hidden="true" />
                    : <ChevronRight size={14} strokeWidth={2} aria-hidden="true" />}
                  <span>
                    {transcripts.length} transcript{transcripts.length === 1 ? '' : 's'}
                  </span>
                </button>
                {transcriptsOpen && transcripts.map((d) => (
                  <DocRow
                    key={d.filename}
                    doc={d}
                    active={selected === d.filename}
                    onSelect={() => onSelect(d.filename)}
                  />
                ))}
              </div>
            )}
          </>
        )}
      </div>
    </div>
  );
}

function DocRow({ doc, active, onSelect }) {
  const links = doc.links ?? [];
  const archived = !!doc.archivedAt;
  return (
    <button
      type="button"
      className={`mk-docs-row ${active ? 'is-active' : ''} ${archived ? 'is-archived' : ''}`}
      onClick={onSelect}
    >
      <div className="mk-docs-row-line1">
        <FileText size={14} strokeWidth={2} aria-hidden="true" className="mk-docs-row-icon" />
        <span className="mk-docs-row-name">{doc.filename}</span>
        <span className="mk-docs-row-type">{typeLabel(doc.type)}</span>
      </div>
      {(links.length > 0 || doc.snippet) && (
        <div className="mk-docs-row-line2">
          {links.length > 0 && (
            <span className="mk-docs-row-links">
              {links.slice(0, 3).map((l, i) => (
                <span key={i} className="mk-docs-row-link-chip">
                  <Link2 size={10} strokeWidth={2} aria-hidden="true" />
                  <span>{l.issueKey || l.featureSlug}</span>
                </span>
              ))}
              {links.length > 3 && <span className="mk-docs-row-link-more">+{links.length - 3}</span>}
            </span>
          )}
          {doc.snippet && (
            <span className="mk-docs-row-snippet">{snippetForRow(doc.snippet)}</span>
          )}
        </div>
      )}
      <div className="mk-docs-row-line3">
        <span className="mk-docs-row-when">{doc.updatedAt ? formatWhen(doc.updatedAt) : ''}</span>
        <span className="mk-docs-row-size">{formatSize(doc.sizeBytes)}</span>
        {archived && <span className="mk-docs-archived-badge">Archived</span>}
      </div>
    </button>
  );
}
