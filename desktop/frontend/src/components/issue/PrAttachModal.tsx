import React, { useState, useCallback } from 'react';
import Modal from '../Modal';
import { reportError } from '../../errors';

// PrAttachModal (BACI-339) — the URL-input modal opened from the
// IssueWorkspace rail's "+ Attach PR" button. It replaces the always-open
// inline `.mk-pr-form` so the field only occupies space when the user
// actually wants to attach a PR.
//
// Mirrors PhantomLinkModal's pattern: an inline error (so the user can
// correct a bad URL and retry without losing the form) for the store's
// client-facing URL-shape refusal, plus a global reportError for genuinely
// unexpected failures (network, 5xx).
//
// Props:
//   - open: whether the modal is shown.
//   - onClose(): close handler (the X / Cancel / Escape).
//   - onAttach(url): attaches the PR — the workspace's onAttachPR prop
//     (App's attachPR → api.attachPullRequest → refreshBrief). Throws on a
//     rejected URL; the message surfaces inline.
export default function PrAttachModal({ open, onClose, onAttach }) {
  const [url, setUrl] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState('');

  const close = useCallback(() => {
    if (submitting) return;
    setUrl('');
    setError('');
    onClose?.();
  }, [submitting, onClose]);

  const submit = useCallback(async () => {
    const trimmed = url.trim();
    if (!trimmed) {
      setError('A pull-request URL is required.');
      return;
    }
    setError('');
    setSubmitting(true);
    try {
      await onAttach(trimmed);
      setUrl('');
      onClose?.();
    } catch (err) {
      const msg = err?.message || 'Failed to attach pull request';
      setError(msg);
      // The store's URL-shape refusals (must be http/https, malformed URL)
      // stay inline so the user can fix them; surface the global modal only
      // for genuinely unexpected failures (network, 5xx).
      if (!isClientFacingValidation(msg)) {
        reportError(err, { headline: "Couldn't attach pull request" });
      }
    } finally {
      setSubmitting(false);
    }
  }, [url, onAttach, onClose]);

  return (
    <Modal
      open={open}
      onClose={close}
      title="Attach a pull request"
      preventClickOutsideClose={submitting}
    >
      <div className="mk-pr-attach-modal">
        <fieldset className="mk-sync-setup-fields" disabled={submitting}>
          <label className="mk-tmpl-add-field">
            <span>Pull-request URL</span>
            <input
              autoFocus
              type="url"
              className="mk-tmpl-input"
              value={url}
              placeholder="https://github.com/owner/repo/pull/N"
              onChange={(e) => setUrl(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') {
                  e.preventDefault();
                  submit();
                }
              }}
            />
          </label>
        </fieldset>

        {error && (
          <p className="mk-settings-hint" style={{ color: 'var(--status-blocked, #d44)' }}>
            {error}
          </p>
        )}

        <div className="mk-modal-actions">
          <button
            type="button"
            className="mk-segmented-btn"
            onClick={close}
            disabled={submitting}
          >
            Cancel
          </button>
          <button
            type="button"
            className="mk-segmented-btn is-active"
            onClick={submit}
            disabled={submitting}
          >
            {submitting ? 'Attaching…' : 'Attach'}
          </button>
        </div>
      </div>
    </Modal>
  );
}

// isClientFacingValidation recognises the store's URL-shape refusals so we
// don't double-fire the global error modal for something the user can fix
// inline. ValidatePRURLStrict (internal/store/validate.go) emits these
// stable human messages for a malformed / non-http(s) / hostless URL;
// string-match them the same way PhantomLinkModal filters its handler's
// typed errors.
function isClientFacingValidation(msg) {
  if (!msg) return false;
  return msg.includes('URL is required')
    || msg.includes('URL too long')
    || msg.includes('disallowed control character')
    || msg.includes('invalid URL')
    || msg.includes('http or https scheme')
    || msg.includes('URL must include a host');
}
