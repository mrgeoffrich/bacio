import React, { useEffect, useState } from 'react';
import ReactMarkdown from 'react-markdown';

// InlineDescriptionEditor toggles between a rendered (markdown) view and
// a textarea editor in place. The pencil opens edit mode; Save persists
// via onSave (workspace → App → api.updateIssueDescription) and Cancel
// reverts to the brief's description. While editing, the parent App
// keeps `descEditing` true so its 10s poll-refresh skips replacing the
// user's buffer — `value` resyncs to a freshly-loaded brief only when
// editing is off (the `editing` guard on the effect).
//
// readOnly hides the pencil entirely — taken/waiting issues can't be
// edited inline. Renders the empty-description placeholder when no body
// and not in edit mode.
export default function InlineDescriptionEditor({
  description,
  readOnly,
  onSave,
  onEditingChange,
}) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(description ?? '');
  const [saving, setSaving] = useState(false);

  // Sync the draft to a fresh brief description only when not editing —
  // a parallel poll re-rendering this component must not stomp the
  // user's in-progress buffer.
  useEffect(() => {
    if (!editing) setDraft(description ?? '');
  }, [description, editing]);

  const startEdit = () => {
    setDraft(description ?? '');
    setEditing(true);
    onEditingChange?.(true);
  };

  const cancelEdit = () => {
    setDraft(description ?? '');
    setEditing(false);
    onEditingChange?.(false);
  };

  const commitSave = async () => {
    setSaving(true);
    try {
      await onSave(draft);
      setEditing(false);
      onEditingChange?.(false);
    } finally {
      setSaving(false);
    }
  };

  return (
    <section className="mk-drawer-section">
      <div className="mk-drawer-label">
        Description
        {!readOnly && !editing && (
          <button
            type="button"
            className="mk-inline-edit-btn"
            aria-label="Edit description"
            onClick={startEdit}
          >
            Edit
          </button>
        )}
      </div>
      {editing ? (
        <>
          <textarea
            className="mk-edit-textarea"
            rows={10}
            value={draft}
            disabled={saving}
            autoFocus
            onChange={(e) => setDraft(e.target.value)}
          />
          <div className="mk-edit-actions">
            <button
              type="button"
              className="mk-btn-secondary"
              disabled={saving}
              onClick={cancelEdit}
            >
              Cancel
            </button>
            <button
              type="button"
              className="mk-btn-primary"
              disabled={saving || draft === (description ?? '')}
              onClick={commitSave}
            >
              {saving ? 'Saving…' : 'Save'}
            </button>
          </div>
        </>
      ) : description ? (
        <div className="mk-drawer-text mk-markdown">
          <ReactMarkdown>{description}</ReactMarkdown>
        </div>
      ) : (
        <p className="mk-drawer-text mk-meta-empty">No description.</p>
      )}
    </section>
  );
}
