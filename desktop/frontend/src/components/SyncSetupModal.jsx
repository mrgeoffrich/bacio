import React, { useState, useCallback } from 'react';
import Modal from './Modal.jsx';
import * as api from '../api';
import { reportError } from '../errors';

// SyncSetupModal (BACI-111) — the two-step wizard that drives
// POST /repos/{prefix}/sync/setup from the standalone Sync view.
//
// Step 1 (the form) picks the mode and fills the per-mode fields:
//   - attach: pick an existing sync repo from the registry. The common
//     case once a sync repo is already set up on this machine.
//   - clone: git-clone a remote sync repo into ~/.bacio/sync/<derived>.
//     Used the first time a project joins an existing team sync repo.
//   - init: bootstrap a new sync repo at a local path. Used to create a
//     fresh sync repo from scratch.
//
// Step 2 (the collision banner) appears only when the server returns a
// 409 — the import would renumber or rename local rows. The user
// either backs out with ← Back (form state is preserved) or confirms
// with "Join anyway (renumber)", which re-runs submit with
// allow_renumber: true.
//
// Props:
//   - open: boolean controlling the Modal's open state.
//   - project: { prefix, name, path } — the unsynced project row.
//   - syncRepos: registry.syncRepos from the parent's BACI-108 fetch.
//   - onClose(): close handler (the X / Cancel / Escape).
//   - onDone(result): fires on a successful 200 so the parent re-fetches
//     the registry.
export default function SyncSetupModal({ open, project, syncRepos, onClose, onDone }) {
  // The default mode prefers attach when there's at least one registry
  // entry to pick from — the common case once a team sync repo is set
  // up. Otherwise default to clone (the next-most-common: joining an
  // existing remote). Init stays available as the third option.
  const initialMode = syncRepos && syncRepos.length > 0 ? 'attach' : 'clone';
  const [mode, setMode] = useState(initialMode);
  const [attachRemote, setAttachRemote] = useState(
    syncRepos && syncRepos[0] ? syncRepos[0].remoteUrl : '',
  );
  const [cloneRemote, setCloneRemote] = useState('');
  const [cloneLocalPath, setCloneLocalPath] = useState('');
  const [initLocalPath, setInitLocalPath] = useState('');
  const [initRemote, setInitRemote] = useState('');
  const [inFlight, setInFlight] = useState(false);
  const [previewCollisions, setPreviewCollisions] = useState(null);
  const [error, setError] = useState('');

  const reset = useCallback(() => {
    setMode(initialMode);
    setAttachRemote(syncRepos && syncRepos[0] ? syncRepos[0].remoteUrl : '');
    setCloneRemote('');
    setCloneLocalPath('');
    setInitLocalPath('');
    setInitRemote('');
    setInFlight(false);
    setPreviewCollisions(null);
    setError('');
  }, [initialMode, syncRepos]);

  const close = useCallback(() => {
    if (inFlight) return;
    reset();
    onClose?.();
  }, [inFlight, onClose, reset]);

  // doSubmit runs the actual setup call. Shared by the step-1 submit
  // button and the step-2 "Join anyway (renumber)" confirm — the only
  // difference is the allowRenumber flag the latter passes.
  const doSubmit = useCallback(async (allowRenumber) => {
    if (!project) return;
    setError('');
    // Per-mode client-side validation. The server also validates; this
    // is just so the user sees field-anchored feedback before a round
    // trip. Server-side absoluteness check on init.localPath is the
    // canonical one — we surface a helper hint here, not a hard refusal.
    let payload;
    switch (mode) {
      case 'attach':
        if (!attachRemote) {
          setError('Pick a sync repo to attach to.');
          return;
        }
        payload = { mode: 'attach', remote: attachRemote, allowRenumber };
        break;
      case 'clone':
        if (!cloneRemote.trim()) {
          setError('Enter the sync repo’s git URL.');
          return;
        }
        payload = {
          mode: 'clone',
          remote: cloneRemote.trim(),
          allowRenumber,
        };
        if (cloneLocalPath.trim()) payload.localPath = cloneLocalPath.trim();
        break;
      case 'init':
        if (!initLocalPath.trim()) {
          setError('Enter the local path where the sync repo should be created.');
          return;
        }
        payload = { mode: 'init', localPath: initLocalPath.trim() };
        if (initRemote.trim()) payload.remote = initRemote.trim();
        break;
      default:
        setError(`Unknown mode: ${mode}`);
        return;
    }
    setInFlight(true);
    try {
      const result = await api.setupSync(project.prefix, payload);
      reset();
      onDone?.(result);
    } catch (err) {
      if (err && err.name === 'SyncSetupCollisionError') {
        // Advance to step 2 — the form state stays intact so ← Back
        // re-renders step 1 with whatever the user typed.
        setPreviewCollisions(err.previewCollisions);
      } else {
        // Inline display for validation-style errors so the user can
        // correct and retry without losing the form. The global error
        // modal also fires (via reportError) for unexpected failures so
        // network / 5xx errors get the standard treatment.
        const msg = err?.message || 'Failed to set up sync';
        setError(msg);
        if (!isClientValidationError(msg)) {
          reportError(err, { headline: "Sync setup failed" });
        }
      }
    } finally {
      setInFlight(false);
    }
  }, [mode, attachRemote, cloneRemote, cloneLocalPath, initLocalPath, initRemote, project, onDone, reset]);

  if (!project) {
    return null;
  }

  return (
    <Modal
      open={open}
      onClose={close}
      title={`Set up sync for ${project.prefix}`}
      preventClickOutsideClose={inFlight}
    >
      <div className="mk-sync-setup-modal">
        {previewCollisions === null
          ? renderStep1({
              project,
              syncRepos,
              mode, setMode,
              attachRemote, setAttachRemote,
              cloneRemote, setCloneRemote,
              cloneLocalPath, setCloneLocalPath,
              initLocalPath, setInitLocalPath,
              initRemote, setInitRemote,
              inFlight, error,
              onCancel: close,
              onSubmit: () => doSubmit(false),
            })
          : renderStep2({
              previewCollisions,
              inFlight,
              error,
              onBack: () => { setPreviewCollisions(null); setError(''); },
              onConfirm: () => doSubmit(true),
            })}
      </div>
    </Modal>
  );
}

