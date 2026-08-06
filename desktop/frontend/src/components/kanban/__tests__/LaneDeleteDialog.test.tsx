import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import LaneDeleteDialog from '../LaneDeleteDialog';
import type { KanbanColumnDeletePreview } from '../../../api';

// The delete confirmation's whole job is to say what actually happens:
// the cards come OFF the board, the issues survive, and the count comes
// from the store's dry run rather than from whatever this window last
// polled. Two invariants worth a test: Confirm stays inert until that dry
// run lands, and the copy never reads as "your issues are deleted".

const hoisted = vi.hoisted(() => ({
  preview: null as KanbanColumnDeletePreview | null,
  resolve: null as ((p: KanbanColumnDeletePreview) => void) | null,
}));

vi.mock('../../../api', () => ({
  previewDeleteKanbanColumn: () =>
    hoisted.preview
      ? Promise.resolve(hoisted.preview)
      : new Promise<KanbanColumnDeletePreview>((res) => { hoisted.resolve = res; }),
}));

function renderDialog() {
  render(
    <LaneDeleteDialog
      repoPrefix="BACI"
      uuid="u2"
      name="Doing"
      busy={false}
      error=""
      onConfirm={vi.fn()}
      onClose={vi.fn()}
    />,
  );
}

describe('LaneDeleteDialog', () => {
  beforeEach(() => {
    hoisted.preview = null;
    hoisted.resolve = null;
  });

  it('holds Delete inert while the dry run is still in flight', () => {
    renderDialog();
    expect(screen.getByText('Checking what this would take off the board…')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Delete lane' })).toBeDisabled();
  });

  it('states the blast radius from the dry run, not the lane length', async () => {
    hoisted.preview = { uuid: 'u2', name: 'Doing', issuesRemovedFromBoard: 3 };
    renderDialog();
    await waitFor(() => {
      expect(screen.getByText(/3 cards come off the Kanban board/)).toBeInTheDocument();
    });
    expect(screen.getByText(/Those issues are not deleted/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Delete lane' })).toBeEnabled();
  });

  it('does not read as destructive when the lane is empty', async () => {
    hoisted.preview = { uuid: 'u2', name: 'Doing', issuesRemovedFromBoard: 0 };
    renderDialog();
    await waitFor(() => {
      expect(screen.getByText('This lane is empty, so no cards are affected.')).toBeInTheDocument();
    });
  });

  it('names the lane it is about to delete', async () => {
    hoisted.preview = { uuid: 'u2', name: 'Doing', issuesRemovedFromBoard: 1 };
    renderDialog();
    expect(screen.getByText('Doing')).toBeInTheDocument();
    await waitFor(() => {
      expect(screen.getByText(/The issue is not deleted/)).toBeInTheDocument();
    });
  });
});
