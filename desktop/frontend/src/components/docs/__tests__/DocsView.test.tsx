import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router';

// A render-level check on the two grafts the design brief called mandatory,
// because both are behaviour rather than markup and neither is provable from
// the pure helpers alone:
//
//   1. **flatten-on-filter** — typing in the rail search must swap the tree
//      for the flat ranked list, and clearing must bring the tree back.
//   2. **the folder page** — clicking a folder must open a real page with a
//      live children index, never just expand a node.
//
// The specs stay clear of selecting a document on purpose: that mounts the
// TipTap editor, which is a heavy (and irrelevant) thing to stand up in
// jsdom. Everything here exercises the rail and the folder surface.

const listDocs = vi.fn();
const listDocFolders = vi.fn();

vi.mock('../../../api', () => ({
  // Consumed by RepoProvider on mount.
  listBoards: () => Promise.resolve([
    { prefix: 'BACI', name: 'bacio', kind: 'git', issueCount: 0 },
    { prefix: 'NOTES', name: 'Product Notes', kind: 'workspace', issueCount: 0 },
  ]),
  listColumns: () => Promise.resolve([]),
  getLaunchRepo: () => Promise.resolve('BACI'),
  // Consumed by DocsView.
  listDocs: (...args: unknown[]) => listDocs(...args),
  listDocFolders: (...args: unknown[]) => listDocFolders(...args),
  getDisplayPreferences: () => Promise.resolve({ showArchived: false }),
  getDoc: () => Promise.resolve({ filename: '', type: '', content: '', updatedAt: '' }),
  saveDoc: () => Promise.resolve({ filename: '', type: '', content: '', updatedAt: '' }),
  archiveDocument: () => Promise.resolve(),
  unarchiveDocument: () => Promise.resolve(),
  createDoc: () => Promise.resolve({ filename: '', type: '', content: '', updatedAt: '' }),
  renameDoc: () => Promise.resolve({ filename: '', type: '', content: '', updatedAt: '' }),
  deleteDoc: () => Promise.resolve(),
  moveDocToFolder: () => Promise.resolve(),
  createDocFolder: () => Promise.resolve({}),
  renameDocFolder: () => Promise.resolve({}),
  moveDocFolder: () => Promise.resolve({}),
  previewDeleteDocFolder: () => Promise.resolve({ uuid: '', name: '', path: '', subfolders: 0, documentsReRooted: 0 }),
  deleteDocFolder: () => Promise.resolve(),
}));

const { default: DocsView } = await import('../../DocsView');
const { RepoProvider } = await import('../../../state/RepoProvider');

function doc(filename: string, folderUuid: string) {
  return {
    filename,
    type: 'plan',
    sizeBytes: 100,
    updatedAt: '2026-08-01T00:00:00Z',
    createdAt: '2026-07-01T00:00:00Z',
    snippet: '',
    links: [],
    folderUuid,
    folderPosition: 0,
  };
}

function renderDocs() {
  return render(
    <MemoryRouter initialEntries={['/BACI/documents']}>
      <RepoProvider>
        <DocsView activeBoard="BACI" onOpenIssue={() => {}} />
      </RepoProvider>
    </MemoryRouter>,
  );
}

beforeEach(() => {
  window.localStorage.clear();
  listDocs.mockResolvedValue([doc('README.md', ''), doc('data-model.md', 'arch')]);
  listDocFolders.mockResolvedValue([
    { uuid: 'arch', name: 'Architecture', parentUuid: '', position: 0, createdAt: '', updatedAt: '' },
  ]);
});