// renderStep1: the mode picker + per-mode form. Three radios, each
// reveals its sub-form when picked.
function renderStep1({
  project, syncRepos,
  mode, setMode,
  attachRemote, setAttachRemote,
  cloneRemote, setCloneRemote,
  cloneLocalPath, setCloneLocalPath,
  initLocalPath, setInitLocalPath,
  initRemote, setInitRemote,
  inFlight, error,
  onCancel, onSubmit,
}) {
  const hasRepos = syncRepos && syncRepos.length > 0;
  return (
    <>
      <p className="mk-settings-hint">
        Project <code>{project.prefix}</code> is at{' '}
        <code>{project.path}</code>. Setup writes the project's{' '}
        <code>.bacio/config.yaml</code> and either creates, clones, or
        attaches to a sync repository.
      </p>

      <fieldset
        className="mk-sync-setup-fields"
        disabled={inFlight}
      >
        <legend className="mk-sync-setup-legend">Sync source</legend>

        <label className="mk-sync-setup-radio">
          <input
            type="radio"
            name="sync-mode"
            value="attach"
            checked={mode === 'attach'}
            disabled={!hasRepos}
            onChange={() => setMode('attach')}
          />
          <span className="mk-sync-setup-radio-label">
            <strong>Attach to existing sync repo</strong>
            <span className="mk-settings-hint">
              Pick a sync repo this machine already knows. The project's
              data is added to it on the next push.
            </span>
          </span>
        </label>
        {mode === 'attach' && hasRepos && (
          <div className="mk-sync-setup-form-fields">
            <label className="mk-tmpl-add-field">
              <span>Sync repository</span>
              <select
                className="mk-tmpl-input"
                value={attachRemote}
                onChange={e => setAttachRemote(e.target.value)}
              >
                {syncRepos.map(r => (
                  <option key={r.remoteUrl} value={r.remoteUrl}>
                    {r.label} — {r.remoteUrl}
                  </option>
                ))}
              </select>
            </label>
          </div>
        )}
        {!hasRepos && (
          <div className="mk-sync-setup-form-fields">
            <p className="mk-settings-hint">
              No sync repos on this machine yet — clone an existing one
              or create a new one.
            </p>
          </div>
        )}

        <label className="mk-sync-setup-radio">
          <input
            type="radio"
            name="sync-mode"
            value="clone"
            checked={mode === 'clone'}
            onChange={() => setMode('clone')}
          />
          <span className="mk-sync-setup-radio-label">
            <strong>Clone an existing remote</strong>
            <span className="mk-settings-hint">
              Run <code>git clone</code> against a remote URL and import
              its data. Use when joining a team sync repo for the first
              time on this machine.
            </span>
          </span>
        </label>
        {mode === 'clone' && (
          <div className="mk-sync-setup-form-fields">
            <label className="mk-tmpl-add-field">
              <span>Remote URL</span>
              <input
                autoFocus
                className="mk-tmpl-input"
                value={cloneRemote}
                placeholder="git@github.com:you/bacio-sync.git"
                onChange={e => setCloneRemote(e.target.value)}
              />
            </label>
            <label className="mk-tmpl-add-field">
              <span>Local path (optional)</span>
              <input
                className="mk-tmpl-input"
                value={cloneLocalPath}
                placeholder="defaults to ~/.bacio/sync/<derived>"
                onChange={e => setCloneLocalPath(e.target.value)}
              />
            </label>
          </div>
        )}

        <label className="mk-sync-setup-radio">
          <input
            type="radio"
            name="sync-mode"
            value="init"
            checked={mode === 'init'}
            onChange={() => setMode('init')}
          />
          <span className="mk-sync-setup-radio-label">
            <strong>Initialise a new sync repo</strong>
            <span className="mk-settings-hint">
              Create a brand-new sync repo at a local path. Use when
              you're the first machine setting up sync for this project.
            </span>
          </span>
        </label>
        {mode === 'init' && (
          <div className="mk-sync-setup-form-fields">
            <label className="mk-tmpl-add-field">
              <span>Local path</span>
              <input
                autoFocus
                className="mk-tmpl-input"
                value={initLocalPath}
                placeholder="/Users/you/bacio-sync"
                onChange={e => setInitLocalPath(e.target.value)}
              />
            </label>
            <label className="mk-tmpl-add-field">
              <span>Remote URL (optional)</span>
              <input
                className="mk-tmpl-input"
                value={initRemote}
                placeholder="git@github.com:you/bacio-sync.git"
                onChange={e => setInitRemote(e.target.value)}
              />
            </label>
          </div>
        )}
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
          onClick={onCancel}
          disabled={inFlight}
        >
          Cancel
        </button>
        <button
          type="button"
          className="mk-segmented-btn is-active"
          onClick={onSubmit}
          disabled={inFlight}
        >
          {inFlight ? 'Setting up…' : 'Set up sync'}
        </button>
      </div>
    </>
  );
}

