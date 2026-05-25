import React, { useState, useMemo } from 'react';
import Modal from './Modal.jsx';
import { reportError } from '../errors';
import * as api from '../api';

// SyncSetupModal is the BACI-111 wizard that wires the previously-
// disabled "Set up sync…" buttons under SyncView's Unsynced projects
// section to the POST /repos/{prefix}/sync/setup handler (BACI-110).
//
// The wizard is a two-step state machine inside a single Radix Modal:
// step 1 picks the mode (attach / clone / init) and gathers the form
// fields; step 2 only renders when the server returns a 409 with a
// renumber-collision preview. Step 2's "Join anyway" re-submits the
// same form payload with `allowRenumber: true` so the rest of the
// flow shares one code path.
//
// The modal is rendered identically in desktop and `bacio web` — there
// is intentionally no hint about server-side filesystem paths (the
// operator is presumed to know where their `bacio web` is hosted).
// RepoPicker's add-repo modal is the lone exception that surfaces a
// "this path is on the server" hint; the sync setup form follows
// the convention of every other form in the React tree.
export default function SyncSetupModal({
  open,
  onClose,
  project,
  syncRepos,
  onDone,
}) {
  const hasAttach = (syncRepos?.length ?? 0) > 0;
  const initialMode = hasAttach ? 'attach' : 'clone';

  const [mode, setMode] = useState(initialMode);
  const [attachRemote, setAttachRemote] = useState(syncRepos?.[0]?.remoteUrl ?? '');
  const [cloneRemote, setCloneRemote] = useState('');
  const [cloneLocalPath, setCloneLocalPath] = useState('');
  const [initLocalPath, setInitLocalPath] = useState('');
  const [initRemote, setInitRemote] = useState('');
  const [inFlight, setInFlight] = useState(false);
  const [previewCollisions, setPreviewCollisions] = useState(null);
  const [error, setError] = useState('');

  // Reset transient state whenever the modal closes — the next open
  // shouldn't carry stale form values from a previous project.
  const resetState = () => {
    setMode(initialMode);
    setAttachRemote(syncRepos?.[0]?.remoteUrl ?? '');
    setCloneRemote('');
    setCloneLocalPath('');
    setInitLocalPath('');
    setInitRemote('');
    setInFlight(false);
    setPreviewCollisions(null);
    setError('');
  };

  const handleClose = () => {
    if (inFlight) return;
    resetState();
    onClose?.();
  };

  // Build the payload from the current form state. Shared by the
  // initial submit and the step-2 "Join anyway" re-submit.
  const payloadForMode = useMemo(() => {
    return (allowRenumber) => {
      const base = { mode, allowRenumber: !!allowRenumber };
      if (mode === 'attach') {
        return { ...base, remote: attachRemote };
      }
      if (mode === 'clone') {
        return {
          ...base,
          remote: cloneRemote.trim(),
          localPath: cloneLocalPath.trim() || undefined,
        };
      }
      // mode === 'init'
      return {
        ...base,
        localPath: initLocalPath.trim(),
        remote: initRemote.trim() || undefined,
      };
    };
  }, [mode, attachRemote, cloneRemote, cloneLocalPath, initLocalPath, initRemote]);

  // Client-side validation. The server's the source of truth, but a
  // pre-flight catches obvious typos without a network round-trip.
  const validate = () => {
    if (mode === 'attach') {
      if (!attachRemote) return 'Pick a sync repository to attach to.';
      return '';
    }
    if (mode === 'clone') {
      const url = cloneRemote.trim();
      if (!url) return 'Remote URL is required.';
      if (!/^(https?|git|ssh|git\+ssh):\/\/|^[^/]+@[^:]+:/.test(url)) {
        return 'Remote URL must look like a git URL (https://…, git@…, ssh://…).';
      }
      const lp = cloneLocalPath.trim();
      if (lp && !lp.startsWith('/') && !lp.startsWith('~')) {
        return 'Local path must be absolute (start with / or ~).';
      }
      return '';
    }
    if (mode === 'init') {
      const lp = initLocalPath.trim();
      if (!lp) return 'Local path is required.';
      if (!lp.startsWith('/') && !lp.startsWith('~')) {
        return 'Local path must be absolute (start with / or ~).';
      }
      return '';
    }
    return '';
  };

  const submit = async (allowRenumber) => {
    const v = validate();
    if (v) { setError(v); return; }
    setError('');
    setInFlight(true);
    try {
      const result = await api.setupSync(project.prefix, payloadForMode(allowRenumber));
      onDone?.(result);
      resetState();
      onClose?.();
    } catch (err) {
      // SyncSetupCollisionError is the 409 path — flip to step 2 and
      // let the user confirm or back out.
      if (err && err.name === 'SyncSetupCollisionError' && err.previewCollisions) {
        setPreviewCollisions(err.previewCollisions);
      } else {
        // Real failure. Surface inline so the modal stays open and the
        // user can correct typos; also broadcast through reportError
        // for the global modal in case the message is opaque (network
        // outage, server crash).
        const msg = err?.message ?? String(err);
        setError(msg);
        reportError(err, { headline: "Sync setup failed" });
      }
    } finally {
      setInFlight(false);
    }
  };

  // Step 2 only renders once the server has returned a 409. "Back"
  // clears the preview so the form state (still in component state)
  // re-appears intact.
  const onBackToForm = () => {
    if (inFlight) return;
    setPreviewCollisions(null);
  };

  if (!project) return null;

  return (
    <Modal
      open={open}
      onClose={handleClose}
      title={`Set up sync for ${project.prefix}`}
      preventClickOutsideClose={inFlight}
    >
      <div className="mk-sync-setup-modal">
        {previewCollisions === null ? (
          renderStep1({
            project,
            mode,
            setMode,
            hasAttach,
            syncRepos,
            attachRemote,
            setAttachRemote,
            cloneRemote,
            setCloneRemote,
            cloneLocalPath,
            setCloneLocalPath,
            initLocalPath,
            setInitLocalPath,
            initRemote,
            setInitRemote,
            error,
            inFlight,
            onCancel: handleClose,
            onSubmit: () => submit(false),
          })
        ) : (
          renderStep2({
            previewCollisions,
            inFlight,
            onBack: onBackToForm,
            onConfirm: () => submit(true),
          })
        )}
      </div>
    </Modal>
  );
}

