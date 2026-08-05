import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { RepoProvider, useActiveRepo } from '../RepoProvider';

// RepoProvider derives the active repo + the prefix/redirect logic off the URL
// (BACI-285), so the suite renders it under MemoryRouter at various paths and
// reads the derived values back through useActiveRepo. The boards mount-load
// is stubbed to a fixed two-repo set.
//
// launchRepo (BACI-368) is what `getLaunchRepo` resolves to — each test sets
// it before rendering. A null value makes the call reject, standing in for an
// older server that 404s the route.
const hoisted = vi.hoisted(() => ({ launchRepo: '' as string | null }));

vi.mock('../../api', () => ({
  listBoards: vi.fn(() => Promise.resolve([
    { prefix: 'BACI', name: 'bacio' },
    { prefix: 'MINI', name: 'mini' },
  ])),
  listColumns: vi.fn(() => Promise.resolve([])),
  getLaunchRepo: vi.fn(() => hoisted.launchRepo === null
    ? Promise.reject(new Error('not found'))
    : Promise.resolve(hoisted.launchRepo)),
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
    hoisted.launchRepo = '';
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

  // BACI-368: the repo the app was launched from outranks the remembered pick.
  it('prefers the launch repo over the remembered pick', async () => {
    hoisted.launchRepo = 'MINI';
    localStorage.setItem('bacio-active-repo', 'BACI');
    renderAt('/');
    await loaded();
    expect(screen.getByTestId('fallback')).toHaveTextContent('MINI');
    expect(screen.getByTestId('legacy')).toHaveTextContent('/MINI/pipeline');
  });

  it('keeps the remembered pick when there is no launch repo', async () => {
    hoisted.launchRepo = '';
    localStorage.setItem('bacio-active-repo', 'MINI');
    renderAt('/');
    await loaded();
    expect(screen.getByTestId('fallback')).toHaveTextContent('MINI');
  });

  it('degrades to the remembered pick when the launch-repo call fails', async () => {
    hoisted.launchRepo = null; // server without the route
    localStorage.setItem('bacio-active-repo', 'MINI');
    renderAt('/');
    await loaded();
    expect(screen.getByTestId('fallback')).toHaveTextContent('MINI');
  });

  it('ignores a launch repo that matches no known board', async () => {
    hoisted.launchRepo = 'ZZZZ';
    localStorage.setItem('bacio-active-repo', 'MINI');
    renderAt('/');
    await loaded();
    expect(screen.getByTestId('fallback')).toHaveTextContent('MINI');
  });
});
