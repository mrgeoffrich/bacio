import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import ConfirmModal from '../ConfirmModal';

// ConfirmModal is the shared confirm/form overlay shell for the
// SystemSettingsSection rename / delete / restore trio (BACI-364). These
// tests cover the closed/open render, the confirm wiring, the optional
// disabled gate, and the Cancel dismissal.

describe('ConfirmModal', () => {
  it('renders nothing while closed', () => {
    render(
      <ConfirmModal open={false} onClose={() => {}} title="Delete template" confirmLabel="Delete" onConfirm={() => {}}>
        <p>body</p>
      </ConfirmModal>,
    );
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('renders the title, body, and the Cancel / confirm pair when open', () => {
    render(
      <ConfirmModal open onClose={() => {}} title="Delete template" confirmLabel="Delete" onConfirm={() => {}}>
        <p>Delete this one?</p>
      </ConfirmModal>,
    );
    expect(screen.getByText('Delete template')).toBeInTheDocument();
    expect(screen.getByText('Delete this one?')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Delete' })).toBeInTheDocument();
  });

  it('fires onConfirm when the confirm button is clicked', () => {
    const onConfirm = vi.fn();
    render(
      <ConfirmModal open onClose={() => {}} title="Rename template" confirmLabel="Save" onConfirm={onConfirm}>
        <p>fields</p>
      </ConfirmModal>,
    );
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect(onConfirm).toHaveBeenCalledTimes(1);
  });

  it('disables the confirm button when confirmDisabled is set', () => {
    const onConfirm = vi.fn();
    render(
      <ConfirmModal open onClose={() => {}} title="Restore" confirmLabel="Restore" onConfirm={onConfirm} confirmDisabled>
        <p>body</p>
      </ConfirmModal>,
    );
    const btn = screen.getByRole('button', { name: 'Restore' });
    expect(btn).toBeDisabled();
    fireEvent.click(btn);
    expect(onConfirm).not.toHaveBeenCalled();
  });

  it('dismisses via the Cancel button (Modal.Close → onClose)', () => {
    const onClose = vi.fn();
    render(
      <ConfirmModal open onClose={onClose} title="Delete template" confirmLabel="Delete" onConfirm={() => {}}>
        <p>body</p>
      </ConfirmModal>,
    );
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(onClose).toHaveBeenCalled();
  });
});
