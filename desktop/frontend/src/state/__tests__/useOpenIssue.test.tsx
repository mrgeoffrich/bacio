import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act, render, screen, fireEvent } from '@testing-library/react';
import { MemoryRouter, Routes, Route } from 'react-router';
import { useOpenIssue } from '../useOpenIssue';
import { POLL_INTERVAL_MS } from '../../lib/hooks/useInterval';
import * as api from '../../api';

// useOpenIssue is the route-scoped brief hook (BACI-361). The suite stubs the
// two context hooks it reads + the api seam, and mounts it under a matching
// `/issues/:key` route so useParams() resolves the key.
vi.mock('../RepoProvider', () => ({ useActiveRepo: () => ({ activeBoard: 'BACI' }) }));
vi.mock('../CardsProvider', () => ({ useCards: () => ({ refreshCards: vi.fn() }) }));
vi.mock('../../api', () => ({ getIssueBrief: vi.fn() }));

function brief(description: string, title: string, taken: boolean) {
  return { issue: { key: 'BACI-1', description, title }, taken };
}

function Probe() {
  const { brief: b, setDescEditing } = useOpenIssue();
  return (
    <div>
      <span data-testid="desc">{b?.issue.description ?? ''}</span>
      <span data-testid="title">{b?.issue.title ?? ''}</span>
      <span data-testid="taken">{b ? String(b.taken) : ''}</span>
      <button onClick={() => setDescEditing(true)}>edit</button>
    </div>
  );
}

function renderProbe() {
  return render(
    <MemoryRouter initialEntries={['/issues/BACI-1']}>
      <Routes>
        <Route path="/issues/:key" element={<Probe />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe('useOpenIssue — descEditing guard', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it('preserves the in-progress description on a poll while refreshing everything else', async () => {
    vi.mocked(api.getIssueBrief).mockResolvedValue(brief('v1', 'T1', false));
    vi.useFakeTimers();

    renderProbe();
    // Eager load.
    await act(async () => { await Promise.resolve(); await Promise.resolve(); });
    expect(screen.getByTestId('desc')).toHaveTextContent('v1');
    expect(screen.getByTestId('title')).toHaveTextContent('T1');

    // User starts editing the description.
    fireEvent.click(screen.getByText('edit'));

    // A poll lands a server brief with a newer description + other fields.
    vi.mocked(api.getIssueBrief).mockResolvedValue(brief('v2-from-server', 'T2', true));
    await act(async () => { vi.advanceTimersByTime(POLL_INTERVAL_MS); });
    await act(async () => { await Promise.resolve(); await Promise.resolve(); });

    // The in-progress description is preserved; everything else refreshed.
    expect(screen.getByTestId('desc')).toHaveTextContent('v1');
    expect(screen.getByTestId('title')).toHaveTextContent('T2');
    expect(screen.getByTestId('taken')).toHaveTextContent('true');
  });

  it('takes the polled description verbatim when not editing', async () => {
    vi.mocked(api.getIssueBrief).mockResolvedValue(brief('v1', 'T1', false));
    vi.useFakeTimers();

    renderProbe();
    await act(async () => { await Promise.resolve(); await Promise.resolve(); });
    expect(screen.getByTestId('desc')).toHaveTextContent('v1');

    vi.mocked(api.getIssueBrief).mockResolvedValue(brief('v2', 'T2', false));
    await act(async () => { vi.advanceTimersByTime(POLL_INTERVAL_MS); });
    await act(async () => { await Promise.resolve(); await Promise.resolve(); });

    // Not editing → the description tracks the server.
    expect(screen.getByTestId('desc')).toHaveTextContent('v2');
  });
});