// renderStep2: the collision banner + Join anyway confirm. Appears
// when the server returns 409 + a populated CollisionPreview.
function renderStep2({ previewCollisions, inFlight, error, onBack, onConfirm }) {
  const renumbered = previewCollisions?.renumbered ?? [];
  const renamed = previewCollisions?.renamed ?? [];
  const total = renumbered.length + renamed.length;
  return (
    <>
      <div className="mk-sync-setup-collision-banner">
        <h3 className="mk-sync-setup-collision-title">
          Cloning would renumber existing issues
        </h3>
        <p className="mk-settings-hint">
          Joining this sync repo would renumber or rename{' '}
          <strong>{total}</strong> local rows to avoid clashes with
          the imported data. The changes only happen if you confirm.
        </p>

        {renumbered.length > 0 && (
          <div className="mk-sync-setup-collision-section">
            <h4 className="mk-sync-setup-collision-subtitle">
              Renumbered issues ({renumbered.length})
            </h4>
            <ul className="mk-sync-setup-collision-list">
              {renumbered.map(r => (
                <li key={r.uuid}>
                  <code>{r.prefix}-{r.oldNumber}</code> →{' '}
                  <code>{r.prefix}-{r.newNumber}</code>
                </li>
              ))}
            </ul>
          </div>
        )}

        {renamed.length > 0 && (
          <div className="mk-sync-setup-collision-section">
            <h4 className="mk-sync-setup-collision-subtitle">
              Renamed ({renamed.length})
            </h4>
            <ul className="mk-sync-setup-collision-list">
              {renamed.map(r => (
                <li key={r.uuid}>
                  <span className="mk-sync-setup-collision-kind">{r.kind}:</span>{' '}
                  <code>{r.old}</code> → <code>{r.new}</code>
                </li>
              ))}
            </ul>
          </div>
        )}
      </div>

      {error && (
        <p className="mk-settings-hint" style={{ color: 'var(--status-blocked, #d44)' }}>
          {error}
        </p>
      )}

      <div className="mk-modal-actions">
        <button
          type="button"
          className="mk-segmented-btn"
          onClick={onBack}
          disabled={inFlight}
        >
          ← Back
        </button>
        <button
          type="button"
          className="mk-segmented-btn is-active"
          onClick={onConfirm}
          disabled={inFlight}
        >
          {inFlight ? 'Joining…' : 'Join anyway (renumber)'}
        </button>
      </div>
    </>
  );
}

// isClientValidationError tries to recognise a server-side validation
// failure (`invalid_input` envelope) so we don't double-fire the global
// error modal alongside the inline message. The HTTP and Wails paths
// both throw plain Errors here, so this is best-effort string matching.
function isClientValidationError(msg) {
  if (!msg) return false;
  return msg.startsWith('mode must be')
    || msg.startsWith('mode="')
    || msg.includes('must not set local_path')
    || msg.includes('local_path must be')
    || msg.includes('no sync_remotes registry entry')
    || msg.includes('renumber collision');
}