function renderStep1({
  project,
  mode,
  setMode,
  hasAttach,
  syncRepos,
  attachRemote,
  setAttachRemote,
  cloneRemote,
  setCloneRemote,
  cloneLocalPath,
  setCloneLocalPath,
  initLocalPath,
  setInitLocalPath,
  initRemote,
  setInitRemote,
  error,
  inFlight,
  onCancel,
  onSubmit,
}) {
  const submitLabel = inFlight
    ? 'Setting up…'
    : mode === 'attach' ? 'Attach' : mode === 'clone' ? 'Clone & attach' : 'Initialise';

  return (
    <>
      <p className="mk-settings-hint">
        Attach <code>{project.prefix}</code> ({project.name}) to a sync
        repository so its issues, features, and documents mirror across
        machines.
      </p>

      <div className="mk-sync-setup-radios">
        <SyncRadio
          name="mode"
          value="attach"
          checked={mode === 'attach'}
          disabled={!hasAttach}
          onChange={setMode}
          title="Attach to an existing sync repository"
          subtitle={
            hasAttach
              ? 'Use a sync repo this machine already knows about.'
              : 'No sync repositories on this machine yet — clone an existing one or create a new one.'
          }
        >
          {mode === 'attach' && hasAttach && (
            <label className="mk-tmpl-add-field">
              <span>Sync repository</span>
              <select
                className="mk-tmpl-input"
                value={attachRemote}
                onChange={(e) => setAttachRemote(e.target.value)}
                disabled={inFlight}
              >
                {syncRepos.map((r) => (
                  <option key={r.remoteUrl} value={r.remoteUrl}>
                    {r.label} ({r.remoteUrl})
                  </option>
                ))}
              </select>
            </label>
          )}
        </SyncRadio>

        <SyncRadio
          name="mode"
          value="clone"
          checked={mode === 'clone'}
          onChange={setMode}
          title="Clone a sync repository"
          subtitle="Clone an existing remote sync repo this machine doesn't have yet."
        >
          {mode === 'clone' && (
            <>
              <label className="mk-tmpl-add-field">
                <span>Remote URL</span>
                <input
                  autoFocus
                  className="mk-tmpl-input"
                  value={cloneRemote}
                  placeholder="git@github.com:team/bacio-sync.git"
                  onChange={(e) => setCloneRemote(e.target.value)}
                  disabled={inFlight}
                />
              </label>
              <label className="mk-tmpl-add-field">
                <span>Local path (optional)</span>
                <input
                  className="mk-tmpl-input"
                  value={cloneLocalPath}
                  placeholder="~/.bacio/sync/<derived> (default)"
                  onChange={(e) => setCloneLocalPath(e.target.value)}
                  disabled={inFlight}
                />
              </label>
            </>
          )}
        </SyncRadio>

        <SyncRadio
          name="mode"
          value="init"
          checked={mode === 'init'}
          onChange={setMode}
          title="Initialise a new sync repository"
          subtitle="Bootstrap a brand-new sync repo locally — for the first project on a new sync remote."
        >
          {mode === 'init' && (
            <>
              <label className="mk-tmpl-add-field">
                <span>Local path</span>
                <input
                  autoFocus
                  className="mk-tmpl-input"
                  value={initLocalPath}
                  placeholder="/Users/you/bacio-sync"
                  onChange={(e) => setInitLocalPath(e.target.value)}
                  disabled={inFlight}
                />
              </label>
              <label className="mk-tmpl-add-field">
                <span>Remote URL (optional)</span>
                <input
                  className="mk-tmpl-input"
                  value={initRemote}
                  placeholder="git@github.com:team/bacio-sync.git"
                  onChange={(e) => setInitRemote(e.target.value)}
                  disabled={inFlight}
                />
              </label>
            </>
          )}
        </SyncRadio>
      </div>

      {error && (
        <p className="mk-settings-hint mk-sync-setup-error">{error}</p>
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
          {submitLabel}
        </button>
      </div>
    </>
  );
}

function renderStep2({ previewCollisions, inFlight, onBack, onConfirm }) {
  const { renumbered = [], renamed = [] } = previewCollisions;
  return (
    <>
      <p className="mk-settings-hint mk-sync-setup-error">
        Joining this sync repository would renumber existing local
        issues and rename existing local features or documents.
      </p>

      {renumbered.length > 0 && (
        <div className="mk-sync-setup-collision-banner">
          <div className="mk-settings-label">
            Renumbered issues ({renumbered.length})
          </div>
          <ul className="mk-sync-setup-collision-list">
            {renumbered.map((r) => (
              <li key={`${r.uuid}`}>
                <code>{r.prefix}-{r.oldNumber}</code> →{' '}
                <code>{r.prefix}-{r.newNumber}</code>
              </li>
            ))}
          </ul>
        </div>
      )}

      {renamed.length > 0 && (
        <div className="mk-sync-setup-collision-banner">
          <div className="mk-settings-label">
            Renamed {renamed.length === 1 ? 'entry' : 'entries'} ({renamed.length})
          </div>
          <ul className="mk-sync-setup-collision-list">
            {renamed.map((r) => (
              <li key={`${r.uuid}`}>
                <span className="mk-sync-setup-collision-kind">{r.kind}</span>{' '}
                <code>{r.old}</code> → <code>{r.new}</code>
              </li>
            ))}
          </ul>
        </div>
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

// SyncRadio is the mode-picker row primitive: a radio + label/title +
// helper text, with the sub-form indented under it when selected.
function SyncRadio({ name, value, checked, disabled, onChange, title, subtitle, children }) {
  return (
    <div className={`mk-sync-setup-radio ${checked ? 'is-checked' : ''} ${disabled ? 'is-disabled' : ''}`}>
      <label className="mk-sync-setup-radio-head">
        <input
          type="radio"
          name={name}
          value={value}
          checked={checked}
          disabled={disabled}
          onChange={() => onChange(value)}
        />
        <span className="mk-sync-setup-radio-title">{title}</span>
      </label>
      {subtitle && (
        <div className="mk-sync-setup-radio-subtitle">{subtitle}</div>
      )}
      {checked && !disabled && (
        <div className="mk-sync-setup-form-fields">{children}</div>
      )}
    </div>
  );
}
