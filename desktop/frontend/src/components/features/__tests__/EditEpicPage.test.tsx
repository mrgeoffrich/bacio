import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor, within } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router';

// Render-level cover for the Edit Epic page. Everything here is behaviour
// the markup can't prove on its own, and it splits along the page's one
// load-bearing idea — the two-speed save:
//
//   1. DETAILS batch. One `updateFeature` call carrying ONLY the keys that
//      actually changed, so an untouched field is never cleared.
//   2. PROPERTIES apply immediately. No Save press, same setters the
//      detail pane uses.
//   3. The navigation guard. A dirty page interposes one confirm over
//      every exit — including a navigation started elsewhere in the shell,
//      which is the gap `useBlocker` would have covered if this app used a
//      data router.

const getFeature = vi.fn();
const updateFeature = vi.fn();
const setFeatureAutoClose = vi.fn();
const refreshCards = vi.fn();

vi.mock('../../../api', () => ({
  // Consumed by RepoProvider on mount.
  listBoards: () => Promise.resolve([
    { prefix: 'BACI', name: 'bacio', kind: 'git', issueCount: 0 },
  ]),
  listColumns: () => Promise.resolve([]),
  getLaunchRepo: () => Promise.resolve('BACI'),
  getDisplayPreferences: () => Promise.resolve({ showArchived: false }),
  // Consumed by EditEpicPage.
  getFeature: (...args: unknown[]) => getFeature(...args),
  updateFeature: (...args: unknown[]) => updateFeature(...args),
  setFeatureAutoClose: (...args: unknown[]) => setFeatureAutoClose(...args),
  setFeatureCollectHandoffs: () => Promise.resolve(detail()),
  setFeatureHiddenOnBoard: () => Promise.resolve(detail()),
  setFeatureState: () => Promise.resolve(detail()),
}));

// The board cache is irrelevant here but the page reads `refreshCards` off
// it, so the provider is stubbed rather than mounted.
vi.mock('../../../state/CardsProvider', () => ({
  useCards: () => ({ refreshCards }),
}));

const { default: EditEpicPage } = await import('../EditEpicPage');
const { RepoProvider } = await import('../../../state/RepoProvider');
const { requestNavigation, resetNavGuards } = await import('../../../lib/navGuard');

function detail(over: Record<string, unknown> = {}) {
  return {
    slug: 'kanban-rework',
    title: 'Kanban rework',
    description: 'Rework the board.',
    emoji: '🎯',
    branchName: '',
    state: 'active',
    stateManual: false,
    collectHandoffs: true,
    hiddenOnBoard: false,
    createdAt: '2026-08-01T00:00:00Z',
    updatedAt: '2026-08-01T00:00:00Z',
    issues: [],
    comments: [],
    documents: [],
    ...over,
  };
}

function mount() {
  return render(
    <MemoryRouter initialEntries={['/BACI/epics/kanban-rework/edit']}>
      <RepoProvider>
        <Routes>
          <Route path="/:prefix/epics/:slug/edit" element={<EditEpicPage />} />
          <Route path="/:prefix/epics/:slug" element={<div>epic detail</div>} />
          <Route path="/:prefix/issues" element={<div>kanban</div>} />
        </Routes>
      </RepoProvider>
    </MemoryRouter>,
  );
}

// Mount and wait for the epic to load and seed the Details draft.
async function mounted() {
  const view = mount();
  await screen.findByDisplayValue('Kanban rework');
  return view;
}

const titleInput = () => screen.getByLabelText('Title') as HTMLInputElement;
const saveBtn = () => screen.getByRole('button', { name: /save details/i });

beforeEach(() => {
  getFeature.mockReset().mockResolvedValue(detail());
  updateFeature.mockReset().mockResolvedValue(detail({ title: 'Kanban overhaul' }));
  setFeatureAutoClose.mockReset().mockResolvedValue(detail({ stateManual: true }));
  refreshCards.mockReset();
  resetNavGuards();
});

describe('EditEpicPage — details batch', () => {
  it('keeps Save disabled until something actually changes', async () => {
    await mounted();
    expect(saveBtn()).toBeDisabled();
    fireEvent.change(titleInput(), { target: { value: 'Kanban overhaul' } });
    expect(saveBtn()).not.toBeDisabled();
  });

  it('counts the dirty fields in the UNSAVED chip', async () => {
    await mounted();
    expect(screen.queryByText(/UNSAVED/)).toBeNull();
    fireEvent.change(titleInput(), { target: { value: 'Kanban overhaul' } });
    expect(screen.getByText(/UNSAVED · 1/)).toBeTruthy();
    fireEvent.change(screen.getByLabelText('Description'), { target: { value: 'New body.' } });
    expect(screen.getByText(/UNSAVED · 2/)).toBeTruthy();
  });

  it('sends ONE update carrying only the changed keys', async () => {
    await mounted();
    fireEvent.change(titleInput(), { target: { value: 'Kanban overhaul' } });
    fireEvent.click(saveBtn());

    await waitFor(() => expect(updateFeature).toHaveBeenCalledTimes(1));
    const [prefix, slug, fields] = updateFeature.mock.calls[0];
    expect(prefix).toBe('BACI');
    expect(slug).toBe('kanban-rework');
    // Presence, not value: an untouched description must not ride along,
    // or a concurrent edit elsewhere would be silently reverted.
    expect(fields).toEqual({ title: 'Kanban overhaul' });
  });

  it('refuses to save an empty title without a round trip', async () => {
    await mounted();
    fireEvent.change(titleInput(), { target: { value: '   ' } });
    expect(saveBtn()).toBeDisabled();
    expect(updateFeature).not.toHaveBeenCalled();
  });
});

