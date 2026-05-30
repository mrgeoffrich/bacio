import React, { useState, useRef, useEffect, useCallback } from 'react';
import Modal from './Modal.jsx';
import * as api from '../api';

// IssueComposer (BACI-166) — the "+ from prompt" modal launched from the
// Topbar's `+` button. The user types a one-line idea (and optionally a
// title); submit creates the issue and routes the operator into its
// workspace. BACI-300 retired the auto-scope dispatch this used to chain
// — triage now runs as a Pipeline stage (drag the card into the Pipeline
// and pick Scope), so the composer just creates the card.
//
// Failure mode:
//   - create fails: the modal stays open with an inline error so the
//     user can retry without losing their typed content.
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
  // Phase 4: features are mandatory, so the composer offers a feature
  // picker. `features` is the repo's feature list; `featureSlug` is the
  // current selection, pre-seeded to the repo default. An empty slug
  // still defers to the store's default-feature resolution, so a repo
  // with no features / no default stays creatable.
  const [features, setFeatures] = useState([]);
  const [featureSlug, setFeatureSlug] = useState('');

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

  // Phase 4: load the repo's features + default when the composer opens
  // so the picker is populated and pre-selected. Both calls are
  // best-effort — a failure leaves the picker on "Default feature" and
  // the create still works (empty slug → store default).
  useEffect(() => {
    if (!open || !repoPrefix || repoPrefix === 'all') {
      setFeatures([]);
      setFeatureSlug('');
      return;
    }
    let cancelled = false;
    api.listFeatures(repoPrefix)
      .then((fs) => { if (!cancelled) setFeatures(fs.filter(f => (f.state || 'active') === 'active')); })
      .catch(() => { if (!cancelled) setFeatures([]); });
    api.getDefaultFeature(repoPrefix)
      .then((d) => { if (!cancelled && d?.slug) setFeatureSlug(d.slug); })
      .catch(() => { /* no default — leave on "Default feature" */ });
    return () => { cancelled = true; };
  }, [open, repoPrefix]);

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
    // submit. A later triage pass can rewrite the title.
    const effectiveTitle = title.trim() || trimmedDesc.split('\n')[0].slice(0, 60);
    setError('');
    setInFlight(true);
    let newCard;
    try {
      newCard = await api.addIssue(repoPrefix, effectiveTitle, trimmedDesc, featureSlug);
    } catch (err) {
      // Leave the modal open with an inline error so the user can
      // retry without losing their content. addIssue throws an Error
      // whose .message is the server envelope's error text.
      setError(err instanceof Error ? err.message : String(err));
      setInFlight(false);
      return;
    }
    // Create succeeded — close + route into the new card's workspace.
    onCreated?.(newCard);
    onClose?.();
  }, [description, title, repoPrefix, featureSlug, onCreated, onClose]);

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
            placeholder="A rough one-liner — flesh it out later or scope it from the Pipeline."
            disabled={inFlight}
            rows={6}
          />
        </label>
        {/* Phase 4: feature picker. Features are mandatory; the select is
            pre-seeded to the repo default. "Default feature" maps to an
            empty slug, which the store resolves to the repo default at
            the boundary — so a repo with no features still creates. */}
        <label className="mk-settings-row">
          <span className="mk-settings-label">Feature</span>
          <select
            className="mk-tmpl-input"
            value={featureSlug}
            onChange={(e) => setFeatureSlug(e.target.value)}
            disabled={inFlight}
          >
            <option value="">Default feature</option>
            {features.map((f) => (
              <option key={f.slug} value={f.slug}>
                {f.emoji ? `${f.emoji} ` : ''}{f.title}
              </option>
            ))}
          </select>
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
            {inFlight ? 'Creating…' : 'Create'}
          </button>
        </div>
      </form>
    </Modal>
  );
}
