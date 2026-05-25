import React, { useState } from 'react';
import Modal from './Modal.jsx';
import { reportError } from '../errors';
import * as api from '../api';

// PhantomLinkModal (BACI-112) is the path-input dialog the Sync view
// opens when the operator clicks `Link local…` on a phantom row of a
// SyncRepoCard. Submission calls api.linkPhantomRepo, which POSTs to
// the shared `/repos/{prefix}/link` handler (desktop via Wails, web via
// fetch). Mirrors the RepoPicker add-repo modal's shape — same
// mk-tmpl-add-field + mk-modal-actions classes — so the two surfaces
// feel consistent.
//
// Props:
//   - phantom: the MemberProject row clicked. Carries prefix + name +
//     uuid; modal title uses prefix.
//   - onClose: invoked when the user cancels / dismisses the modal.
//   - onSubmitted: invoked with the RepoLinkResultDTO on success; the
//     caller is expected to refresh its registry and close the modal.
//
// On failure we route through reportError (so the operator sees the
// global error tray) AND surface the message inline so the modal
// stays open and the user can correct the typed path.
export default function PhantomLinkModal({ phantom, onClose, onSubmitted }) {
  const [path, setPath] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [err, setErr] = useState('');

  const submit = async () => {
    if (submitting) return;
    const trimmed = path.trim();
    if (!trimmed) {
      setErr('Path is required.');
      return;
    }
    setSubmitting(true);
    setErr('');
    try {
      const result = await api.linkPhantomRepo(phantom.prefix, trimmed);
      onSubmitted(result);
    } catch (e) {
      const msg = e?.message || `Failed to link ${phantom.prefix}`;
      setErr(msg);
      reportError(e, { headline: `Couldn't link ${phantom.prefix} to a working tree` });
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Modal
      open={!!phantom}
      onClose={() => { if (!submitting) onClose?.(); }}
      title={`Link ${phantom.prefix} to a working tree`}
    >
      <p className="mk-settings-hint">
        Path is on the server&apos;s filesystem
        {' '}({window.location.host || 'this host'}), not your local machine.
        Point at the existing git working tree for {phantom.name || phantom.prefix}.
        After linking, <code>.bacio/config.yaml</code> is written there so
        background sync picks it up automatically.
      </p>
      <label className="mk-tmpl-add-field">
        <span>Path</span>
        <input
          autoFocus
          className="mk-tmpl-input"
          value={path}
          placeholder="/Users/you/Code/my-project"
          onChange={e => setPath(e.target.value)}
          onKeyDown={e => {
            if (e.key === 'Enter' && !submitting) {
              e.preventDefault();
              submit();
            }
          }}
        />
      </label>
      {err && (
        <p className="mk-settings-hint" style={{ color: 'var(--status-blocked, #d44)' }}>
          {err}
        </p>
      )}
      <div className="mk-modal-actions">
        <button
          className="mk-segmented-btn"
          onClick={() => { if (!submitting) onClose?.(); }}
          disabled={submitting}
        >
          Cancel
        </button>
        <button
          className="mk-segmented-btn is-active"
          onClick={submit}
          disabled={submitting}
        >
          {submitting ? 'Linking…' : 'Link'}
        </button>
      </div>
    </Modal>
  );
}
