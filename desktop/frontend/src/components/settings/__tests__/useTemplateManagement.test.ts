import { describe, it, expect, vi, beforeEach } from 'vitest';
import { act, renderHook, waitFor } from '@testing-library/react';
import { useTemplateManagement } from '../useTemplateManagement';
import * as api from '../../../api';
import { subscribeErrors } from '../../../errors';
import type { ReportedError } from '../../../errors';
import type { PromptTemplateDTO } from '../../../api';

// useTemplateManagement is the prompt-template CRUD cluster lifted out of
// SystemSettingsSection (BACI-364). The suite stubs the `api` seam and
// exercises the load + the eight mutations, asserting the optimistic
// in-place replacement, the draft bookkeeping, the onTemplatesChanged
// fan-out, and the error path.

vi.mock('../../../api', () => ({
  listPromptTemplates: vi.fn(),
  promptPlaceholders: vi.fn(),
  bacioVersion: vi.fn(),
  savePromptTemplate: vi.fn(),
  savePromptConcurrency: vi.fn(),
  savePromptActionLabel: vi.fn(),
  addPromptTemplate: vi.fn(),
  renamePromptTemplate: vi.fn(),
  deletePromptTemplate: vi.fn(),
  restoreBuiltinPromptTemplates: vi.fn(),
}));

function tpl(over: Partial<PromptTemplateDTO> = {}): PromptTemplateDTO {
  return {
    slug: 'plan',
    mode: 'plan',
    label: 'Planning',
    body: 'plan body',
    default: 'plan body',
    isBuiltin: true,
    isDefault: true,
    concurrencyLimit: 0,
    defaultConcurrencyLimit: 0,
    concurrencyIsDefault: true,
    actionLabel: 'Plan',
    defaultActionLabel: 'Plan',
    actionLabelIsDefault: true,
    ...over,
  };
}

function captureReports(): { reports: ReportedError[]; stop: () => void } {
  const reports: ReportedError[] = [];
  const stop = subscribeErrors(e => reports.push(e));
  return { reports, stop };
}

const seed = [tpl({ slug: 'plan' }), tpl({ slug: 'ship', label: 'Shipping', body: 'ship body' })];

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(api.listPromptTemplates).mockResolvedValue(seed);
  vi.mocked(api.promptPlaceholders).mockResolvedValue(['issue_id', 'issue_title']);
  vi.mocked(api.bacioVersion).mockResolvedValue('v9.9.9');
});

// Render the hook and wait for the initial Promise.all load to land.
async function renderLoaded(onChanged?: () => void) {
  const view = renderHook(() => useTemplateManagement(onChanged));
  await waitFor(() => expect(view.result.current.templates).toHaveLength(2));
  return view;
}

