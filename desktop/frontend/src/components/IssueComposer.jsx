import React, { useState, useRef, useEffect, useCallback } from 'react';
import Modal from './Modal.jsx';
import * as api from '../api';
import { reportError } from '../errors';

// IssueComposer (BACI-166) — the "+ from prompt" modal launched from the
// Topbar's `+` button. The user types a one-line idea (and optionally a
// title); submit chains api.addIssue → api.dispatchIssue(_, _, 'scope')
// in one click so the scope worker fills in the structured ticket in the
// background while the user lands in the new issue's workspace.
//
// Failure modes (design pass committed to these in BACI-165):
//   - create fails: the modal stays open with an inline error so the
//     user can retry without losing their typed content.
//   - create succeeds, dispatch fails: surface reportError so the
//     orphan-issue case is visible, but still close the modal and call
//     onCreated(newCard) so the operator lands on the freshly-created
//     todo card and can re-dispatch from the card's existing dropdown.
//
// Props:
//   - open: boolean controlling Modal open state.
//   - onClose(): close handler (X / Cancel / Escape).
//   - repoPrefix: the active board prefix; required (the composer is
//     hidden when "all" is active, but defend at the API boundary too).
//   - onCreated(newCard): fires on a successful create — App.jsx
//     prepends the optimistic card, opens IssueWorkspace, and bumps the
//     refresh poll. The composer itself is unaware of the routing.
export default function IssueComposer({ open, onClose, repoPrefix, onCreated }) {
  const [title, setTitle] = useState('');
  const [description, setDescription] = useState('');
  const [inFlight, setInFlight] = useState(false);
  const [error, setError] = useState('');
  const descriptionRef = useRef(null);

  // Autofocus the description on open — title is optional per the
  // design (the worker derives one from the description when empty),
  // and the description is where the user's idea lives.
  useEffect(() => {
    if (open) {
      setTitle('');
      setDescription('');
      setError('');
      setInFlight(false);
      // requestAnimationFrame so the textarea exists in the DOM by the
      // time we reach for it (Radix Dialog mounts on the next tick).
      requestAnimationFrame(() => {
        descriptionRef.current?.focus();
      });
    }
  }, [open]);

  const close = useCallback(() => {
    if (inFlight) return;
    onClose?.();
  }, [inFlight, onClose]);

  const submit = useCallback(async (e) => {
    e?.preventDefault?.();
    const trimmedDesc = description.trim();
    if (!trimmedDesc) return;
    // The store rejects empty titles; derive one from the first ~60
    // chars of the description when the user didn't supply one, so the
    // server-side `title is required` guard never trips on a real
    // submit. The worker rewrites the title in the scoping pass anyway.
    const effectiveTitle = title.trim() || trimmedDesc.split('\n')[0].slice(0, 60);
    setError('');
    setInFlight(true);
    let newCard;
    try {
      newCard = await api.addIssue(repoPrefix, effectiveTitle, trimmedDesc);
    } catch (err) {
      // Leave the modal open with an inline error so the user can
      // retry without losing their content. addIssue throws an Error
      // whose .message is the server envelope's error text.
      setError(err instanceof Error ? err.message : String(err));
      setInFlight(false);
      return;
    }
    // Create succeeded — close + route, then fire-and-forget the
    // scope dispatch. The design pass committed to surfacing the
    // orphan-issue case via reportError without blocking navigation.
    onCreated?.(newCard);
    onClose?.();
    try {
      await api.dispatchIssue(repoPrefix, newCard.key, 'scope');
    } catch (err) {
      reportError(err, {
        headline: "Scope dispatch couldn't be queued — issue was created and is in todo",
      });
    }
  }, [description, title, repoPrefix, onCreated, onClose]);

  const disabled = !description.trim() || inFlight;

  return (
    <Modal open={open} onClose={close} title="New issue">
      <form className="mk-issue-composer-form" onSubmit={submit}>
        <label className="mk-settings-row">
          <span className="mk-settings-label">Title (optional)</span>
          <input
            type="text"
            className="mk-tmpl-input"
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            placeholder="Leave blank to derive one from the description"
            disabled={inFlight}
            maxLength={200}
          />
        </label>
        <label className="mk-settings-row">
          <span className="mk-settings-label">Describe the issue</span>
          <textarea
            ref={descriptionRef}
            className="mk-issue-composer-textarea"
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            placeholder="A rough one-liner — the scope worker will turn it into a triage-ready ticket."
            disabled={inFlight}
            rows={6}
          />
        </label>
        {error && (
          <p className="mk-settings-hint mk-issue-composer-error" role="alert">
            {error}
          </p>
        )}
        <div className="mk-modal-actions">
          <Modal.Close asChild>
            <button type="button" className="mk-btn" disabled={inFlight}>
              Cancel
            </button>
          </Modal.Close>
          <button type="submit" className="mk-btn mk-btn-primary" disabled={disabled}>
            {inFlight ? 'Creating…' : 'Create & scope'}
          </button>
        </div>
      </form>
    </Modal>
  );
}
