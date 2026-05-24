import React, { useState, useEffect, useCallback, useMemo } from 'react';
import { NotionEditor } from './editor/NotionEditor';
import TranscriptView from '../lib/transcript/TranscriptView';
import { reportError } from '../errors';
import { isJsonlTranscriptDoc, isSvgDoc } from '../lib/docFormat';
import * as api from '../api';

// Human label for a bacio document-type enum value.
function typeLabel(t) {
  return t.replace(/_/g, ' ');
}

// DocsView is the desktop document browser + editor. Documents are per-repo.
// Edits are buffered locally and persisted explicitly via the Save button.
//
// SVG docs render via a Render/Source toggle (BACI-56). The Render tab
// shows an <img> backed by a Blob object URL — browsers don't execute
// JavaScript in <img>-loaded SVGs, so embedded <script> and event
// handlers are inert without needing DOMPurify. The Source tab is the
// existing NotionEditor for raw text editing.
//
// Subagent JSONL transcripts (BACI-125) follow the same Render/Source
// pattern — Render mounts <TranscriptView>, Source falls back to the
// NotionEditor so the raw JSONL is one click away if rendering can't
// cope with a malformed line.
//
// Non-SVG, non-transcript docs see no UI change.
export default function DocsView({ activeBoard }) {
  const [docs, setDocs] = useState([]);
  const [selected, setSelected] = useState(null); // filename
  const [content, setContent] = useState('');      // live editor buffer
  const [savedContent, setSavedContent] = useState(''); // last persisted body
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [view, setView] = useState('render');      // 'render' | 'source'

  const repoSelected = !!activeBoard;
  const dirty = content !== savedContent;

  // Reload the document list whenever the selected repo changes.
  useEffect(() => {
    setSelected(null);
    setContent('');
    setSavedContent('');
    if (!repoSelected) {
      setDocs([]);
      return;
    }
    api.listDocs(activeBoard)
      .then(setDocs)
      .catch(err => reportError(err, { headline: "Couldn't list docs" }));
  }, [activeBoard, repoSelected]);

  // Load the chosen document's body. Reset the Render/Source toggle to
  // Render whenever a different doc is opened so the user sees the
  // rendered surface first.
  useEffect(() => {
    if (!selected || !repoSelected) return;
    setLoading(true);
    setView('render');
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
      })
      .catch(err => {
        reportError(err, { headline: "Couldn't save document" });
        setSaving(false);
      });
  }, [activeBoard, selected, content, dirty, saving]);

  // Detect SVG from filename + current buffer content. Sniffs the live
  // buffer (not just savedContent) so editing in Source tab and tabbing
  // back to Render still keeps the toggle visible.
  const isSvg = useMemo(
    () => !!selected && isSvgDoc(selected, content),
    [selected, content],
  );

  // Transcript detection is filename-driven (predicate is conservative
  // so a hand-uploaded `.jsonl` doesn't accidentally trigger this
  // surface).
  const isTranscript = useMemo(
    () => !!selected && isJsonlTranscriptDoc(selected),
    [selected],
  );

  // A renderable doc has a Render/Source tab pair. Save still works
  // from the Source tab — symmetric with the SVG flow.
  const renderable = isSvg || isTranscript;

  // Object URL for the SVG Render tab. Created and revoked inside a
  // single useEffect so each URL.createObjectURL is paired with exactly
  // one URL.revokeObjectURL — important under React StrictMode, which
  // mounts / unmounts / remounts effects in dev and would otherwise
  // leave the first-mount URL revoked while the JSX kept rendering it.
  // Refreshes on every content edit so a dirty Source-tab buffer
  // renders live when the user tabs back.
  const [svgUrl, setSvgUrl] = useState('');
  useEffect(() => {
    if (!isSvg) { setSvgUrl(''); return undefined; }
    const url = URL.createObjectURL(new Blob([content], { type: 'image/svg+xml' }));
    setSvgUrl(url);
    return () => URL.revokeObjectURL(url);
  }, [isSvg, content]);

  const copySource = useCallback(() => {
    if (!content) return;
    try {
      navigator.clipboard?.writeText(content);
    } catch (_) {
      /* clipboard access can be blocked in some web contexts; ignore */
    }
  }, [content]);

  if (!repoSelected) {
    return (
      <div className="mk-docs">
        <div className="mk-docs-empty">Select a repository to view its documents.</div>
      </div>
    );
  }

  return (
    <div className="mk-docs">
      <aside className="mk-docs-list">
        {docs.length === 0 ? (
          <div className="mk-docs-list-empty">No documents in this repository.</div>
        ) : (
          docs.map(doc => (
            <button
              key={doc.filename}
              className={`mk-docs-item ${selected === doc.filename ? 'is-active' : ''}`}
              onClick={() => setSelected(doc.filename)}
            >
              <span className="mk-docs-item-name">{doc.filename}</span>
              <span className="mk-docs-item-type">{typeLabel(doc.type)}</span>
            </button>
          ))
        )}
      </aside>

      <div className="mk-docs-main">
        {!selected ? (
          <div className="mk-docs-empty">Pick a document to start editing.</div>
        ) : loading ? (
          <div className="mk-docs-empty">Loading…</div>
        ) : (
          <>
            <header className="mk-docs-bar">
              <span className="mk-docs-bar-name">{selected}</span>
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
              <span className="mk-docs-bar-status">
                {dirty ? 'Unsaved changes' : 'Saved'}
              </span>
              <div className="mk-docs-bar-actions">
                {renderable && (
                  <button
                    className="mk-btn-secondary"
                    onClick={copySource}
                    title="Copy source to clipboard"
                  >
                    Copy source
                  </button>
                )}
                <button
                  className="mk-btn-primary"
                  onClick={save}
                  disabled={!dirty || saving}
                >
                  {saving ? 'Saving…' : 'Save'}
                </button>
              </div>
            </header>
            {isSvg && view === 'render' ? (
              <div className="mk-docs-svg-pane">
                <img className="mk-docs-svg-img" src={svgUrl} alt={selected} />
              </div>
            ) : isTranscript && view === 'render' ? (
              <div className="mk-docs-transcript-pane">
                <TranscriptView source={content} filename={selected} sizeBytes={content?.length} />
              </div>
            ) : (
              <div className="mk-docs-editor">
                <NotionEditor content={content} onChange={setContent} />
              </div>
            )}
          </>
        )}
      </div>
    </div>
  );
}
