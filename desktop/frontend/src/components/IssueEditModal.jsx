import React, { useState } from 'react';
import Icon from './Icon.jsx';
import { reportError } from '../errors';
import * as api from '../api';

// IssueEditModal is a centred dialog for the two write operations the issue
// drawer exposes: editing the description and adding a comment. Each is an
// independent labelled section with its own action button. On a successful
// write the parent is handed the refreshed IssueDetail via onSaved so the
// drawer stays in sync; the modal stays open so the user can do both.
export default function IssueEditModal({ issue, repoPrefix, onClose, onSaved }) {
  const [desc, setDesc] = useState(issue?.description ?? '');
  const [commentAuthor, setCommentAuthor] = useState('');
  const [commentBody, setCommentBody] = useState('');
  const [busy, setBusy] = useState(null); // 'desc' | 'comment' | null

  if (!issue) return null;

  const descDirty = desc !== (issue.description ?? '');

  const saveDescription = async () => {
    setBusy('desc');
    try {
      const updated = await api.updateIssueDescription(repoPrefix, issue.key, desc);
      onSaved(updated);
    } catch (err) {
      reportError(err, { headline: "Couldn't save description" });
    } finally {
      setBusy(null);
    }
  };

  const addComment = async () => {
    if (!commentBody.trim()) return;
    setBusy('comment');
    try {
      const updated = await api.addComment(repoPrefix, issue.key, commentAuthor, commentBody);
      setCommentBody('');
      onSaved(updated);
    } catch (err) {
      reportError(err, { headline: "Couldn't add comment" });
    } finally {
      setBusy(null);
    }
  };

  return (
    <>
      <div className="mk-scrim" onClick={onClose} />
      <div className="mk-edit-modal" role="dialog" aria-label={`Edit ${issue.key}`}>
        <header className="mk-edit-modal-head">
          <h2 className="mk-edit-modal-title">Edit {issue.key}</h2>
          <button className="mk-icbtn" aria-label="Close" onClick={onClose}>
            <Icon name="x" />
          </button>
        </header>

        <div className="mk-edit-modal-body">
          <section className="mk-edit-section">
            <div className="mk-drawer-label">Description</div>
            <textarea
              className="mk-edit-textarea"
              rows={8}
              value={desc}
              disabled={busy === 'desc'}
              onChange={(e) => setDesc(e.target.value)}
            />
            <div className="mk-edit-actions">
              <button
                className="mk-btn-primary"
                disabled={!descDirty || busy !== null}
                onClick={saveDescription}
              >
                {busy === 'desc' ? 'Saving…' : 'Save description'}
              </button>
            </div>
          </section>

          <section className="mk-edit-section">
            <div className="mk-drawer-label">Add comment</div>
            <input
              className="mk-edit-input"
              type="text"
              placeholder="Author (defaults to your OS username)"
              value={commentAuthor}
              disabled={busy === 'comment'}
              onChange={(e) => setCommentAuthor(e.target.value)}
            />
            <textarea
              className="mk-edit-textarea"
              rows={4}
              placeholder="Write a comment…"
              value={commentBody}
              disabled={busy === 'comment'}
              onChange={(e) => setCommentBody(e.target.value)}
            />
            <div className="mk-edit-actions">
              <button
                className="mk-btn-primary"
                disabled={!commentBody.trim() || busy !== null}
                onClick={addComment}
              >
                {busy === 'comment' ? 'Adding…' : 'Add comment'}
              </button>
            </div>
          </section>
        </div>
      </div>
    </>
  );
}