describe('EditEpicPage — properties apply immediately', () => {
  it('persists a toggle with no Save press, leaving Details clean', async () => {
    await mounted();
    // The ariaLabel names the segmented GROUP; the pressable children are
    // the bare On/Off pair, so scope the query rather than matching "Off"
    // across all four property rows.
    const autoClose = within(screen.getByRole('group', { name: /auto-close this epic/i }));
    fireEvent.click(autoClose.getByRole('button', { name: 'Off' }));

    await waitFor(() => expect(setFeatureAutoClose).toHaveBeenCalledTimes(1));
    // The two speeds must not bleed: a property write is not a Details edit.
    expect(updateFeature).not.toHaveBeenCalled();
    expect(screen.queryByText(/UNSAVED/)).toBeNull();
    expect(saveBtn()).toBeDisabled();
  });
});

describe('EditEpicPage — a failed load', () => {
  it('offers a retry instead of claiming the epic is gone', async () => {
    // `silent: true` keeps a modal off a form the user may be mid-edit in,
    // which used to leave a network blip indistinguishable from a delete.
    getFeature.mockReset().mockRejectedValue(new Error('network down'));
    mount();

    expect(await screen.findByText(/Couldn’t load this epic/)).toBeTruthy();
    expect(screen.getByText('network down')).toBeTruthy();
    expect(screen.queryByText(/doesn’t exist any more/)).toBeNull();

    getFeature.mockResolvedValue(detail());
    fireEvent.click(screen.getByRole('button', { name: /try again/i }));
    expect(await screen.findByDisplayValue('Kanban rework')).toBeTruthy();
  });

  it('still says the epic is gone when the server says so', async () => {
    getFeature.mockReset().mockResolvedValue(null);
    mount();
    expect(await screen.findByText(/doesn’t exist any more/)).toBeTruthy();
  });

  it('never flashes the gone copy between the load and the draft seeding', async () => {
    await mounted();
    expect(screen.queryByText(/doesn’t exist any more/)).toBeNull();
  });
});

describe('EditEpicPage — navigation guard', () => {
  it('lets a navigation through untouched when the page is clean', async () => {
    await mounted();
    const proceed = vi.fn();
    requestNavigation(proceed);
    expect(proceed).toHaveBeenCalledTimes(1);
    expect(screen.queryByText(/Discard your changes/i)).toBeNull();
  });

  it('intercepts a shell navigation once the page is dirty', async () => {
    await mounted();
    fireEvent.change(titleInput(), { target: { value: 'Kanban overhaul' } });

    const proceed = vi.fn();
    requestNavigation(proceed);
    // Held, not run — this is the gap useBlocker would have covered.
    expect(proceed).not.toHaveBeenCalled();
    expect(await screen.findByText(/Discard your changes/i)).toBeTruthy();
  });

  it('drops the pending navigation on Keep editing', async () => {
    await mounted();
    fireEvent.change(titleInput(), { target: { value: 'Kanban overhaul' } });
    const proceed = vi.fn();
    requestNavigation(proceed);

    fireEvent.click(await screen.findByRole('button', { name: /keep editing/i }));
    expect(proceed).not.toHaveBeenCalled();
    // Still editing, and the edit survived the modal.
    expect(titleInput().value).toBe('Kanban overhaul');
  });

  it('runs the pending navigation on Discard', async () => {
    await mounted();
    fireEvent.change(titleInput(), { target: { value: 'Kanban overhaul' } });
    const proceed = vi.fn();
    requestNavigation(proceed);

    fireEvent.click(await screen.findByRole('button', { name: /^discard$/i }));
    expect(proceed).toHaveBeenCalledTimes(1);
  });

  it('routes the page-owned back link through the same confirm', async () => {
    await mounted();
    fireEvent.change(titleInput(), { target: { value: 'Kanban overhaul' } });
    fireEvent.click(screen.getByRole('button', { name: /discard edits/i }));
    expect(await screen.findByText(/Discard your changes/i)).toBeTruthy();
  });

  it('does NOT interpose the confirm when Escape only dismissed the emoji picker', async () => {
    // The picker portals its menu out of this page, but React still bubbles
    // the keystroke through the tree — so the page has to tell an Escape
    // aimed at the picker from one aimed at itself.
    await mounted();
    fireEvent.change(titleInput(), { target: { value: 'Kanban overhaul' } });

    fireEvent.keyDown(screen.getByRole('button', { name: /Epic emoji/ }), { key: 'Enter' });
    const menu = await screen.findByRole('menu');
    fireEvent.keyDown(menu, { key: 'Escape', code: 'Escape' });

    expect(screen.queryByText(/Discard your changes/i)).toBeNull();
    expect(titleInput().value).toBe('Kanban overhaul');
  });

  it('interposes the confirm on an Escape aimed at the form itself', async () => {
    await mounted();
    fireEvent.change(titleInput(), { target: { value: 'Kanban overhaul' } });
    fireEvent.keyDown(titleInput(), { key: 'Escape' });
    expect(await screen.findByText(/Discard your changes/i)).toBeTruthy();
  });

  it('disarms once the edit is reverted by hand', async () => {
    await mounted();
    fireEvent.change(titleInput(), { target: { value: 'Kanban overhaul' } });
    fireEvent.change(titleInput(), { target: { value: 'Kanban rework' } });

    await waitFor(() => expect(screen.queryByText(/UNSAVED/)).toBeNull());
    const proceed = vi.fn();
    requestNavigation(proceed);
    expect(proceed).toHaveBeenCalledTimes(1);
  });
});
