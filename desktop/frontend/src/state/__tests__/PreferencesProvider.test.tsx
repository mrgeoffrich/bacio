import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import type { ReactNode } from 'react';
import { PreferencesProvider, usePreferences } from '../PreferencesProvider';
import * as api from '../../api';

// PreferencesProvider's mount load seeds the global prefs and runs the
// BACI-312 first-run timezone auto-detect-then-persist. The suite mocks the
// settings api so it can drive the empty-vs-populated server-timezone branch.
vi.mock('../../api', () => ({
  listPromptTemplates: vi.fn(() => Promise.resolve([])),
  getDisplayPreferences: vi.fn(() => Promise.resolve({ showArchived: false })),
  getArchivePreferences: vi.fn(() => Promise.resolve({ autoEnabled: true, retentionDays: 7 })),
  getAudioPreferences: vi.fn(() => Promise.resolve({ shippedSfx: true })),
  getTimezonePreferences: vi.fn(() => Promise.resolve({ timezone: '' })),
  setTimezonePreferences: vi.fn((tz: string) => Promise.resolve({ timezone: tz })),
}));

function wrapper({ children }: { children: ReactNode }) {
  return <PreferencesProvider>{children}</PreferencesProvider>;
}

describe('PreferencesProvider — first-run timezone', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('auto-detects the browser zone and persists it when the server value is empty', async () => {
    vi.mocked(api.getTimezonePreferences).mockResolvedValueOnce({ timezone: '' });
    const detected = Intl.DateTimeFormat().resolvedOptions().timeZone;

    const { result } = renderHook(() => usePreferences(), { wrapper });

    // The detected zone is set + persisted; the persist echoes it back.
    await waitFor(() => expect(result.current.timezone).toBe(detected));
    expect(api.setTimezonePreferences).toHaveBeenCalledTimes(1);
    expect(api.setTimezonePreferences).toHaveBeenCalledWith(detected);
  });

  it('uses the server value verbatim and never persists when it is already set', async () => {
    vi.mocked(api.getTimezonePreferences).mockResolvedValueOnce({ timezone: 'Australia/Sydney' });

    const { result } = renderHook(() => usePreferences(), { wrapper });

    await waitFor(() => expect(result.current.timezone).toBe('Australia/Sydney'));
    expect(api.setTimezonePreferences).not.toHaveBeenCalled();
  });
});
