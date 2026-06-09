import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { RepoProvider, useActiveRepo } from '../RepoProvider';

// RepoProvider derives the active repo + the prefix/redirect logic off the URL
// (BACI-285), so the suite renders it under MemoryRouter at various paths and
// reads the derived values back through useActiveRepo. The boards mount-load
// is stubbed to a fixed two-repo set.
vi.mock('../../api', () => ({
  listBoards: vi.fn(() => Promise.resolve([
    { prefix: 'BACI', name: 'bacio' },
    { prefix: 'MINI', name: 'mini' },
  ])),
  listColumns: vi.fn(() => Promise.resolve([])),
}));

function Probe() {
  const { activeBoard, urlPrefix, prefixUnknown, fallbackPrefix, legacyRedirectTarget, loading } = useActiveRepo();
  return (
    <div>
      <span data-testid="loading">{String(loading)}</span>
      <span data-testid="activeBoard">{activeBoard}</span>
      <span data-testid="urlPrefix">{urlPrefix}</span>
      <span data-testid="prefixUnknown">{String(prefixUnknown)}</span>
      <span data-testid="fallback">{fallbackPrefix}</span>
      <span data-testid="legacy">{legacyRedirectTarget}</span>
    </div>
  );
}

function renderAt(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <RepoProvider>
        <Probe />
      </RepoProvider>
    </MemoryRouter>,
  );
}

// Wait for the mount load to settle (loading → false) before asserting the
// load-gated derivations (prefixUnknown defers while loading).
async function loaded() {
  await waitFor(() => expect(screen.getByTestId('loading')).toHaveTextContent('false'));
}

describe('RepoProvider / useActiveRepo', () => {
  beforeEach(() => {
    localStorage.clear();
  });
  afterEach(() => {
    localStorage.clear();
  });

  it('matches a lowercased URL prefix to its canonical board casing', async () => {
    renderAt('/baci/pipeline');
    await loaded();
    expect(screen.getByTestId('activeBoard')).toHaveTextContent('BACI');
    expect(screen.getByTestId('prefixUnknown')).toHaveTextContent('false');
  });

  it('flags a genuinely-unknown prefix as not-found once boards have loaded', async () => {
    renderAt('/ZZZZ/pipeline');
    await loaded();
    expect(screen.getByTestId('activeBoard')).toHaveTextContent('');
    expect(screen.getByTestId('prefixUnknown')).toHaveTextContent('true');
  });

  it('treats a prefix-less legacy page word as a soft-redirect, not a 404', async () => {
    renderAt('/pipeline');
    await loaded();
    // `pipeline` is a recognised legacy page word — not a hard 404.
    expect(screen.getByTestId('prefixUnknown')).toHaveTextContent('false');
    // It rebases under the fallback repo (first board, no remembered pick).
    expect(screen.getByTestId('legacy')).toHaveTextContent('/BACI/pipeline');
  });

  it('prefers the remembered repo for the fallback when it still exists', async () => {
    localStorage.setItem('bacio-active-repo', 'MINI');
    renderAt('/');
    await loaded();
    expect(screen.getByTestId('fallback')).toHaveTextContent('MINI');
    expect(screen.getByTestId('legacy')).toHaveTextContent('/MINI/pipeline');
  });

  it('falls back to the first board when the remembered repo is gone', async () => {
    localStorage.setItem('bacio-active-repo', 'GONE');
    renderAt('/');
    await loaded();
    expect(screen.getByTestId('fallback')).toHaveTextContent('BACI');
  });
});
