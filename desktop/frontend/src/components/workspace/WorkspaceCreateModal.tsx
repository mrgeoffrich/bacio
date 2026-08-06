import { useEffect, useState } from 'react';
import Modal from '../Modal';
import * as api from '../../api';
import type { Board } from '../../api';
import './workspace.css';

// WorkspaceCreateModal collects the two fields a manual workspace takes —
// `{name, prefix?}` — and calls api.addWorkspace.
//
// It is deliberately ONE modal for both transports. The git-repository
// path forks on WEB_MODE because desktop has something native to invoke
// (a Wails folder picker over the working tree) and the browser does not.
// A workspace has no directory at all on either transport, so there is
// nothing native to reach for and nothing to fork on: same fields, same
// call, same modal, desktop and web.
//
// The prefix is optional. Blank means "allocate one from the name", the
// same store.AllocatePrefix path a git registration uses — workspaces and
// git repos share one prefix namespace. A typed prefix is 4 alphanumeric
// characters, but this component does not reimplement that rule: the
// store-boundary validator owns it, and a rejection (including the 409 a
// taken prefix produces) is surfaced verbatim below the fields.
type WorkspaceCreateModalProps = {
  open: boolean;
  onClose: () => void;
  onCreated: (board: Board) => void;
};

export default function WorkspaceCreateModal({ open, onClose, onCreated }: WorkspaceCreateModalProps) {
  const [name, setName] = useState('');
  const [prefix, setPrefix] = useState('');
  const [error, setError] = useState('');
  const [submitting, setSubmitting] = useState(false);

  // Reset on open so a second visit doesn't inherit the last attempt's
  // half-typed name or its error.
  useEffect(() => {
    if (!open) return;
    setName('');
    setPrefix('');
    setError('');
    setSubmitting(false);
  }, [open]);

  const close = () => {
    if (submitting) return;
    onClose();
  };

  const submit = async () => {
    if (submitting) return;
    const trimmedName = name.trim();
    if (!trimmedName) {
      setError('Name is required.');
      return;
    }
    setSubmitting(true);
    setError('');
    try {
      const board = await api.addWorkspace(trimmedName, prefix.trim() || undefined);
      onCreated(board);
    } catch (err) {
      // Surfaced inline only, not through the global reportError modal:
      // every failure this call produces is a correctable form error
      // (blank name, malformed prefix, or the 409 a prefix already in use
      // returns), and the fix is in the fields still on screen. Stacking
      // an error modal on top of the form would hide them.
      const message = err instanceof Error ? err.message : '';
      setError(message || 'Failed to create workspace');
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Modal open={open} onClose={close} title="New Workspace">
      <p className="mk-settings-hint">
        A workspace is a bacio project with no git repository behind it —
        issues, documents and a Kanban board, with nothing on disk. There is
        no path to point at.
      </p>
      <label className="mk-tmpl-add-field">
        <span>Name</span>
        <input
          autoFocus
          className="mk-tmpl-input"
          value={name}
          placeholder="Marketing"
          onChange={e => setName(e.target.value)}
          onKeyDown={e => { if (e.key === 'Enter') void submit(); }}
        />
      </label>
      <label className="mk-tmpl-add-field">
        <span>Prefix (optional)</span>
        <input
          className="mk-tmpl-input"
          value={prefix}
          placeholder="MARK (auto-allocated if blank)"
          maxLength={4}
          onChange={e => setPrefix(e.target.value)}
          onKeyDown={e => { if (e.key === 'Enter') void submit(); }}
        />
      </label>
      <p className="mk-settings-hint">
        Issue keys are built from the prefix (<code>MARK-1</code>). Leave it
        blank to have one allocated from the name; workspaces and git repos
        share one prefix namespace, so a prefix already taken by either is
        refused.
      </p>
      {error && (
        <p className="mk-settings-hint" style={{ color: 'var(--status-blocked, #d44)' }}>
          {error}
        </p>
      )}
      <div className="mk-modal-actions">
        <button className="mk-segmented-btn" onClick={close} disabled={submitting}>
          Cancel
        </button>
        <button
          className="mk-segmented-btn is-active"
          onClick={() => void submit()}
          disabled={submitting}
        >
          {submitting ? 'Creating…' : 'Create'}
        </button>
      </div>
    </Modal>
  );
}
