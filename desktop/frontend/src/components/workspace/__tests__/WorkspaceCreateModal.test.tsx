import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import WorkspaceCreateModal from '../WorkspaceCreateModal';
import type { Board } from '../../../api';

// The workspace modal is the one add path that does NOT fork on WEB_MODE —
// a workspace has no directory on either transport, so there is nothing
// native to invoke and no path to collect. These specs pin the three
// things that matters for:
//   1. it collects {name, prefix?} and nothing else (no path field),
//   2. a blank prefix is passed as undefined so the server allocates one,
//   3. the server's rejection (a taken prefix is a 409) is surfaced in
//      the form rather than swallowed or reimplemented client-side.
//
// The suite runs against the HTTP transport (vitest.config.ts aliases
// `../api` → api.http.ts, same as web mode), so mocking the seam here
// covers both transports' shared component.
const hoisted = vi.hoisted(() => ({
  addWorkspace: vi.fn(),
}));
vi.mock('../../../api', () => ({
  addWorkspace: hoisted.addWorkspace,
}));

function board(prefix: string, name: string): Board {
  return {
    prefix,
    name,
    kind: 'workspace',
    issueCount: 0,
    syncEnabled: false,
    syncBackgroundEnabled: false,
    syncInProgress: false,
  };
}

describe('WorkspaceCreateModal', () => {
  beforeEach(() => {
    hoisted.addWorkspace.mockReset();
  });

  it('renders nothing when closed', () => {
    render(<WorkspaceCreateModal open={false} onClose={vi.fn()} onCreated={vi.fn()} />);
    expect(screen.queryByText('New Workspace')).not.toBeInTheDocument();
  });

  it('collects name and prefix only — never a path', () => {
    render(<WorkspaceCreateModal open onClose={vi.fn()} onCreated={vi.fn()} />);
    expect(screen.getByText('Name')).toBeInTheDocument();
    expect(screen.getByText('Prefix (optional)')).toBeInTheDocument();
    // The git-repository modal's path field must not leak in here.
    expect(screen.queryByText('Path')).not.toBeInTheDocument();
    expect(screen.queryByPlaceholderText(/\/Users\//)).not.toBeInTheDocument();
  });

  it('passes an omitted prefix through as undefined so the server allocates one', async () => {
    hoisted.addWorkspace.mockResolvedValue(board('MARK', 'Marketing'));
    const onCreated = vi.fn();
    render(<WorkspaceCreateModal open onClose={vi.fn()} onCreated={onCreated} />);

    fireEvent.change(screen.getByPlaceholderText('Marketing'), { target: { value: '  Marketing  ' } });
    fireEvent.click(screen.getByText('Create'));

    await waitFor(() => expect(hoisted.addWorkspace).toHaveBeenCalledWith('Marketing', undefined));
    await waitFor(() => expect(onCreated).toHaveBeenCalledWith(board('MARK', 'Marketing')));
  });

  it('passes a typed prefix through verbatim', async () => {
    hoisted.addWorkspace.mockResolvedValue(board('MKTG', 'Marketing'));
    render(<WorkspaceCreateModal open onClose={vi.fn()} onCreated={vi.fn()} />);

    fireEvent.change(screen.getByPlaceholderText('Marketing'), { target: { value: 'Marketing' } });
    fireEvent.change(screen.getByPlaceholderText(/auto-allocated if blank/), { target: { value: 'MKTG' } });
    fireEvent.click(screen.getByText('Create'));

    await waitFor(() => expect(hoisted.addWorkspace).toHaveBeenCalledWith('Marketing', 'MKTG'));
  });

  it('refuses to submit a blank name without calling the server', () => {
    render(<WorkspaceCreateModal open onClose={vi.fn()} onCreated={vi.fn()} />);
    fireEvent.click(screen.getByText('Create'));
    expect(hoisted.addWorkspace).not.toHaveBeenCalled();
    expect(screen.getByText('Name is required.')).toBeInTheDocument();
  });

  it('surfaces the server rejection for a prefix already in use', async () => {
    hoisted.addWorkspace.mockRejectedValue(new Error('prefix MKTG is already in use'));
    const onCreated = vi.fn();
    render(<WorkspaceCreateModal open onClose={vi.fn()} onCreated={onCreated} />);

    fireEvent.change(screen.getByPlaceholderText('Marketing'), { target: { value: 'Marketing' } });
    fireEvent.change(screen.getByPlaceholderText(/auto-allocated if blank/), { target: { value: 'MKTG' } });
    fireEvent.click(screen.getByText('Create'));

    await waitFor(() =>
      expect(screen.getByText('prefix MKTG is already in use')).toBeInTheDocument());
    // The modal stays open on the failure so the prefix can be corrected.
    expect(screen.getByText('New Workspace')).toBeInTheDocument();
    expect(onCreated).not.toHaveBeenCalled();
  });

  it('surfaces a prefix validation rejection verbatim rather than reimplementing the rule', async () => {
    hoisted.addWorkspace.mockRejectedValue(new Error('prefix must be 4 alphanumeric characters'));
    render(<WorkspaceCreateModal open onClose={vi.fn()} onCreated={vi.fn()} />);

    fireEvent.change(screen.getByPlaceholderText('Marketing'), { target: { value: 'Marketing' } });
    fireEvent.change(screen.getByPlaceholderText(/auto-allocated if blank/), { target: { value: 'M!' } });
    fireEvent.click(screen.getByText('Create'));

    await waitFor(() =>
      expect(screen.getByText('prefix must be 4 alphanumeric characters')).toBeInTheDocument());
  });
});
