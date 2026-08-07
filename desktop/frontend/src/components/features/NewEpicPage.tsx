import { useCallback, useEffect, useRef, useState } from 'react';
import type { FormEvent, KeyboardEvent } from 'react';
import { Link, Navigate, useLocation, useNavigate } from 'react-router';
import { ChevronLeft } from 'lucide-react';
import * as api from '../../api';
import type { FeatureSummary } from '../../api';
import { useAsyncResource } from '../../lib/hooks/useAsyncResource';
import { useActiveRepo } from '../../state/RepoProvider';
import { isValidBranchName } from '../../lib/branchName';
import { editEpicPath, featurePath, viewPath } from '../../lib/routes';
import FeatureEmojiPicker from '../FeatureEmojiPicker';
import {
  SLUG_MAX_LEN,
  checkSlug,
  deriveSlug,
  errorMessage,
  isSlugCollisionError,
} from './epicForm';

// NewEpicPage — the full-screen `/:prefix/epics/new` create form.
//
// A page rather than a modal, matching ProcessEditor's relationship to the
// Pipeline: five fields including a markdown description is past what
// `Modal` comfortably holds (IssueComposer at four is already at the
// edge), and a modal that has to scroll has become a bad page. The shell
// is `.mk-pe` verbatim — 720px is one measure of prose, which is what the
// description wants.
//
// Field order mirrors the detail pane deliberately: emoji + title on one
// row (a blank version of the thing it makes), then the live-derived
// slug, then description, then the optional branch.
//
// All three validations are client-side pre-flight; the server-side
// equivalents stay as the backstop. The one that matters is the slug
// collision — `features` has UNIQUE(repo_id, slug) and store.CreateFeature
// does not pre-check, so a duplicate would otherwise surface as raw SQLite
// text. The full epic list is already loaded here, so we refuse before the
// round trip and keep isSlugCollisionError as the fallback for the race.
export default function NewEpicPage() {
  const { activeBoard } = useActiveRepo();
  const navigate = useNavigate();
  const location = useLocation();
  const repoSelected = !!activeBoard && activeBoard !== 'all';

  // The epic list backs the local collision check and the route guard
  // below. Failures are silenced rather than routed to the error modal: a
  // modal over a create form is exactly what the design forbids, and
  // losing the pre-check only means the server's rejection does the work
  // instead.
  const { data: features } = useAsyncResource<FeatureSummary[]>(
    () => api.listFeatures(activeBoard),
    [],
    [activeBoard],
    { enabled: repoSelected, silent: true, errorHeadline: "Couldn't load epics" },
  );

  const [emoji, setEmoji] = useState('');
  const [title, setTitle] = useState('');
  const [titleTouched, setTitleTouched] = useState(false);
  const [description, setDescription] = useState('');
  const [branch, setBranch] = useState('');
  // The slug tracks the title until the user presses Edit. Silently
  // un-linking on first edit and never saying so is the trap here, so the
  // label changes and a Reset link appears.
  const [slugEdited, setSlugEdited] = useState(false);
  const [slugDraft, setSlugDraft] = useState('');
  const [inFlight, setInFlight] = useState(false);
  const [banner, setBanner] = useState('');

  // Autofocus lands on the title — but NOT via the `autoFocus` attribute.
  // This page is usually reached from the Topbar's Radix menu, and Radix
  // restores focus to its trigger as it closes; an attribute-autofocused
  // input is focused at mount, immediately blurred by that restore, and
  // the empty-title message fires before the user has touched anything.
  // A rAF-deferred focus lands after the restore instead.
  const titleRef = useRef<HTMLInputElement>(null);
  useEffect(() => {
    const raf = requestAnimationFrame(() => titleRef.current?.focus());
    return () => cancelAnimationFrame(raf);
  }, []);

  // Belt and braces for the same problem: only a blur that follows real
  // user interaction with this form counts as "touched". A programmatic
  // focus-steal is not the user leaving a field empty.
  const interacted = useRef(false);
  const markInteracted = () => { interacted.current = true; };

  const trimmedTitle = title.trim();
  const derived = trimmedTitle ? deriveSlug(trimmedTitle) : '';
  const slug = slugEdited ? deriveSlug(slugDraft) : derived;
  const slugProblem = checkSlug(features, slug);
  const branchCheck = isValidBranchName(branch);
  const branchError = branch && !branchCheck.ok ? branchCheck.reason : '';
  // The derived slug is capped at 60 characters by store.Slugify. Say so
  // when we hit it — otherwise the user is looking at a slug that isn't
  // the slug they think they typed.
  const slugTruncated = !slugEdited && deriveSlug(trimmedTitle).length >= SLUG_MAX_LEN;

  const canSubmit = !!trimmedTitle && !slugProblem && !branchError && !inFlight;

  const backToList = useCallback(() => {
    // A deep link / a reload has no in-app history to pop. react-router
    // stamps the first entry of a session with key 'default'.
    if (location.key !== 'default') navigate(-1);
    else navigate(viewPath(activeBoard, 'features'));
  }, [activeBoard, location.key, navigate]);

  const submit = useCallback(
    async (e?: FormEvent) => {
      e?.preventDefault();
      if (!canSubmit) return;
      setInFlight(true);
      setBanner('');
      try {
        // Send an empty slug unless the user explicitly edited one, so
        // the server's own Slugify stays authoritative and the preview is
        // only ever a preview.
        const created = await api.createFeature(
          activeBoard,
          trimmedTitle,
          slugEdited ? slug : '',
          description,
          emoji,
          branch,
        );
        navigate(featurePath(activeBoard, created.slug));
      } catch (err) {
        setInFlight(false);
        setBanner(
          isSlugCollisionError(err)
            ? `An epic with the slug “${slug}” already exists in this space. Pick another slug.`
            : errorMessage(err),
        );
      }
    },
    [activeBoard, branch, canSubmit, description, emoji, navigate, slug, slugEdited, trimmedTitle],
  );

  if (!repoSelected) {
    return (
      <div className="mk-pe">
        <div className="mk-pe-empty">Select a repository or workspace to create an epic.</div>
      </div>
    );
  }

  // Belt-and-braces route guard. `store.Slugify("New")` is "new", so an
  // epic created through the CLI with that slug would be shadowed by this
  // page forever — react-router ranks the static `new` segment above
  // `:slug`, so `featurePath(prefix, 'new')` can never reach it.
  //
  // DEVIATION, deliberate: the plan and the design both say to Navigate to
  // "its detail route", but that route IS this page — the redirect would
  // loop. The nearest reachable address for that epic is its Edit page
  // (`/:prefix/epics/new/edit`), which is a strictly deeper path and
  // therefore uncontested, so the guard sends the user there instead. The
  // epic stays visible and editable; the cost is that creating a new epic
  // in that space needs the CLI-created `new` epic renamed first. The
  // create form itself refuses the slug (RESERVED_SLUGS), so the UI can
  // never put a space into this state.
  if (features.some((f) => f.slug === 'new')) {
    return <Navigate to={editEpicPath(activeBoard, 'new')} replace />;
  }

  return (
    <div className="mk-pe">
      <header className="mk-pe-head">
        <button type="button" className="mk-epic-back" onClick={backToList}>
          <ChevronLeft size={14} strokeWidth={2} aria-hidden="true" />
          <span>Epics</span>
        </button>
        <div className="mk-pe-title">
          <span className="mk-pe-name">New epic</span>
        </div>
        <p className="mk-pe-sub">
          Epics group issues, share an integration branch, and can close themselves
          when every child is done.
        </p>
      </header>

      {/* Enter submits from every input for free (native form behaviour)
          and NOT from the textarea, where it inserts a newline — which is
          exactly the split the design asks for. Escape cancels, matching
          FeatureBranchEditor's revert-on-Escape convention; there is no
          unsaved-changes guard on a create form. */}
      <form
        className="mk-epic-form"
        onSubmit={submit}
        onPointerDownCapture={markInteracted}
        onKeyDownCapture={markInteracted}
        onKeyDown={(e: KeyboardEvent<HTMLFormElement>) => {
          if (e.key === 'Escape' && !inFlight) {
            e.preventDefault();
            backToList();
          }
        }}
      >
        {/* Emoji + title on one row, mirroring the detail pane's title row
            (.mk-features-title-row) so the create form looks like a blank
            version of the thing it makes. */}
        <div className="mk-epic-field">
          <div className="mk-epic-field-head">
            <label className="mk-features-prop-label" htmlFor="mk-epic-title">Title</label>
            <span className="mk-epic-req">required</span>
          </div>
          <div className="mk-epic-title-row">
            <FeatureEmojiPicker value={emoji} onSelect={setEmoji} disabled={inFlight} />
            <input
              id="mk-epic-title"
              ref={titleRef}
              type="text"
              className="mk-epic-input mk-epic-input-title"
              value={title}
              maxLength={200}
              disabled={inFlight}
              placeholder="What is this epic about?"
              onChange={(e) => setTitle(e.target.value)}
              onBlur={() => { if (interacted.current) setTitleTouched(true); }}
            />
          </div>
          {titleTouched && !trimmedTitle && (
            <p className="mk-features-prop-error" role="alert">Give the epic a title.</p>
          )}
          <p className="mk-features-prop-hint">
            The emoji is optional and rides on every kanban card under this epic.
          </p>
        </div>

        {/* The slug is the epic's identity in the URL, in `bacio feature
            show` and in the sync folder layout, so it is shown as the tail
            of the whole path rather than as a bare word. */}
        <div className="mk-epic-field">
          <div className="mk-epic-field-head">
            <label className="mk-features-prop-label" htmlFor="mk-epic-slug">URL slug</label>
            <span className="mk-epic-req is-optional">
              {slugEdited ? 'edited' : 'derived from the title'}
            </span>
            {slugEdited ? (
              <button
                type="button"
                className="mk-epic-linkbtn"
                disabled={inFlight}
                onClick={() => { setSlugEdited(false); setSlugDraft(''); }}
              >
                Reset
              </button>
            ) : (
              <button
                type="button"
                className="mk-epic-linkbtn"
                disabled={inFlight}
                onClick={() => { setSlugEdited(true); setSlugDraft(derived); }}
              >
                Edit
              </button>
            )}
          </div>
          <div className={`mk-epic-slug${slugProblem ? ' is-error' : ''}`}>
            <span className="mk-epic-slug-prefix">/{activeBoard}/epics/</span>
            {slugEdited ? (
              <input
                id="mk-epic-slug"
                type="text"
                className="mk-epic-input mk-epic-slug-input"
                value={slugDraft}
                maxLength={SLUG_MAX_LEN}
                disabled={inFlight}
                spellCheck={false}
                autoComplete="off"
                onChange={(e) => setSlugDraft(e.target.value)}
              />
            ) : (
              <span className="mk-epic-slug-value">
                {slug || <span className="mk-epic-slug-empty">…</span>}
              </span>
            )}
          </div>
          {slugProblem?.kind === 'reserved' && (
            <p className="mk-features-prop-error" role="alert">
              <code>new</code> is reserved — it is this page’s own address. Try <code>new-thing</code>.
            </p>
          )}
          {slugProblem?.kind === 'taken' && (
            <p className="mk-features-prop-error" role="alert">
              Taken by{' '}
              <Link to={featurePath(activeBoard, slugProblem.feature.slug)}>
                {slugProblem.feature.emoji ? `${slugProblem.feature.emoji} ` : ''}
                {slugProblem.feature.title}
              </Link>
              .
            </p>
          )}
          {slugTruncated && (
            <p className="mk-features-prop-hint">Slugs are capped at {SLUG_MAX_LEN} characters.</p>
          )}
          <p className="mk-features-prop-hint">
            Used by the URL, <code>bacio feature show</code>, and the sync folder{' '}
            <code>repos/{activeBoard}/features/&lt;slug&gt;/</code>.
          </p>
        </div>

        {/* A plain textarea, not InlineDescriptionEditor — that component
            is built around "there is a saved value, click to edit it",
            which is a state this page never has. */}
        <div className="mk-epic-field">
          <div className="mk-epic-field-head">
            <label className="mk-features-prop-label" htmlFor="mk-epic-desc">Description</label>
            <span className="mk-epic-req is-optional">optional · markdown</span>
          </div>
          <textarea
            id="mk-epic-desc"
            className="mk-epic-textarea"
            value={description}
            rows={6}
            disabled={inFlight}
            placeholder="What does this epic cover, and what would done look like?"
            onChange={(e) => setDescription(e.target.value)}
          />
        </div>

        <div className="mk-epic-field">
          <div className="mk-epic-field-head">
            <label className="mk-features-prop-label" htmlFor="mk-epic-branch">Integration branch</label>
            <span className="mk-epic-req is-optional">optional · this machine only</span>
          </div>
          <div className="mk-features-prop-input-wrap">
            <input
              id="mk-epic-branch"
              type="text"
              className="mk-features-prop-input"
              value={branch}
              placeholder="main (default)"
              spellCheck={false}
              autoComplete="off"
              disabled={inFlight}
              onChange={(e) => setBranch(e.target.value)}
            />
          </div>
          {branchError && <p className="mk-features-prop-error" role="alert">{branchError}</p>}
          <p className="mk-features-prop-hint">
            Issues under this epic ship to this branch instead of <code>main</code>.
            The branch is per-machine — it never reaches <code>feature.yaml</code>, so
            it does not sync.
          </p>
        </div>

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
          <button
            type="button"
            className="mk-btn-secondary"
            onClick={backToList}
            disabled={inFlight}
          >
            Cancel
          </button>
          <button type="submit" className="mk-btn-primary" disabled={!canSubmit}>
            {inFlight ? 'Creating…' : 'Create epic'}
          </button>
        </footer>
      </form>
    </div>
  );
}
