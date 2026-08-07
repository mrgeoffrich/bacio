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

function mount(entries: string[] = ['/BACI/epics/new']) {
  return render(
    <MemoryRouter initialEntries={entries} initialIndex={entries.length - 1}>
      <RepoProvider>
        <Routes>
          <Route path="/:prefix/epics/new" element={<NewEpicPage />} />
          <Route path="/:prefix/epics" element={<div>EPICS LIST</div>} />
          <Route path="/:prefix/epics/:slug" element={<div>epic detail</div>} />
          <Route path="/:prefix/epics/:slug/edit" element={<div>EDIT EPIC</div>} />
          <Route path="/:prefix/issues" element={<div>KANBAN</div>} />
        </Routes>
      </RepoProvider>
    </MemoryRouter>,
  );
}

// Open the slug field for hand editing — the pencil beside "URL slug".
function editSlug() {
  fireEvent.click(screen.getByRole('button', { name: 'Edit' }));
  return screen.getByLabelText('URL slug') as HTMLInputElement;
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

describe('NewEpicPage — the hand-edited slug', () => {
  it('refuses an emptied slug instead of silently creating `feature`', async () => {
    mount();
    const title = await screen.findByLabelText('Title');
    fireEvent.change(title, { target: { value: 'Unified create affordance' } });

    const slug = editSlug();
    fireEvent.change(slug, { target: { value: '' } });

    // deriveSlug('') is store.Slugify's `feature` fallback. An empty field
    // is the user clearing it, not a request for an epic called `feature`.
    expect(screen.getByText(/Give the epic a slug/)).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Create epic' })).toBeDisabled();
    fireEvent.click(screen.getByRole('button', { name: 'Create epic' }));
    expect(createFeature).not.toHaveBeenCalled();
  });

  it('says what a typed slug will actually become, and sends that', async () => {
    createFeature.mockResolvedValue({ slug: 'my-epic' });
    mount();
    const title = await screen.findByLabelText('Title');
    fireEvent.change(title, { target: { value: 'Something else' } });

    const slug = editSlug();
    fireEvent.change(slug, { target: { value: 'My Epic' } });
    // The input keeps what was typed, so the page has to say out loud that
    // the address will differ.
    expect(screen.getByText('my-epic')).toBeTruthy();

    fireEvent.click(screen.getByRole('button', { name: 'Create epic' }));
    await waitFor(() => expect(createFeature).toHaveBeenCalledTimes(1));
    expect(createFeature.mock.calls[0][2]).toBe('my-epic');
  });

  it('goes back to following the title on Reset', async () => {
    mount();
    const title = await screen.findByLabelText('Title');
    fireEvent.change(title, { target: { value: 'Unified create affordance' } });
    fireEvent.change(editSlug(), { target: { value: '' } });

    fireEvent.click(screen.getByRole('button', { name: 'Reset' }));
    expect(screen.getByText('unified-create-affordance')).toBeTruthy();
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Create epic' })).not.toBeDisabled(),
    );
  });
});

describe('NewEpicPage — leaving the form', () => {
  it('lands on the Epics list, whatever the user arrived from', async () => {
    // The button says "Epics" and Cancel sits beside it, so both go there.
    // Popping history would send the user back to the Kanban instead.
    mount(['/BACI/issues', '/BACI/epics/new']);
    fireEvent.click(await screen.findByRole('button', { name: /Epics/ }));
    expect(await screen.findByText('EPICS LIST')).toBeTruthy();
    expect(screen.queryByText('KANBAN')).toBeNull();
  });

  it('cancels the form on Escape', async () => {
    mount();
    const title = await screen.findByLabelText('Title');
    fireEvent.keyDown(title, { key: 'Escape' });
    expect(await screen.findByText('EPICS LIST')).toBeTruthy();
  });

  it('does NOT cancel when Escape only dismissed the emoji picker', async () => {
    // The picker portals its menu out of the form, but React still bubbles
    // the keystroke through the tree — so the form has to tell an Escape
    // aimed at the picker from one aimed at itself.
    mount();
    const title = await screen.findByLabelText('Title');
    fireEvent.change(title, { target: { value: 'Unified create affordance' } });

    fireEvent.keyDown(screen.getByRole('button', { name: 'Set epic emoji' }), { key: 'Enter' });
    const menu = await screen.findByRole('menu');
    fireEvent.keyDown(menu, { key: 'Escape', code: 'Escape' });

    expect(screen.queryByText('EPICS LIST')).toBeNull();
    expect((screen.getByLabelText('Title') as HTMLInputElement).value)
      .toBe('Unified create affordance');
  });
});

describe('NewEpicPage — an epic already slugged `new`', () => {
  it('explains the clash instead of silently redirecting to an edit form', async () => {
    listFeatures.mockResolvedValue([feature('new', 'New')]);
    mount();

    expect(await screen.findByText(/already uses the slug/)).toBeTruthy();
    // No create form to fill in, and no unexplained Edit page either.
    expect(screen.queryByLabelText('Title')).toBeNull();
    expect(screen.queryByText('EDIT EPIC')).toBeNull();
    // ...but that epic is still one click away.
    fireEvent.click(screen.getByRole('button', { name: /Edit the/ }));
    expect(await screen.findByText('EDIT EPIC')).toBeTruthy();
  });
});
