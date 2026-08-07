import type { FeatureSummary } from '../../api';

// epicForm — the pure bits of the New Epic / Edit Epic forms, kept out of
// the components so they can be tested without a render (the same split
// kanbanPlacement.ts and docsActions.ts already use).
//
// Everything here is client-side *pre-flight*. The server stays
// authoritative on all of it: `store.Slugify` derives the real slug,
// `UNIQUE(repo_id, slug)` is the real collision check. What these
// functions buy is a live preview and a refusal that costs no round trip
// — and, for the reserved word, a rule that has no server-side equivalent
// at all (see RESERVED_SLUGS).

// SLUG_MAX_LEN mirrors the 60-character cap in store.Slugify
// (internal/store/features.go:16-27).
export const SLUG_MAX_LEN = 60;

// EMPTY_SLUG is what store.Slugify falls back to when the input reduces
// to nothing — a title of "!!!" really does become the epic `feature`.
export const EMPTY_SLUG = 'feature';

// deriveSlug is a faithful TS mirror of store.Slugify: lowercase, trim,
// every run of non-[a-z0-9] to a single `-`, strip leading/trailing `-`,
// empty falls back to `feature`, then truncate to 60.
//
// Two details worth not "improving":
//   - the truncation happens AFTER the dash-trim, so a 60-character
//     result may legitimately end in `-`. Go does the same; a tidier
//     order here would make the preview disagree with the server.
//   - Go slices bytes (`s[:60]`), JS slices UTF-16 units. They agree
//     because by this point every surviving character is ASCII.
export function deriveSlug(title: string): string {
  let s = title.toLowerCase().trim();
  s = s.replace(/[^a-z0-9]+/g, '-');
  s = s.replace(/^-+/, '').replace(/-+$/, '');
  if (s === '') s = EMPTY_SLUG;
  if (s.length > SLUG_MAX_LEN) s = s.slice(0, SLUG_MAX_LEN);
  return s;
}

// RESERVED_SLUGS are slugs the UI refuses because the router has already
// spent them. `new` is the whole list: `featurePath(prefix, 'new')` emits
// `/PREFIX/epics/new`, which is the New Epic page's own address, so an
// epic slugged `new` would be permanently unreachable and its list row
// would open the create form.
//
// Client-side ONLY, deliberately. Reserving it in `store.ValidateSlug`
// would also constrain `bacio feature add` and could reject an
// already-existing row on its next edit — a store-boundary change for a
// router-shaped problem. NewEpicPage additionally guards the route: if an
// epic slugged `new` somehow exists (created by the CLI before this
// shipped), the page redirects to its detail route rather than shadowing
// it forever.
export const RESERVED_SLUGS: ReadonlySet<string> = new Set(['new']);

export function isReservedSlug(slug: string): boolean {
  return RESERVED_SLUGS.has(slug);
}

// slugTaken returns the epic already holding this slug, or null. The
// caller has the full list loaded already (FeaturesView / NewEpicPage
// both fetch it), so the collision check is local and instant — and the
// message can name the culprit by TITLE, which is the useful thing: the
// most likely reason you hit this is that the epic you are creating
// already exists.
export function slugTaken(features: FeatureSummary[], slug: string): FeatureSummary | null {
  if (!slug) return null;
  return features.find((f) => f.slug === slug) ?? null;
}

// SlugProblem is the one validation verdict the slug field can be in.
// `null` means the slug is fine.
export type SlugProblem =
  | { kind: 'reserved' }
  | { kind: 'taken'; feature: FeatureSummary };

// checkSlug runs the two client-side slug rules in the order the user
// meets them. Emptiness is NOT checked here: an empty slug is legal on
// the wire (the server derives one), and the create form only ever sends
// a derived or explicitly-edited value.
export function checkSlug(features: FeatureSummary[], slug: string): SlugProblem | null {
  if (isReservedSlug(slug)) return { kind: 'reserved' };
  const owner = slugTaken(features, slug);
  if (owner) return { kind: 'taken', feature: owner };
  return null;
}

// isSlugCollisionError recognises the store's raw SQLite text so the
// create form can render the friendly line instead of it.
//
// This is the fallback for the genuine race — two windows, same slug,
// same second — that the local pre-check cannot see. `store.CreateFeature`
// does not pre-check the UNIQUE index, and neither transport substitutes
// a friendly message (compare createKanbanColumn, which does). Matching
// the text is unlovely; the alternative is a store change that the design
// puts out of scope.
export function isSlugCollisionError(err: unknown): boolean {
  const message =
    err instanceof Error
      ? err.message
      : typeof err === 'object' && err !== null && 'message' in err
        ? String((err as { message: unknown }).message)
        : String(err ?? '');
  return /UNIQUE constraint failed: features/i.test(message);
}

// errorMessage pulls a displayable string off an unknown rejection —
// the same narrowing FeatureBranchEditor does inline, hoisted so both
// epic pages share it.
export function errorMessage(err: unknown): string {
  if (err instanceof Error && err.message) return err.message;
  if (typeof err === 'object' && err !== null && 'message' in err) {
    const m = String((err as { message: unknown }).message);
    if (m) return m;
  }
  const s = String(err ?? '');
  return s || 'Something went wrong';
}