describe('DocsView rail', () => {
  it('renders the page tree with folders above loose pages', async () => {
    renderDocs();
    expect(await screen.findByRole('tree')).toBeInTheDocument();
    expect(screen.getByText('Architecture')).toBeInTheDocument();
    expect(screen.getByText('README.md')).toBeInTheDocument();
    // A collapsed folder hides its pages.
    expect(screen.queryByText('data-model.md')).not.toBeInTheDocument();
  });

  it('shows the space banner with the git/workspace pill', async () => {
    const { container } = renderDocs();
    await screen.findByRole('tree');
    await waitFor(() =>
      expect(container.querySelector('.mk-docs-space-name')).toHaveTextContent('bacio'),
    );
    expect(container.querySelector('.mk-docs-space-pill')).toHaveTextContent('git');
  });

  it('lists every space in the Space facet group, git and workspace alike', async () => {
    renderDocs();
    await screen.findByRole('tree');
    // The fold is collapsed by default but its contents are in the DOM.
    await waitFor(() => expect(screen.getByRole('tab', { name: /Product Notes/ })).toBeInTheDocument());
    expect(screen.getByRole('tab', { name: /bacio/ })).toHaveAttribute('aria-selected', 'true');
  });

  it('flattens to the ranked list while a search is active, and restores the tree when cleared', async () => {
    renderDocs();
    await screen.findByRole('tree');

    fireEvent.change(screen.getByLabelText('Search pages'), { target: { value: 'data' } });

    await waitFor(() => expect(screen.queryByRole('tree')).not.toBeInTheDocument());
    // DocsList, whole: its toolbar count is the tell.
    expect(screen.getByText('1 document')).toBeInTheDocument();
    expect(screen.getByText('data-model.md')).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText('Search pages'), { target: { value: '' } });
    await waitFor(() => expect(screen.getByRole('tree')).toBeInTheDocument());
  });

  it('flattens when a facet is activated, not only on search', async () => {
    renderDocs();
    await screen.findByRole('tree');

    fireEvent.click(screen.getByRole('tab', { name: /Unlinked/ }));

    await waitFor(() => expect(screen.queryByRole('tree')).not.toBeInTheDocument());
    expect(screen.getByText('2 documents')).toBeInTheDocument();
  });

  it('lets the segmented switch override the derived mode', async () => {
    renderDocs();
    await screen.findByRole('tree');

    fireEvent.click(screen.getByRole('tab', { name: /All docs/ }));
    await waitFor(() => expect(screen.queryByRole('tree')).not.toBeInTheDocument());

    fireEvent.click(screen.getByRole('tab', { name: 'Tree' }));
    await waitFor(() => expect(screen.getByRole('tree')).toBeInTheDocument());
  });
});

describe('DocsView folder page', () => {
  it('opens a real page with a live children index when a folder is selected', async () => {
    renderDocs();
    await screen.findByRole('tree');

    fireEvent.click(screen.getByText('Architecture'));

    // The heading, the summary, and the child row — none of which a bare
    // expand-on-click tree would give you.
    expect(await screen.findByRole('heading', { name: 'Architecture' })).toBeInTheDocument();
    expect(screen.getByText('Children')).toBeInTheDocument();
    expect(screen.getByText('1 page')).toBeInTheDocument();
    expect(screen.getAllByText('data-model.md').length).toBeGreaterThan(0);
  });

  it('carries its own New page button', async () => {
    renderDocs();
    await screen.findByRole('tree');
    fireEvent.click(screen.getByText('Architecture'));

    await screen.findByRole('heading', { name: 'Architecture' });
    // One in the folder-page header, one in the rail foot.
    expect(screen.getAllByRole('button', { name: 'New page' }).length).toBeGreaterThanOrEqual(2);
  });

  it('opens the name dialog from the folder page, seeded empty', async () => {
    renderDocs();
    await screen.findByRole('tree');
    fireEvent.click(screen.getByText('Architecture'));
    await screen.findByRole('heading', { name: 'Architecture' });

    fireEvent.click(screen.getAllByRole('button', { name: 'New page' })[0]);

    expect(await screen.findByRole('dialog')).toBeInTheDocument();
    expect(screen.getByLabelText('Page name')).toHaveValue('');
  });
});