describe('useTemplateManagement', () => {
  it('loads templates, placeholders, and the bacio version on mount', async () => {
    const { result } = await renderLoaded();
    expect(result.current.placeholders).toEqual(['issue_id', 'issue_title']);
    expect(result.current.bacioVer).toBe('v9.9.9');
    // Drafts seed from each template body.
    expect(result.current.drafts).toEqual({ plan: 'plan body', ship: 'ship body' });
  });

  it('setDraft edits a body draft without persisting', async () => {
    const { result } = await renderLoaded();
    act(() => result.current.setDraft('plan', 'edited'));
    expect(result.current.drafts.plan).toBe('edited');
    expect(api.savePromptTemplate).not.toHaveBeenCalled();
  });

  it('saveTemplate persists, replaces the row + draft in place, and notifies', async () => {
    const onChanged = vi.fn();
    vi.mocked(api.savePromptTemplate).mockResolvedValue(tpl({ slug: 'plan', body: 'saved body' }));
    const { result } = await renderLoaded(onChanged);

    await act(async () => { await result.current.saveTemplate('plan', 'saved body'); });

    expect(api.savePromptTemplate).toHaveBeenCalledWith('plan', 'saved body');
    expect(result.current.templates.find(t => t.slug === 'plan')?.body).toBe('saved body');
    expect(result.current.drafts.plan).toBe('saved body');
    expect(result.current.savingSlug).toBeNull();
    expect(onChanged).toHaveBeenCalledTimes(1);
  });

  it('saveConcurrency replaces the row in place and notifies', async () => {
    const onChanged = vi.fn();
    vi.mocked(api.savePromptConcurrency).mockResolvedValue(tpl({ slug: 'ship', concurrencyLimit: 3 }));
    const { result } = await renderLoaded(onChanged);

    await act(async () => { await result.current.saveConcurrency('ship', 3); });

    expect(api.savePromptConcurrency).toHaveBeenCalledWith('ship', 3);
    expect(result.current.templates.find(t => t.slug === 'ship')?.concurrencyLimit).toBe(3);
    expect(onChanged).toHaveBeenCalledTimes(1);
  });

  it('saveActionLabel replaces the row in place and notifies', async () => {
    const onChanged = vi.fn();
    vi.mocked(api.savePromptActionLabel).mockResolvedValue(tpl({ slug: 'plan', actionLabel: 'Sketch' }));
    const { result } = await renderLoaded(onChanged);

    await act(async () => { await result.current.saveActionLabel('plan', 'Sketch'); });

    expect(api.savePromptActionLabel).toHaveBeenCalledWith('plan', 'Sketch');
    expect(result.current.templates.find(t => t.slug === 'plan')?.actionLabel).toBe('Sketch');
    expect(onChanged).toHaveBeenCalledTimes(1);
  });

  it('commitAdd creates the template, clears the add form, refreshes, and notifies', async () => {
    const onChanged = vi.fn();
    vi.mocked(api.addPromptTemplate).mockResolvedValue(tpl({ slug: 'spike' }));
    const { result } = await renderLoaded(onChanged);

    act(() => result.current.setAdding({ slug: 'spike', name: 'Spiking', body: 'b', actionLabel: '' }));
    await act(async () => { await result.current.commitAdd(); });

    expect(api.addPromptTemplate).toHaveBeenCalledWith('spike', 'Spiking', 'b', '');
    expect(result.current.adding).toBeNull();
    expect(api.listPromptTemplates).toHaveBeenCalledTimes(2); // mount + refresh
    expect(onChanged).toHaveBeenCalledTimes(1);
  });

  it('commitRename renames, clears the overlay, refreshes, and notifies', async () => {
    const onChanged = vi.fn();
    vi.mocked(api.renamePromptTemplate).mockResolvedValue(tpl({ slug: 'plan2' }));
    const { result } = await renderLoaded(onChanged);

    act(() => result.current.setRenaming({ slug: 'plan', newSlug: 'plan2', newName: 'Planning2' }));
    await act(async () => { await result.current.commitRename(); });

    expect(api.renamePromptTemplate).toHaveBeenCalledWith('plan', 'plan2', 'Planning2');
    expect(result.current.renaming).toBeNull();
    expect(api.listPromptTemplates).toHaveBeenCalledTimes(2);
    expect(onChanged).toHaveBeenCalledTimes(1);
  });

  it('commitDelete deletes, clears the confirm, refreshes, and notifies', async () => {
    const onChanged = vi.fn();
    vi.mocked(api.deletePromptTemplate).mockResolvedValue(tpl({ slug: 'ship' }));
    const { result } = await renderLoaded(onChanged);

    act(() => result.current.setPendingDelete('ship'));
    await act(async () => { await result.current.commitDelete(); });

    expect(api.deletePromptTemplate).toHaveBeenCalledWith('ship');
    expect(result.current.pendingDelete).toBeNull();
    expect(api.listPromptTemplates).toHaveBeenCalledTimes(2);
    expect(onChanged).toHaveBeenCalledTimes(1);
  });

  it('commitRestore re-seeds the whole list + drafts and notifies', async () => {
    const onChanged = vi.fn();
    const restored = [tpl({ slug: 'plan', body: 'fresh' }), tpl({ slug: 'review', body: 'rev' })];
    vi.mocked(api.restoreBuiltinPromptTemplates).mockResolvedValue(restored);
    const { result } = await renderLoaded(onChanged);

    act(() => result.current.setPendingRestore(true));
    await act(async () => { await result.current.commitRestore(); });

    expect(result.current.pendingRestore).toBe(false);
    expect(result.current.templates.map(t => t.slug)).toEqual(['plan', 'review']);
    expect(result.current.drafts).toEqual({ plan: 'fresh', review: 'rev' });
    expect(onChanged).toHaveBeenCalledTimes(1);
  });

  it('reports a failed mutation through the error bus, clears savingSlug, and does not notify', async () => {
    const onChanged = vi.fn();
    vi.mocked(api.savePromptTemplate).mockRejectedValue(new Error('boom'));
    const { reports, stop } = captureReports();
    const { result } = await renderLoaded(onChanged);

    await act(async () => { await result.current.saveTemplate('plan', 'x'); });

    expect(reports.some(r => r.headline === "Couldn't save template body")).toBe(true);
    expect(result.current.savingSlug).toBeNull();
    expect(onChanged).not.toHaveBeenCalled();
    stop();
  });
});
