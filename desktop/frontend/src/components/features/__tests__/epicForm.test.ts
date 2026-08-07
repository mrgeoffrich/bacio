import { describe, it, expect } from 'vitest';
import type { FeatureSummary } from '../../../api';
import {
  EMPTY_SLUG,
  RESERVED_SLUGS,
  SLUG_MAX_LEN,
  checkSlug,
  deriveSlug,
  errorMessage,
  isReservedSlug,
  isSlugCollisionError,
  previewSlug,
  slugTaken,
} from '../epicForm';

// epicForm holds the pure pre-flight the New Epic / Edit Epic forms run
// before any round trip. The one thing these tests are really pinning is
// that `deriveSlug` stays a faithful mirror of `store.Slugify`
// (internal/store/features.go:16-27) — if it drifts, the live preview
// starts lying about what the server will actually create.

function feature(over: Partial<FeatureSummary> = {}): FeatureSummary {
  return {
    slug: 'auth',
    title: 'Auth',
    emoji: '',
    state: 'active',
    branchName: '',
    updatedAt: '2026-01-01T00:00:00Z',
    hiddenOnBoard: false,
    ...over,
  };
}

describe('deriveSlug mirrors store.Slugify', () => {
  it('lowercases and joins words with a single hyphen', () => {
    expect(deriveSlug('Unified Create Affordance')).toBe('unified-create-affordance');
    expect(deriveSlug('AUTH')).toBe('auth');
  });

  it('collapses every run of non-[a-z0-9] into one hyphen', () => {
    // The Go regex is `[^a-z0-9]+` — a RUN, not a character, so a burst of
    // punctuation is one hyphen and not five.
    expect(deriveSlug('auth // session __ hardening')).toBe('auth-session-hardening');
    expect(deriveSlug('a  b')).toBe('a-b');
    expect(deriveSlug('épée')).toBe('p-e');
  });

  it('strips leading and trailing hyphens', () => {
    expect(deriveSlug('  --Auth--  ')).toBe('auth');
    expect(deriveSlug('!!!bugs!!!')).toBe('bugs');
  });

  it('falls back to "feature" when nothing survives', () => {
    expect(deriveSlug('')).toBe(EMPTY_SLUG);
    expect(deriveSlug('   ')).toBe(EMPTY_SLUG);
    expect(deriveSlug('!!!')).toBe(EMPTY_SLUG);
  });

  it('truncates to 60 characters AFTER trimming hyphens, exactly as Go does', () => {
    const long = deriveSlug('a'.repeat(80));
    expect(long).toHaveLength(SLUG_MAX_LEN);
    // The order matters: Go trims dashes first, then slices, so a result
    // may legitimately end in `-`. A "tidier" trim-after-slice here would
    // make the preview disagree with the server.
    const trailing = deriveSlug(`${'a'.repeat(59)} b`);
    expect(trailing).toHaveLength(SLUG_MAX_LEN);
    expect(trailing.endsWith('-')).toBe(true);
  });

  it('leaves digits and existing hyphens alone', () => {
    expect(deriveSlug('baci-410 follow-up')).toBe('baci-410-follow-up');
  });
});

describe('previewSlug', () => {
  it('never reaches the EMPTY_SLUG fallback for a field the user emptied', () => {
    // deriveSlug faithfully mirrors Go and answers `feature` here. A form
    // field is a different question: blank means "no slug", and submitting
    // `feature` would create an epic at an address nobody typed.
    expect(previewSlug('')).toBe('');
    expect(previewSlug('   ')).toBe('');
  });

  it('otherwise agrees with deriveSlug exactly', () => {
    expect(previewSlug('Unified Create Affordance')).toBe('unified-create-affordance');
    expect(previewSlug('My Epic')).toBe(deriveSlug('My Epic'));
    // Input that reduces to nothing but was not blank keeps Go's fallback:
    // the server would derive `feature` from it too.
    expect(previewSlug('!!!')).toBe(EMPTY_SLUG);
  });
});

describe('reserved slugs', () => {
  it('reserves exactly `new` — the create page owns that address', () => {
    expect([...RESERVED_SLUGS]).toEqual(['new']);
    expect(isReservedSlug('new')).toBe(true);
    expect(isReservedSlug('new-thing')).toBe(false);
    expect(isReservedSlug('renew')).toBe(false);
  });

  it('is reachable from a title, which is why the check exists', () => {
    // store.Slugify("New") is "new", so a plainly-typed title lands on it.
    expect(deriveSlug('New')).toBe('new');
    expect(checkSlug([], deriveSlug('New'))).toEqual({ kind: 'reserved' });
  });
});

describe('slugTaken / checkSlug', () => {
  const features = [feature({ slug: 'auth', title: 'Auth & session' }), feature({ slug: 'bugs', title: 'Bugs' })];

  it('finds the epic already holding a slug', () => {
    expect(slugTaken(features, 'bugs')?.title).toBe('Bugs');
    expect(slugTaken(features, 'nope')).toBeNull();
    // An empty slug is not a collision — the server derives one.
    expect(slugTaken(features, '')).toBeNull();
  });

  it('reports the collision with the owning epic so the message can name it', () => {
    expect(checkSlug(features, 'auth')).toEqual({ kind: 'taken', feature: features[0] });
    expect(checkSlug(features, 'fresh')).toBeNull();
  });

  it('checks reserved before taken', () => {
    const withNew = [...features, feature({ slug: 'new', title: 'New' })];
    expect(checkSlug(withNew, 'new')).toEqual({ kind: 'reserved' });
  });
});

describe('isSlugCollisionError', () => {
  it('recognises the raw SQLite text the store lets through', () => {
    expect(
      isSlugCollisionError(new Error('UNIQUE constraint failed: features.repo_id, features.slug')),
    ).toBe(true);
    // The HTTP transport wraps the same text in its {error} envelope.
    expect(isSlugCollisionError({ message: 'UNIQUE constraint failed: features.slug' })).toBe(true);
  });

  it('does not swallow unrelated failures', () => {
    expect(isSlugCollisionError(new Error('network down'))).toBe(false);
    expect(
      isSlugCollisionError(new Error('UNIQUE constraint failed: kanban_columns.name')),
    ).toBe(false);
    expect(isSlugCollisionError(null)).toBe(false);
  });
});

describe('errorMessage', () => {
  it('narrows the shapes both transports actually reject with', () => {
    expect(errorMessage(new Error('boom'))).toBe('boom');
    expect(errorMessage({ message: 'envelope' })).toBe('envelope');
    expect(errorMessage('plain string')).toBe('plain string');
    expect(errorMessage(null)).toBe('Something went wrong');
  });
});
