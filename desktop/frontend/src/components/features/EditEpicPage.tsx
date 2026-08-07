import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { FormEvent, KeyboardEvent } from 'react';
import { useNavigate, useParams } from 'react-router';
import { ChevronLeft, Lock } from 'lucide-react';
import * as api from '../../api';
import type { FeatureDetail, FeatureSummary } from '../../api';
import { useAsyncResource } from '../../lib/hooks/useAsyncResource';
import { useActiveRepo } from '../../state/RepoProvider';
import { useCards } from '../../state/CardsProvider';
import { isValidBranchName } from '../../lib/branchName';
import { useNavGuard } from '../../lib/navGuard';
import { featurePath, viewPath } from '../../lib/routes';
import Modal from '../Modal';
import FeatureEmojiPicker from '../FeatureEmojiPicker';
import FeaturePropertyToggle from './FeaturePropertyToggle';
import FeatureStateControl from './FeatureStateControl';
import { AUTO_CLOSE_OPTIONS, COLLECT_HANDOFFS_OPTIONS, SHOW_ON_BOARD_OPTIONS } from './constants';
import { useFeaturePropertyUpdate } from './useFeaturePropertyUpdate';
import { errorMessage } from './epicForm';

// EditEpicPage — the full-screen `/:prefix/epics/:slug/edit` form.
//
// It carries EVERYTHING about the epic, and it is additive: every inline
// affordance on the detail pane stays exactly where it is. The one field
// that has no other home anywhere in the app is the TITLE — there is no
// title editor on the detail pane and no per-field setter for it — which
// is the strongest single reason this page exists.
//
// ── The two-speed save, and why ──────────────────────────────────────
//
// DETAILS (title / description / emoji / branch) batch through ONE
// `updateFeature` PATCH behind `Save details`. That is the backend's
// native shape: the route has always decoded all four together, so a
// per-field Save would be four sequential round trips with four partial
// failure states and nothing honest to say about a half-applied edit.
//
// PROPERTIES (state / auto-close / collect-handoffs / show-on-board)
// apply IMMEDIATELY through the same optimistic setters the detail pane
// uses. Making the identical control behave differently depending on
// which screen you found it on would be a worse consistency violation
// than the one this work exists to fix — and each of those four has its
// own endpoint anyway, so batching them would re-introduce exactly the
// partial-failure shape the PATCH avoids.
//
// A mixed-mode form is only dangerous when the split is invisible, so it
// is carried by four redundant signals: a sunken surface, a card
// boundary, an explicit APPLIED IMMEDIATELY badge, and a Save button that
// names its own scope ("Save details", not "Save").
//
// ── The navigation guard ─────────────────────────────────────────────
//
// The design specifies react-router's `useBlocker`. That hook requires a
// DATA router (`createBrowserRouter`); this app mounts a plain
// `<BrowserRouter>` in main.tsx, so `useBlocker` throws
// "useBlocker must be used within a data router" on mount. Migrating the
// whole router is far outside this work, so the shell's navigation entry
// points funnel through `lib/navGuard` instead.
//
// That gives one confirm covering three classes of exit:
//   - the ones this page owns (Discard edits, Escape, the back link),
//   - a navigation started elsewhere in the shell (a Topbar tab, the repo
//     picker, a bell/Shipped deep-link, a New epic/New page menu item),
//     which arrives through the registered guard,
//   - browser close/reload, via `beforeunload`.
//
// All three land on the SAME modal because the pending exit is stored as
// a thunk: whatever was about to happen runs on Discard and is dropped on
// Keep editing. Adding a fourth exit means routing it through
// `requestNavigation`, not growing another branch here.

// SAVED_FLASH_MS is how long a property's "just persisted" receipt sits
// on screen. Long enough to be read after the eye has moved, short enough
// not to become permanent chrome.
const SAVED_FLASH_MS = 1200;

// EditableDetails is the batched half of the page — the four fields that
// wait for Save. Kept as one object so "dirty" is a single shallow
// compare against the loaded FeatureDetail rather than four booleans.
type EditableDetails = {
  title: string;
  description: string;
  emoji: string;
  branchName: string;
};

