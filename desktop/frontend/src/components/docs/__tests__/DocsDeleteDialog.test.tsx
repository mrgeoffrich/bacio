import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import DocsDeleteDialog from '../DocsDeleteDialog';

// The two irreversible Documents mutations share one dialog, and its whole
// job is to state the consequence before the user commits to it.
//
// A FOLDER delete opens on a dry run, so for one round trip the only thing
// the dialog can say is "Checking what this would remove…" — and confirming
// a delete whose blast radius is still loading confirms nothing. That is
// what `confirmDisabled` gates, and it is the same rule LaneDeleteDialog
// applies on the Kanban. It matters more here: a folder's subtree really is
// deleted, where a lane delete only unsets membership.
//
// A PAGE delete has no preview to wait on, so it must NOT inherit the gate.

const noop = vi.fn();

function renderDialog(props: Partial<React.ComponentProps<typeof DocsDeleteDialog>> = {}) {
  render(
    <DocsDeleteDialog
      open
      title="Delete folder"
      subject="Design/API"
      lines={['Checking what this would remove…']}
      busy={false}
      error=""
      confirmDisabled={false}
      onConfirm={noop}
      onClose={noop}
      {...props}
    />,
  );
}

describe('DocsDeleteDialog', () => {
  it('holds Delete inert while the folder preview is still in flight', () => {
    renderDialog({ confirmDisabled: true });
    expect(screen.getByText('Checking what this would remove…')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Delete' })).toBeDisabled();
  });

  it('releases Delete once the blast radius is on screen', () => {
    renderDialog({
      confirmDisabled: false,
      lines: [
        '2 subfolders are deleted with it.',
        '7 pages move back to the top level — no page is deleted.',
      ],
    });
    expect(screen.getByText(/7 pages move back to the top level/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Delete' })).toBeEnabled();
  });

  it('leaves a page delete enabled — it has no preview to wait on', () => {
    renderDialog({
      title: 'Delete page',
      subject: 'sync-across-machines.md',
      confirmDisabled: false,
      lines: ['The body and every link pointing at it are removed permanently.'],
    });
    expect(screen.getByRole('button', { name: 'Delete' })).toBeEnabled();
  });

  it('keeps both actions inert mid-delete', () => {
    renderDialog({ busy: true, lines: ['2 subfolders are deleted with it.'] });
    expect(screen.getByRole('button', { name: 'Deleting…' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeDisabled();
  });
});
