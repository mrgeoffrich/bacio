import React, { useState, useRef, useEffect } from 'react';
import Modal from './Modal';
import Icon from './Icon';
import { WEB_MODE } from '../env';

// RepoPicker is the topbar's repository selector — a searchable dropdown that
// replaces the plain native <select>. Clicking the trigger opens a menu with
// a filter input, the matching repos, and an "Add Repository" action.
//
// Desktop mode pops a native folder picker via Wails. Web mode pops a
// path-input modal that POSTs the typed path to /repos (BACI-50). The
// onAddRepository callback handles both: desktop ignores its payload,
// web reads {path, name, prefix?} off it.
export default function RepoPicker({ boards, activeBoard, onPick, onAddRepository }) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState('');
  // Web-only: { path, name, prefix } modal state. Null = closed.
  const [addingWeb, setAddingWeb] = useState(null);
  const [addError, setAddError] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const rootRef = useRef(null);
  const inputRef = useRef(null);

  const active = boards.find(b => b.prefix === activeBoard);
  const label = active?.name || activeBoard || 'Select repository';

  // While open: focus the filter, and close on Escape or an outside click.
  // The outside-click handler is suspended while the Add-Repository modal
  // is open so a click inside the modal doesn't dismiss the dropdown
  // underneath it (which would unmount the modal mid-edit).
  useEffect(() => {
    if (!open) {
      setQuery('');
      return;
    }
    inputRef.current?.focus();
    const onDown = (e) => {
      if (addingWeb) return;
      if (rootRef.current && !rootRef.current.contains(e.target)) setOpen(false);
    };
    const onKey = (e) => {
      if (e.key !== 'Escape') return;
      if (addingWeb) return; // the modal handles its own Escape
      setOpen(false);
    };
    document.addEventListener('mousedown', onDown);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onDown);
      document.removeEventListener('keydown', onKey);
    };
  }, [open, addingWeb]);

  const q = query.trim().toLowerCase();
  const filtered = q
    ? boards.filter(b =>
        b.name.toLowerCase().includes(q) || b.prefix.toLowerCase().includes(q))
    : boards;

  const pick = (prefix) => {
    onPick(prefix);
    setOpen(false);
  };

  // Desktop path: hand straight off to the native folder picker.
  const addDesktop = () => {
    setOpen(false);
    onAddRepository();
  };

  // Web path: open the inline modal, gather path/name/prefix, then submit.
  const openWebModal = () => {
    setAddError('');
    setAddingWeb({ path: '', name: '', prefix: '' });
  };
  const closeWebModal = () => {
    if (submitting) return;
    setAddingWeb(null);
    setAddError('');
  };
  const submitWebAdd = async () => {
    if (!addingWeb || submitting) return;
    const path = addingWeb.path.trim();
    const name = addingWeb.name.trim();
    if (!path || !name) {
      setAddError('Both path and name are required.');
      return;
    }
    setSubmitting(true);
    setAddError('');
    try {
      const result = await onAddRepository({
        path,
        name,
        prefix: addingWeb.prefix.trim() || undefined,
      });
      if (result) {
        setAddingWeb(null);
        setOpen(false);
      }
    } catch (err) {
      // App.jsx already routes the failure through the global error
      // modal; surface the message inline too so the modal stays open
      // and the user can correct the typed path.
      setAddError(err?.message || 'Failed to add repository');
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="mk-repo-picker" ref={rootRef}>
      <button
        className="mk-repo-picker-trigger"
        onClick={() => setOpen(o => !o)}
        aria-haspopup="listbox"
        aria-expanded={open}
      >
        <Icon name="branch" />
        <span className="mk-repo-picker-label">{label}</span>
        {active && (
          <>
            <span className="mk-repo-picker-sep">·</span>
            <span className="mk-repo-picker-code">{active.prefix}</span>
          </>
        )}
      </button>

      {open && (
        <div className="mk-repo-picker-menu" role="listbox">
          <input
            ref={inputRef}
            className="mk-repo-picker-search"
            type="text"
            placeholder="Search repositories…"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
          />
          <div className="mk-repo-picker-list">
            {filtered.length === 0 ? (
              <div className="mk-repo-picker-empty">No matching repositories.</div>
            ) : (
              filtered.map(b => (
                <button
                  key={b.prefix}
                  className={`mk-repo-picker-item ${b.prefix === activeBoard ? 'is-active' : ''}`}
                  onClick={() => pick(b.prefix)}
                  role="option"
                  aria-selected={b.prefix === activeBoard}
                >
                  <span className="mk-repo-picker-item-name">{b.name}</span>
                  <span className="mk-repo-picker-item-prefix">{b.prefix}</span>
                </button>
              ))
            )}
          </div>
          <button
            className="mk-repo-picker-add"
            onClick={WEB_MODE ? openWebModal : addDesktop}
          >
            <Icon name="plus" />
            Add Repository…
          </button>
        </div>
      )}

      <Modal
        open={!!addingWeb}
        onClose={() => { if (!submitting) closeWebModal(); }}
        title="Add Repository"
      >
        {addingWeb && (
          <>
            <p className="mk-settings-hint">
              Path is on the server&apos;s filesystem
              {' '}({window.location.host || 'this host'}), not your local machine.
              Point at the git working tree you want bacio to track.
            </p>
            <label className="mk-tmpl-add-field">
              <span>Path</span>
              <input
                autoFocus
                className="mk-tmpl-input"
                value={addingWeb.path}
                placeholder="/Users/you/Code/my-project"
                onChange={e => setAddingWeb({ ...addingWeb, path: e.target.value })}
              />
            </label>
            <label className="mk-tmpl-add-field">
              <span>Name</span>
              <input
                className="mk-tmpl-input"
                value={addingWeb.name}
                placeholder="my-project"
                onChange={e => setAddingWeb({ ...addingWeb, name: e.target.value })}
              />
            </label>
            <label className="mk-tmpl-add-field">
              <span>Prefix (optional)</span>
              <input
                className="mk-tmpl-input"
                value={addingWeb.prefix}
                placeholder="MYPR (auto-allocated if blank)"
                maxLength={4}
                onChange={e => setAddingWeb({ ...addingWeb, prefix: e.target.value })}
              />
            </label>
            {addError && (
              <p className="mk-settings-hint" style={{ color: 'var(--status-blocked, #d44)' }}>
                {addError}
              </p>
            )}
            <div className="mk-modal-actions">
              <button
                className="mk-segmented-btn"
                onClick={closeWebModal}
                disabled={submitting}
              >
                Cancel
              </button>
              <button
                className="mk-segmented-btn is-active"
                onClick={submitWebAdd}
                disabled={submitting}
              >
                {submitting ? 'Adding…' : 'Add'}
              </button>
            </div>
          </>
        )}
      </Modal>
    </div>
  );
}