function detailsOf(detail: FeatureDetail): EditableDetails {
  return {
    title: detail.title ?? '',
    description: detail.description ?? '',
    emoji: detail.emoji ?? '',
    branchName: detail.branchName ?? '',
  };
}

function changedFields(draft: EditableDetails, base: EditableDetails): Set<keyof EditableDetails> {
  const out = new Set<keyof EditableDetails>();
  (Object.keys(draft) as (keyof EditableDetails)[]).forEach((k) => {
    if (draft[k] !== base[k]) out.add(k);
  });
  return out;
}

export default function EditEpicPage() {
  const { activeBoard } = useActiveRepo();
  const { slug = '' } = useParams();
  const navigate = useNavigate();
  const { refreshCards } = useCards();
  const repoSelected = !!activeBoard && activeBoard !== 'all';

  const {
    data: detail,
    loading,
    setData: setDetail,
  } = useAsyncResource<FeatureDetail | null>(
    () => api.getFeature(activeBoard, slug),
    null,
    [activeBoard, slug],
    { enabled: repoSelected && !!slug, silent: true },
  );

  // The Details draft, seeded from the loaded epic and re-seeded whenever
  // the epic identity changes. It deliberately does NOT re-seed on every
  // `detail` change: a property write returns a fresh FeatureDetail, and
  // clobbering the user's half-typed title with it would be the worst
  // possible consequence of flipping a toggle.
  const [draft, setDraft] = useState<EditableDetails | null>(null);
  const [baseline, setBaseline] = useState<EditableDetails | null>(null);
  const seededFor = useRef<string>('');
  useEffect(() => {
    if (!detail) return;
    const identity = `${activeBoard}/${detail.slug}`;
    if (seededFor.current === identity) return;
    seededFor.current = identity;
    setDraft(detailsOf(detail));
    setBaseline(detailsOf(detail));
  }, [activeBoard, detail]);

  const [saving, setSaving] = useState(false);
  const [banner, setBanner] = useState('');
  // Set when the epic has gone from under us (renamed / deleted / synced
  // away). Save is disabled and we deliberately do NOT navigate away from
  // a form holding unsaved text.
  const [gone, setGone] = useState(false);
  // The exit waiting on the user's answer, held as a thunk so a Topbar
  // navigation and the page's own back link share one modal. Wrapped in an
  // object because `useState` would otherwise treat a bare function as a
  // lazy initialiser / updater and call it.
  const [pendingExit, setPendingExit] = useState<{ run: () => void } | null>(null);
  const [flash, setFlash] = useState<string>('');
  const flashTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  // Memoised because `save` closes over it: a fresh Set every render would
  // re-arm that callback on every keystroke.
  const dirtyKeys = useMemo(
    () => (draft && baseline ? changedFields(draft, baseline) : new Set<keyof EditableDetails>()),
    [draft, baseline],
  );
  const dirty = dirtyKeys.size > 0;

  const branchCheck = isValidBranchName(draft?.branchName ?? '');
  const branchError = draft?.branchName && !branchCheck.ok ? branchCheck.reason : '';
  // An empty title would 400 at the store boundary (ValidateTitle), so
  // the button refuses before the round trip — same gate the New page
  // applies, for the same reason.
  const canSave = dirty && !saving && !gone && !branchError && !!draft?.title.trim();

  // Property writes flash a receipt beside their label. The timer is
  // cleared on unmount and on the next write so a rapid double-flip
  // doesn't leave a stale pill behind.
  const flashSaved = useCallback((key: string) => {
    if (flashTimer.current) clearTimeout(flashTimer.current);
    setFlash(key);
    flashTimer.current = setTimeout(() => setFlash(''), SAVED_FLASH_MS);
  }, []);
  useEffect(() => () => {
    if (flashTimer.current) clearTimeout(flashTimer.current);
  }, []);

  // The four properties reuse the detail pane's own reconcile/update pair
  // verbatim, so their behaviour here IS their behaviour there. The
  // summary-list refresh it fires is a no-op on this route (no list is
  // mounted) and harmless.
  const noopFeatures = useCallback((_features: FeatureSummary[]) => {}, []);
  const applyProperty = useCallback(
    (updated: FeatureDetail, key: string) => {
      setDetail(updated);
      flashSaved(key);
    },
    [flashSaved, setDetail],
  );
  const { update } = useFeaturePropertyUpdate(
    activeBoard,
    (updated) => applyProperty(updated, 'state'),
    noopFeatures,
  );

  const backToEpic = useCallback(() => {
    if (detail) navigate(featurePath(activeBoard, detail.slug));
    else navigate(viewPath(activeBoard, 'features'));
  }, [activeBoard, detail, navigate]);

  // Every exit this page owns funnels through here, so the confirm can
  // never be bypassed by picking a different button.
  const leave = useCallback(() => {
    if (dirty) setPendingExit({ run: backToEpic });
    else backToEpic();
  }, [backToEpic, dirty]);

  // …and every exit it does NOT own arrives here. Armed only while dirty,
  // so a clean page never interposes a modal on an ordinary tab click.
  useNavGuard(
    dirty,
    useCallback((proceed: () => void) => setPendingExit({ run: proceed }), []),
  );

  // Answering the modal. Discard runs whatever was pending — for an
  // external navigation that is the shell's own `proceed`, which unmounts
  // this page and takes the guard with it.
  const keepEditing = useCallback(() => setPendingExit(null), []);
  const discardAndGo = useCallback(() => {
    const go = pendingExit?.run;
    setPendingExit(null);
    go?.();
  }, [pendingExit]);

  // The web transport's last line of defence. Wails' webview does not
  // fire beforeunload reliably, so the desktop keeps the gap rather than
  // growing a second mechanism for it.
  useEffect(() => {
    if (!dirty) return;
    const onBeforeUnload = (e: BeforeUnloadEvent) => {
      e.preventDefault();
      e.returnValue = '';
    };
    window.addEventListener('beforeunload', onBeforeUnload);
    return () => window.removeEventListener('beforeunload', onBeforeUnload);
  }, [dirty]);

  const save = useCallback(
    async (e?: FormEvent) => {
      e?.preventDefault();
      if (!canSave || !draft || !baseline || !detail) return;
      setSaving(true);
      setBanner('');
      try {
        // Presence, not value: send only the keys that actually changed,
        // so an untouched field is never accidentally cleared.
        const updated = await api.updateFeature(activeBoard, detail.slug, {
          ...(dirtyKeys.has('title') ? { title: draft.title.trim() } : {}),
          ...(dirtyKeys.has('description') ? { description: draft.description } : {}),
          ...(dirtyKeys.has('emoji') ? { emoji: draft.emoji } : {}),
          ...(dirtyKeys.has('branchName') ? { branchName: draft.branchName } : {}),
        });
        // The emoji rides on every kanban card under this epic, so the
        // cached board is stale the moment it changes.
        if (dirtyKeys.has('emoji')) refreshCards({ silent: true });
        navigate(featurePath(activeBoard, updated.slug));
      } catch (err) {
        setSaving(false);
        const message = errorMessage(err);
        if (/not found|no such feature/i.test(message)) {
          setGone(true);
          setBanner(
            'This epic no longer exists — it may have been renamed or removed. ' +
            'Your edits are still here; copy anything you need.',
          );
        } else {
          setBanner(message);
        }
      }
    },
    [activeBoard, baseline, canSave, detail, dirtyKeys, draft, navigate, refreshCards],
  );

  if (!repoSelected) {
    return (
      <div className="mk-pe">
        <div className="mk-pe-empty">Select a repository or workspace to edit an epic.</div>
      </div>
    );
  }
  if (!detail || !draft) {
    return (
      <div className="mk-pe">
        <div className="mk-pe-empty">
          {loading ? 'Loading…' : (
            <>
              <p>That epic doesn’t exist any more.</p>
              <button
                type="button"
                className="mk-btn-secondary"
                onClick={() => navigate(viewPath(activeBoard, 'features'))}
              >
                Back to Epics
              </button>
            </>
          )}
        </div>
      </div>
    );
  }

  const patch = (next: Partial<EditableDetails>) => setDraft((d) => (d ? { ...d, ...next } : d));
  const edited = (key: keyof EditableDetails) =>
    dirtyKeys.has(key) ? <span className="mk-epic-edited-marker">edited</span> : null;
  const savedFlash = (key: string) =>
    flash === key ? <span className="mk-pill mk-status-done mk-epic-saved-flash">SAVED</span> : null;

  return (
    <div
      className="mk-pe"
      onKeyDown={(e: KeyboardEvent<HTMLDivElement>) => {
        // Escape goes THROUGH the guard, never around it.
        if (e.key === 'Escape' && !saving && !pendingExit) {
          e.preventDefault();
          leave();
        }
      }}
    >
      <header className="mk-pe-head">
        <button type="button" className="mk-epic-back" onClick={leave}>
          <ChevronLeft size={14} strokeWidth={2} aria-hidden="true" />
          <span>{detail.emoji ? `${detail.emoji} ` : ''}{detail.title}</span>
        </button>
        <div className="mk-pe-title">
          <span className="mk-pe-name">Edit epic</span>
          {dirty && (
            <span className="mk-pill mk-status-doing mk-epic-dirty-chip">
              UNSAVED · {dirtyKeys.size}
            </span>
          )}
        </div>
        <p className="mk-pe-sub">
          Everything about this epic in one place. The detail pane keeps its inline
          controls — this page is additive.
        </p>
      </header>

      <form className="mk-epic-form" onSubmit={save}>
        <section className="mk-epic-section">
          <div className="mk-epic-section-head">
            <span className="mk-epic-section-title">Details</span>
            <span className="mk-epic-section-note">— saved together when you press Save</span>
          </div>

          <div className="mk-epic-field">
            <div className="mk-epic-field-head">
              <label className="mk-features-prop-label" htmlFor="mk-epic-title">Title</label>
              {edited('title')}
            </div>
            <div className="mk-epic-title-row">
              <FeatureEmojiPicker
                value={draft.emoji}
                onSelect={(next) => patch({ emoji: next })}
                disabled={saving}
              />
              <input
                id="mk-epic-title"
                type="text"
                className="mk-epic-input mk-epic-input-title"
                value={draft.title}
                maxLength={200}
                disabled={saving}
                onChange={(e) => patch({ title: e.target.value })}
              />
            </div>
            {!draft.title.trim() && (
              <p className="mk-features-prop-error" role="alert">Give the epic a title.</p>
            )}
            {edited('emoji') && (
              <p className="mk-features-prop-hint">
                The emoji is part of Details — it saves with the rest, not on pick.
              </p>
            )}
          </div>

          {/* The one field where this page and the New page differ in KIND,
              so it looks different rather than merely being disabled:
              dashed border, muted type, a lock glyph. `readOnly` rather
              than `disabled` markup so it stays selectable and copyable. */}
          <div className="mk-epic-field">
            <div className="mk-epic-field-head">
              <label className="mk-features-prop-label">URL slug</label>
              <span className="mk-epic-req is-optional">read-only</span>
            </div>
            <div className="mk-epic-slug is-readonly">
              <span className="mk-epic-slug-prefix">/{activeBoard}/epics/</span>
              <span className="mk-epic-slug-value">{detail.slug}</span>
              <span className="mk-epic-slug-lock" aria-hidden="true">
                <Lock size={12} strokeWidth={2} />
              </span>
            </div>
            <p className="mk-features-prop-hint">
              Renaming a slug generates a sync redirect, which the sync layer models
              separately — not editable here.
            </p>
          </div>

          <div className="mk-epic-field">
            <div className="mk-epic-field-head">
              <label className="mk-features-prop-label" htmlFor="mk-epic-desc">Description</label>
              <span className="mk-epic-req is-optional">markdown</span>
              {edited('description')}
            </div>
            <textarea
              id="mk-epic-desc"
              className="mk-epic-textarea"
              value={draft.description}
              rows={8}
              disabled={saving}
              onChange={(e) => patch({ description: e.target.value })}
            />
          </div>

          <div className="mk-epic-field">
            <div className="mk-epic-field-head">
              <label className="mk-features-prop-label" htmlFor="mk-epic-branch">
                Integration branch
              </label>
              {edited('branchName')}
            </div>
            <div className="mk-features-prop-input-wrap">
              <input
                id="mk-epic-branch"
                type="text"
                className="mk-features-prop-input"
                value={draft.branchName}
                placeholder="main (default)"
                spellCheck={false}
                autoComplete="off"
                disabled={saving}
                onChange={(e) => patch({ branchName: e.target.value })}
              />
            </div>
            {branchError && <p className="mk-features-prop-error" role="alert">{branchError}</p>}
            <p className="mk-features-prop-hint">
              Per-machine — <code>branch_name</code> never reaches <code>feature.yaml</code>,
              so it does not sync.
            </p>
          </div>
        </section>

        {banner && (
          <div className="mk-pe-banner is-error" role="alert">
            {banner}
            <button
              type="button"
              className="mk-pe-banner-x"
              onClick={() => setBanner('')}
              aria-label="Dismiss"
            >
              ✕
            </button>
          </div>
        )}

        <footer className="mk-pe-foot">
          <button type="button" className="mk-btn-secondary" onClick={leave} disabled={saving}>
            Discard edits
          </button>
          <button type="submit" className="mk-btn-primary" disabled={!canSave}>
            {saving ? 'Saving…' : 'Save details'}
          </button>
        </footer>
      </form>

      {/* PROPERTIES sits OUTSIDE the <form> — belt and braces against a
          segmented button ever submitting the batched Details save. */}
      <section className="mk-epic-section is-sunken">
        <div className="mk-epic-section-head">
          <span className="mk-epic-section-title">Properties</span>
          <span className="mk-pill mk-status-done">APPLIED IMMEDIATELY</span>
          <span className="mk-epic-section-note">
            — same controls, same behaviour as the detail pane. No Save needed.
          </span>
        </div>
        <div className="mk-epic-props">
          <FeatureStateControl
            activeBoard={activeBoard}
            detail={detail}
            update={update}
            badge={savedFlash('state')}
          />
          <FeaturePropertyToggle
            label="Auto Close"
            ariaLabel="Auto-close this epic when every child is terminal"
            options={AUTO_CLOSE_OPTIONS}
            value={!detail.stateManual}
            persist={(next) => api.setFeatureAutoClose(activeBoard, detail.slug, next)}
            onApplied={(updated) => applyProperty(updated, 'autoClose')}
            errorHeadline="Couldn't update auto-close"
            hint="When on, the auto-completion sweep can promote this epic to done/cancelled once every child issue is terminal. Turn off for long-lived catch-all epics."
            badge={savedFlash('autoClose')}
          />
          <FeaturePropertyToggle
            label="Collect handoffs"
            ariaLabel="Collect worker handoff comments on this epic"
            options={COLLECT_HANDOFFS_OPTIONS}
            value={detail.collectHandoffs}
            persist={(next) => api.setFeatureCollectHandoffs(activeBoard, detail.slug, next)}
            onApplied={(updated) => applyProperty(updated, 'collectHandoffs')}
            errorHeadline="Couldn't update collect-handoffs"
            hint="When on, worker close-outs append a handoff comment so siblings inherit context. Turn off for standing buckets like bugs / maintenance."
            badge={savedFlash('collectHandoffs')}
          />
          <FeaturePropertyToggle
            label="Show on board"
            ariaLabel="Show this epic's cards on the board"
            options={SHOW_ON_BOARD_OPTIONS}
            value={!detail.hiddenOnBoard}
            persist={(next) => api.setFeatureHiddenOnBoard(activeBoard, detail.slug, !next)}
            onApplied={(updated) => {
              applyProperty(updated, 'hiddenOnBoard');
              // This one changes which cards ship over the wire.
              refreshCards();
            }}
            errorHeadline="Couldn't update visibility"
            hint="Hide this epic's cards from the kanban board."
            badge={savedFlash('hiddenOnBoard')}
          />
        </div>
      </section>

      {/* The second sentence is load-bearing: it is the moment the
          two-speed model has to pay for itself. */}
      <Modal
        open={pendingExit !== null}
        onClose={keepEditing}
        title="Discard your changes to this epic?"
      >
        <p className="mk-settings-hint">
          The title, description, emoji and branch you edited won’t be saved.
          Everything under Properties has already been applied.
        </p>
        <div className="mk-modal-actions">
          <button type="button" className="mk-btn" onClick={keepEditing}>
            Keep editing
          </button>
          <button
            type="button"
            className="mk-btn mk-btn-primary"
            onClick={discardAndGo}
          >
            Discard
          </button>
        </div>
      </Modal>
    </div>
  );
}
