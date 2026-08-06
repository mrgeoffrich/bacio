import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import RepoPicker from '../RepoPicker';
import type { Board, RepoActivity, RepoKind } from '../../api';

// The pivot turned the picker's single "Add Repository…" action into a
// two-option menu and split the row list by `repos.kind`. These specs pin
// the two properties that would silently rot: the frozen-at-open ordering
// surviving the new grouping, and the workspace path never touching the
// git one.
//
// RepoPicker reads everything off useActiveRepo() / useRepoActivity()
// (BACI-361 / BACI-369), so the suite mocks those two hooks rather than
// standing up the whole provider chain — the same shape as the
// CommandPalette suite.
const hoisted = vi.hoisted(() => ({
  boards: [] as Board[],
  activity: [] as RepoActivity[],
  pickBoard: vi.fn(),
  addRepository: vi.fn(),
  refreshBoards: vi.fn(),
  addWorkspace: vi.fn(),
}));

vi.mock('../../state/RepoProvider', () => ({
  useActiveRepo: () => ({
    boards: hoisted.boards,
    activeBoard: hoisted.boards[0]?.prefix ?? '',
    pickBoard: hoisted.pickBoard,
    addRepository: hoisted.addRepository,
    refreshBoards: hoisted.refreshBoards,
  }),
}));
vi.mock('../../state/useRepoActivity', () => ({
  useRepoActivity: () => ({ activity: hoisted.activity }),
}));
vi.mock('../../api', () => ({
  addWorkspace: hoisted.addWorkspace,
}));

function board(prefix: string, kind: RepoKind = 'git'): Board {
  return {
    prefix,
    name: prefix.toLowerCase(),
    kind,
    issueCount: 0,
    syncEnabled: false,
    syncBackgroundEnabled: false,
    syncInProgress: false,
  };
}

// rowPrefixes reads the rendered row order out of the DOM — the prefix
// chip is the only per-row text guaranteed unique.
function rowPrefixes(): string[] {
  return screen.getAllByRole('option').map(el =>
    el.querySelector('.mk-repo-picker-item-prefix')?.textContent ?? '');
}

const openMenu = () => fireEvent.click(screen.getByRole('button', { name: /Select repository|aaaa|wksp/i }));

describe('RepoPicker', () => {
  beforeEach(() => {
    hoisted.boards = [];
    hoisted.activity = [];
    hoisted.pickBoard.mockReset();
    hoisted.addRepository.mockReset();
    hoisted.refreshBoards.mockReset();
    hoisted.addWorkspace.mockReset();
  });

  it('offers both add paths', () => {
    hoisted.boards = [board('AAAA')];
    render(<RepoPicker />);
    openMenu();
    expect(screen.getByText('Add Git Repository…')).toBeInTheDocument();
    expect(screen.getByText('New Workspace…')).toBeInTheDocument();
  });

  it('leaves a git-only store as a flat, unlabelled list', () => {
    hoisted.boards = [board('AAAA'), board('BBBB')];
    render(<RepoPicker />);
    openMenu();
    expect(screen.queryByText('Repositories')).not.toBeInTheDocument();
    expect(screen.queryByText('Workspaces')).not.toBeInTheDocument();
    expect(rowPrefixes()).toEqual(['AAAA', 'BBBB']);
  });

  it('labels both sections and floats git repos above workspaces once one exists', () => {
    hoisted.boards = [board('AAAA'), board('WKSP', 'workspace'), board('BBBB')];
    render(<RepoPicker />);
    openMenu();
    expect(screen.getByText('Repositories')).toBeInTheDocument();
    expect(screen.getByText('Workspaces')).toBeInTheDocument();
    expect(rowPrefixes()).toEqual(['AAAA', 'BBBB', 'WKSP']);
  });

  it('keeps the frozen rank order inside each section', () => {
    hoisted.boards = [board('AAAA'), board('WKSP', 'workspace'), board('BBBB')];
    // BBBB is busy so it outranks AAAA; the workspace is the most
    // recently active of the three but still sorts into its own section.
    hoisted.activity = [
      { prefix: 'AAAA', lastActivityAt: '2026-08-02T00:00:00Z', activeJobs: 0 },
      { prefix: 'BBBB', lastActivityAt: '2026-08-01T00:00:00Z', activeJobs: 1 },
      { prefix: 'WKSP', lastActivityAt: '2026-08-06T00:00:00Z', activeJobs: 0 },
    ];
    render(<RepoPicker />);
    openMenu();
    expect(rowPrefixes()).toEqual(['BBBB', 'AAAA', 'WKSP']);
  });

  it('keeps the section label when the search narrows to one kind', () => {
    hoisted.boards = [board('AAAA'), board('WKSP', 'workspace')];
    render(<RepoPicker />);
    openMenu();
    fireEvent.change(screen.getByPlaceholderText(/Search repositories/), { target: { value: 'wksp' } });
    expect(rowPrefixes()).toEqual(['WKSP']);
    expect(screen.getByText('Workspaces')).toBeInTheDocument();
  });

  it('creates a workspace through the api seam, never the git add path', async () => {
    hoisted.boards = [board('AAAA')];
    hoisted.addWorkspace.mockResolvedValue(board('MARK', 'workspace'));
    const { rerender } = render(<RepoPicker />);
    openMenu();
    fireEvent.click(screen.getByText('New Workspace…'));

    fireEvent.change(screen.getByPlaceholderText('Marketing'), { target: { value: 'Marketing' } });
    fireEvent.click(screen.getByText('Create'));

    await waitFor(() => expect(hoisted.addWorkspace).toHaveBeenCalledWith('Marketing', undefined));
    // The native folder picker / POST /repos path is untouched.
    expect(hoisted.addRepository).not.toHaveBeenCalled();
    // The board list is refreshed; navigation waits for the new prefix to
    // land in it, so nothing has been picked yet — routing early would
    // flash RepoNotFound, since the active repo is URL-derived.
    await waitFor(() => expect(hoisted.refreshBoards).toHaveBeenCalled());
    expect(hoisted.pickBoard).not.toHaveBeenCalled();

    // …and once the refreshed list carries it, the picker routes there.
    hoisted.boards = [board('AAAA'), board('MARK', 'workspace')];
    rerender(<RepoPicker />);
    await waitFor(() => expect(hoisted.pickBoard).toHaveBeenCalledWith('MARK'));
  });

  it('marks the trigger when the active repo is a workspace', () => {
    hoisted.boards = [board('WKSP', 'workspace')];
    render(<RepoPicker />);
    expect(screen.getByText('Workspace')).toBeInTheDocument();
  });
});
