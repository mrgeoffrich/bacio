import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import FeaturePropertyToggle from '../FeaturePropertyToggle';
import { subscribeErrors } from '../../../errors';
import type { ReportedError } from '../../../errors';
import type { FeatureDetail } from '../../../api';

// FeaturePropertyToggle is the reusable boolean Properties row (BACI-363) — an
// On/Off mk-segmented pair backed by useOptimisticToggle. The tests cover the
// active-button render, the visible optimistic flip, the success reconcile via
// onApplied, the rollback + error report on a failed persist, and the no-op
// when the already-active option is clicked.

const OPTIONS = [
  { id: true, label: 'On' },
  { id: false, label: 'Off' },
];

function detail(over: Partial<FeatureDetail> = {}): FeatureDetail {
  return {
    slug: 'alpha',
    title: 'Alpha',
    description: '',
    emoji: '',
    branchName: '',
    state: 'active',
    stateManual: false,
    collectHandoffs: true,
    createdAt: '2026-01-01T00:00:00Z',
    updatedAt: '2026-01-01T00:00:00Z',
    issues: [],
    comments: [],
    documents: [],
    hiddenOnBoard: false,
    ...over,
  };
}

function captureReports(): { reports: ReportedError[]; stop: () => void } {
  const reports: ReportedError[] = [];
  const stop = subscribeErrors((e) => reports.push(e));
  return { reports, stop };
}

function pressed(name: string): string | null {
  return screen.getByRole('button', { name }).getAttribute('aria-pressed');
}

describe('FeaturePropertyToggle', () => {
  it('marks the option matching the value as active', () => {
    render(
      <FeaturePropertyToggle
        label="Auto Close"
        ariaLabel="auto close"
        options={OPTIONS}
        value={true}
        persist={vi.fn().mockResolvedValue(detail())}
        onApplied={vi.fn()}
        errorHeadline="x"
      />,
    );
    expect(pressed('On')).toBe('true');
    expect(pressed('Off')).toBe('false');
  });

  it('flips optimistically, persists, and reconciles via onApplied on success', async () => {
    const updated = detail({ stateManual: true });
    const persist = vi.fn().mockResolvedValue(updated);
    const onApplied = vi.fn();
    render(
      <FeaturePropertyToggle
        label="Auto Close"
        ariaLabel="auto close"
        options={OPTIONS}
        value={true}
        persist={persist}
        onApplied={onApplied}
        errorHeadline="x"
      />,
    );

    fireEvent.click(screen.getByRole('button', { name: 'Off' }));
    // Optimistic flip is visible before the persist settles.
    expect(pressed('Off')).toBe('true');
    expect(persist).toHaveBeenCalledWith(false);

    await waitFor(() => expect(onApplied).toHaveBeenCalledWith(updated));
  });

  it('reverts and reports through the error bus when the persist rejects', async () => {
    const persist = vi.fn().mockRejectedValue(new Error('PUT failed'));
    const onApplied = vi.fn();
    const { reports, stop } = captureReports();
    render(
      <FeaturePropertyToggle
        label="Auto Close"
        ariaLabel="auto close"
        options={OPTIONS}
        value={true}
        persist={persist}
        onApplied={onApplied}
        errorHeadline="Couldn't update auto-close"
      />,
    );

    fireEvent.click(screen.getByRole('button', { name: 'Off' }));
    expect(pressed('Off')).toBe('true'); // optimistic

    await waitFor(() => expect(pressed('On')).toBe('true')); // reverted
    expect(onApplied).not.toHaveBeenCalled();
    expect(reports.some((r) => r.headline === "Couldn't update auto-close")).toBe(true);
    stop();
  });

  it('does nothing when the already-active option is clicked', () => {
    const persist = vi.fn().mockResolvedValue(detail());
    render(
      <FeaturePropertyToggle
        label="Auto Close"
        ariaLabel="auto close"
        options={OPTIONS}
        value={true}
        persist={persist}
        onApplied={vi.fn()}
        errorHeadline="x"
      />,
    );
    fireEvent.click(screen.getByRole('button', { name: 'On' }));
    expect(persist).not.toHaveBeenCalled();
  });
});
