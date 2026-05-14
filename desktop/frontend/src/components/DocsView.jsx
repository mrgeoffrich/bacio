import React, { useState, useEffect, useCallback } from 'react';
import { NotionEditor } from './editor/NotionEditor';
import * as api from '../api';

// Human label for a bacio document-type enum value.
function typeLabel(t) {
  return t.replace(/_/g, ' ');
}

// DocsView is the desktop document browser + editor. Documents are per-repo.
// Edits are buffered locally and persisted explicitly via the Save button.
export default function DocsView({ activeBoard }) {
  const [docs, setDocs] = useState([]);
  const [selected, setSelected] = useState(null); // filename
  const [content, setContent] = useState('');      // live editor buffer
  const [savedContent, setSavedContent] = useState(''); // last persisted body
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState(null);

  const repoSelected = !!activeBoard;
  const dirty = content !== savedContent;

  // Reload the document list whenever the selected repo changes.
  useEffect(() => {
    setSelected(null);
    setContent('');
    setSavedContent('');
    setError(null);
    if (!repoSelected) {
      setDocs([]);
      return;
    }
    api.listDocs(activeBoard)
      .then(setDocs)
      .catch(err => setError(err.message));
  }, [activeBoard, repoSelected]);

  // Load the chosen document's markdown body.
  useEffect(() => {
    if (!selected || !repoSelected) return;
    setLoading(true);
    setError(null);
    api.getDoc(activeBoard, selected)
      .then(doc => {
        setContent(doc.content);
        setSavedContent(doc.content);
        setLoading(false);
      })
      .catch(err => {
        setError(err.message);
        setLoading(false);
      });
  }, [selected, activeBoard, repoSelected]);

  const save = useCallback(() => {
    if (!selected || !dirty || saving) return;
    setSaving(true);
    setError(null);
    api.saveDoc(activeBoard, selected, content)
      .then(doc => {
        setSavedContent(doc.content);
        setSaving(false);
      })
      .catch(err => {
        setError(err.message);
        setSaving(false);
      });
  }, [activeBoard, selected, content, dirty, saving]);

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
        {error && <div className="mk-docs-error">{error}</div>}
        {!selected ? (
          <div className="mk-docs-empty">Pick a document to start editing.</div>
        ) : loading ? (
          <div className="mk-docs-empty">Loading…</div>
        ) : (
          <>
            <header className="mk-docs-bar">
              <span className="mk-docs-bar-name">{selected}</span>
              <span className="mk-docs-bar-status">
                {dirty ? 'Unsaved changes' : 'Saved'}
              </span>
              <button
                className="mk-btn-primary"
                onClick={save}
                disabled={!dirty || saving}
              >
                {saving ? 'Saving…' : 'Save'}
              </button>
            </header>
            <div className="mk-docs-editor">
              <NotionEditor content={content} onChange={setContent} />
            </div>
          </>
        )}
      </div>
    </div>
  );
}
