import { useState, useCallback } from 'react';
import Modal from './Modal';
import * as api from '../api';
import type { MemberProjectDTO, RepoLinkResult } from '../api';
import { reportError } from '../errors';

// PhantomLinkModal (BACI-112) — the path-input modal opened from a
// phantom row's "Link local…" button on SyncRepoCard. Submits the
// supplied path to api.linkPhantomRepo, which routes through either
// the Wails seam (desktop) or POST /repos/{prefix}/link (web).
//
// Mirrors the SyncSetupModal pattern: an inline error rendered through
// mk-settings-hint (so the user can correct and retry without losing
// the form), plus a global reportError firing for unexpected failures
// (e.g. 5xx, network).
//
// Props:
//   - phantom: { prefix, name, status, ... } — the phantom row clicked.
//     null when the modal is closed.
//   - onClose(): close handler (the X / Cancel / Escape).
//   - onSubmitted(result): fires on a successful link so the parent can
//     refresh the registry. result has shape { prefix, path,
//     syncRemoteUrl, alreadyLinked }.
type PhantomLinkModalProps = {
  phantom: MemberProjectDTO | null;
  onClose?: () => void;
  onSubmitted?: (result: RepoLinkResult) => void;
};

export default function PhantomLinkModal({ phantom, onClose, onSubmitted }: PhantomLinkModalProps) {
  const [path, setPath] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState('');

  const close = useCallback(() => {
    if (submitting) return;
    setPath('');
    setError('');
    onClose?.();
  }, [submitting, onClose]);

  const submit = useCallback(async () => {
    if (!phantom) return;
    const trimmed = path.trim();
    if (!trimmed) {
      setError('Path is required.');
      return;
    }
    setError('');
    setSubmitting(true);
    try {
      const result = await api.linkPhantomRepo(phantom.prefix, trimmed);
      setPath('');
      onSubmitted?.(result);
    } catch (err) {
      const msg = (err instanceof Error ? err.message : '') || 'Failed to link phantom repo';
      setError(msg);
      // Surface the global modal for genuinely unexpected failures
      // (network, 5xx). The handler's typed-error refusals — not a
      // phantom, path conflict, missing sync repo, bad path shape —
      // carry one of the recognisable strings below; those stay inline
      // so the user can correct the path without losing the form.
      if (!isClientFacingValidation(msg)) {
        reportError(err, { headline: "Couldn't link to working tree" });
      }
    } finally {
      setSubmitting(false);
    }
  }, [phantom, path, onSubmitted]);

  if (!phantom) {
    return null;
  }

  const open = !!phantom;
  return (
    <Modal
      open={open}
      onClose={close}
      title={`Link ${phantom.prefix} to a working tree`}
      preventClickOutsideClose={submitting}
    >
      <div className="mk-phantom-link-modal">
        <p className="mk-settings-hint">
          Bind <code>{phantom.prefix}</code>{phantom.name ? ` (${phantom.name})` : ''} to a local
          git working tree. The project's <code>.bacio/config.yaml</code> is
          written pointing at the sync repo's remote URL — bacio sync from
          that checkout then uses the same sync repo.
        </p>

        <fieldset className="mk-sync-setup-fields" disabled={submitting}>
          <label className="mk-tmpl-add-field">
            <span>Local working tree path</span>
            <input
              autoFocus
              className="mk-tmpl-input"
              value={path}
              placeholder="/Users/you/code/project"
              onChange={(e) => setPath(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') {
                  e.preventDefault();
                  submit();
                }
              }}
            />
          </label>
          <p className="mk-settings-hint">
            Must be an absolute path to an existing git working tree.
          </p>
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
            {submitting ? 'Linking…' : 'Link'}
          </button>
        </div>
      </div>
    </Modal>
  );
}

// isClientFacingValidation recognises the handler's typed-error refusals
// (kind=not_phantom / path_already_bound / no_owning_sync_repo /
// path_not_absolute / path_not_exists / path_not_git) so we don't
// double-fire the global error modal for what the user can fix inline.
// String-match against the typed-error Error() output verbatim — the
// API's human messages are deliberately stable so this stays a thin
// best-effort filter.
function isClientFacingValidation(msg: string): boolean {
  if (!msg) return false;
  return msg.includes('is not a phantom')
    || msg.includes('is already bound to repo')
    || msg.includes('no sync repository on this machine carries')
    || msg.includes('path must be absolute')
    || msg.includes('path does not exist')
    || msg.includes('path is not a git working tree')
    || msg.includes('path is not a directory')
    || msg.includes('path is required');
}
