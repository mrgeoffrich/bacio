import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import ProcessMenu from '../ProcessMenu';

// ProcessMenu a11y (BACI-366): the stage toggles are real on/off controls, so
// they must expose aria-pressed; an Edit overlay (onCancel present) must be
// keyboard-dismissable via Escape.
describe('ProcessMenu — a11y', () => {
  const noop = () => {};

  it('exposes aria-pressed reflecting each stage toggle state', () => {
    // Fresh card default: Plan + Implement + Ship on, Design off.
    render(<ProcessMenu onPick={noop} onPickAuto={noop} />);
    expect(screen.getByTitle('Add Design')).toHaveAttribute('aria-pressed', 'false');
    expect(screen.getByTitle(/^Plan on/)).toHaveAttribute('aria-pressed', 'true');
  });

  it('flips aria-pressed when a stage toggle is clicked', () => {
    render(<ProcessMenu onPick={noop} onPickAuto={noop} />);
    const design = screen.getByTitle('Add Design');
    expect(design).toHaveAttribute('aria-pressed', 'false');
    fireEvent.click(design);
    expect(screen.getByTitle(/^Design on/)).toHaveAttribute('aria-pressed', 'true');
  });

  it('labels the overlay as a group and cancels on Escape when cancellable', () => {
    const onCancel = vi.fn();
    // Edit-process overlay: dimmedHasProcess flips the heading to "Replace the
    // process" and surfaces the Cancel affordance.
    render(<ProcessMenu dimmedHasProcess onPick={noop} onPickAuto={noop} onCancel={onCancel} />);
    const group = screen.getByRole('group', { name: /replace the process/i });
    fireEvent.keyDown(group, { key: 'Escape' });
    expect(onCancel).toHaveBeenCalled();
  });
});
