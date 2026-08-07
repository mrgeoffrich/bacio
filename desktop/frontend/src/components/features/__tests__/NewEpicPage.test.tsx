import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router';

// Render-level cover for the three things on the New Epic page that are
// behaviour rather than markup, and none of which the pure helpers can
// prove on their own:
//
//   1. the required-title gate — `Create epic` is disabled until a title
//      is typed, and the inline message only appears after first blur.
//   2. the live slug preview — it tracks the title without a debounce.
//   3. the local slug pre-check — a collision refuses BEFORE any round
//      trip, which is the whole reason the check is client-side.

const createFeature = vi.fn();
const listFeatures = vi.fn();

vi.mock('../../../api', () => ({
  // Consumed by RepoProvider on mount.
  listBoards: () => Promise.resolve([
    { prefix: 'BACI', name: 'bacio', kind: 'git', issueCount: 0 },
  ]),
  listColumns: () => Promise.resolve([]),
  getLaunchRepo: () => Promise.resolve('BACI'),
  getDisplayPreferences: () => Promise.resolve({ showArchived: false }),
  // Consumed by NewEpicPage.
  listFeatures: (...args: unknown[]) => listFeatures(...args),
  createFeature: (...args: unknown[]) => createFeature(...args),
}));

const { default: NewEpicPage } = await import('../NewEpicPage');
const { RepoProvider } = await import('../../../state/RepoProvider');

function feature(slug: string, title: string) {
  return {
    slug,
    title,
    emoji: '',
    state: 'active',
    branchName: '',
    updatedAt: '2026-08-01T00:00:00Z',
    hiddenOnBoard: false,
  };
}

function mount() {
  return render(
    <MemoryRouter initialEntries={['/BACI/epics/new']}>
      <RepoProvider>
        <Routes>
          <Route path="/:prefix/epics/new" element={<NewEpicPage />} />
          <Route path="/:prefix/epics/:slug" element={<div>epic detail</div>} />
        </Routes>
      </RepoProvider>
    </MemoryRouter>,
  );
}

beforeEach(() => {
  createFeature.mockReset();
  listFeatures.mockReset();
  listFeatures.mockResolvedValue([]);
});

describe('NewEpicPage', () => {
  it('gates Create epic on a title, and only nags after first blur', async () => {
    mount();
    const create = await screen.findByRole('button', { name: 'Create epic' });
    expect(create).toBeDisabled();
    // An untouched empty field is not an error yet — nagging a field the
    // user has not reached is noise.
    expect(screen.queryByText('Give the epic a title.')).toBeNull();

    const title = screen.getByLabelText('Title');
    // A blur with no prior interaction does NOT nag — that is the
    // programmatic focus-steal Radix performs when the Topbar menu closes
    // behind this page, and it must not read as "the user left it empty".
    fireEvent.blur(title);
    expect(screen.queryByText('Give the epic a title.')).toBeNull();

    fireEvent.pointerDown(title);
    fireEvent.blur(title);
    expect(await screen.findByText('Give the epic a title.')).toBeTruthy();

    fireEvent.change(title, { target: { value: 'Unified create affordance' } });
    await waitFor(() => expect(create).not.toBeDisabled());
    expect(screen.queryByText('Give the epic a title.')).toBeNull();
  });

  it('derives the slug live and submits an EMPTY slug so the server stays authoritative', async () => {
    createFeature.mockResolvedValue({ slug: 'unified-create-affordance' });
    mount();
    const title = await screen.findByLabelText('Title');
    fireEvent.change(title, { target: { value: 'Unified Create Affordance' } });
    expect(screen.getByText('unified-create-affordance')).toBeTruthy();

    fireEvent.click(screen.getByRole('button', { name: 'Create epic' }));
    await waitFor(() => expect(createFeature).toHaveBeenCalledTimes(1));
    expect(createFeature).toHaveBeenCalledWith(
      'BACI',
      'Unified Create Affordance',
      '', // slug omitted — store.Slugify derives it
      '',
      '',
      '',
    );
  });

  it('refuses a colliding slug locally, before any round trip', async () => {
    listFeatures.mockResolvedValue([feature('bugs', 'Bugs')]);
    mount();
    const title = await screen.findByLabelText('Title');
    fireEvent.change(title, { target: { value: 'Bugs' } });

    expect(await screen.findByText(/Taken by/)).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Create epic' })).toBeDisabled();
    expect(createFeature).not.toHaveBeenCalled();
  });

  it('refuses the reserved `new` slug — it is this page’s own address', async () => {
    mount();
    const title = await screen.findByLabelText('Title');
    fireEvent.change(title, { target: { value: 'New' } });

    expect(await screen.findByText(/is reserved/)).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Create epic' })).toBeDisabled();
  });
});
