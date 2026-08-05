import { useState, useRef, useEffect, useCallback } from 'react';
import type { FormEvent } from 'react';
import Modal from './Modal';
import * as api from '../api';
import type { FeatureSummary, BoardCard } from '../api';
import { useActiveRepo } from '../state/RepoProvider';

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
// BACI-374 adds the auto-run switch: on by default, it hands the new card
// straight to the engine on the full Scope → Plan → Implement → Ship
// chain. Off gives today's behaviour — an inert card the operator drives
// themselves.
//
// Props:
//   - open: boolean controlling Modal open state.
//   - onClose(): close handler (X / Cancel / Escape).
//   - onCreated(newCard, autoRan): fires on a successful create — Shell
//     prepends the optimistic card and routes on `autoRan` (Pipeline when
//     the card is already running, the workspace otherwise). The composer
//     itself is unaware of the routing.
//
// BACI-361: the active repo prefix is read from useActiveRepo() rather than
// prop-drilled (the composer is hidden when "all" is active, but it still
// defends at the API boundary).
type IssueComposerProps = {
  open: boolean;
  onClose: () => void;
  onCreated: (newCard: BoardCard, autoRan: boolean) => void;
};

export default function IssueComposer({ open, onClose, onCreated }: IssueComposerProps) {
  const { activeBoard: repoPrefix } = useActiveRepo();
  const [title, setTitle] = useState('');
  const [description, setDescription] = useState('');
  const [inFlight, setInFlight] = useState(false);
  const [error, setError] = useState('');
  const descriptionRef = useRef<HTMLTextAreaElement>(null);
  // Phase 4: features are mandatory, so the composer offers a feature
  // picker. `features` is the repo's feature list; `featureSlug` is the
  // current selection, pre-seeded to the repo default. An empty slug
  // still defers to the store's default-feature resolution, so a repo
  // with no features / no default stays creatable.
  const [features, setFeatures] = useState<FeatureSummary[]>([]);
  const [featureSlug, setFeatureSlug] = useState('');
  // BACI-374: auto-run defaults on and resets to on every open — "defaults
  // to on" is per-issue, not a remembered preference.
  const [autoRun, setAutoRun] = useState(true);

  // Autofocus the description on open — title is optional per the
  // design (the worker derives one from the description when empty),
  // and the description is where the user's idea lives.
  useEffect(() => {
    if (open) {
      setTitle('');
      setDescription('');
      setError('');
      setInFlight(false);
      setAutoRun(true);
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

  const submit = useCallback(async (e?: FormEvent) => {
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
      newCard = await api.addIssue(repoPrefix, effectiveTitle, trimmedDesc, featureSlug, autoRun);
    } catch (err) {
      // Leave the modal open with an inline error so the user can
      // retry without losing their content. addIssue throws an Error
      // whose .message is the server envelope's error text.
      setError(err instanceof Error ? err.message : String(err));
      setInFlight(false);
      return;
    }
    // Create succeeded — close + let the shell route on whether the card
    // is already running under the engine.
    onCreated?.(newCard, autoRun);
    onClose?.();
  }, [description, title, repoPrefix, featureSlug, autoRun, onCreated, onClose]);

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
        {/* BACI-374: auto-run. The label names all four stages on purpose —
            leaving this on means four agents run unattended and the card can
            end in a PR, so the consequence has to be legible at the click. */}
        <div className="mk-settings-row mk-issue-composer-autorun">
          <label className="mk-pl-toggle">
            <span className="mk-settings-label">Auto-run · Scope → Plan → Implement → Ship</span>
            <button
              type="button"
              role="switch"
              aria-checked={autoRun}
              className={`mk-pl-switch${autoRun ? ' is-on' : ''}`}
              onClick={() => setAutoRun(v => !v)}
              disabled={inFlight}
            />
          </label>
          <p className="mk-settings-hint">
            {autoRun
              ? 'The card goes straight into the Pipeline and runs all four stages unattended.'
              : 'The card lands in the Backlog for you to drive yourself.'}
          </p>
        </div>
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
