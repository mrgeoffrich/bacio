import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import LaneMenu from '../LaneMenu';

// The lane menu omits a move item rather than greying it out at the ends
// of the board — a disabled row invites a click that does nothing, and a
// leftmost lane has nothing to say about moving left.

type Handlers = {
  onRename: () => void;
  onMoveLeft: () => void;
  onMoveRight: () => void;
  onDelete: () => void;
};

function open(canMoveLeft: boolean, canMoveRight: boolean): Handlers {
  const handlers: Handlers = {
    onRename: vi.fn(),
    onMoveLeft: vi.fn(),
    onMoveRight: vi.fn(),
    onDelete: vi.fn(),
  };
  render(
    <LaneMenu
      laneName="Doing"
      canMoveLeft={canMoveLeft}
      canMoveRight={canMoveRight}
      {...handlers}
    />,
  );
  fireEvent.keyDown(screen.getByRole('button', { name: 'Actions for Doing lane' }), {
    key: 'Enter',
  });
  return handlers;
}

describe('LaneMenu', () => {
  it('offers both nudges for a lane in the middle of the board', () => {
    open(true, true);
    expect(screen.getByText('Move lane left')).toBeInTheDocument();
    expect(screen.getByText('Move lane right')).toBeInTheDocument();
  });

  it('omits the left nudge for the leftmost lane', () => {
    open(false, true);
    expect(screen.queryByText('Move lane left')).not.toBeInTheDocument();
    expect(screen.getByText('Move lane right')).toBeInTheDocument();
  });

  it('omits both nudges when the lane is the only one', () => {
    open(false, false);
    expect(screen.queryByText('Move lane left')).not.toBeInTheDocument();
    expect(screen.queryByText('Move lane right')).not.toBeInTheDocument();
    // Rename and delete always survive — a one-lane board still needs them.
    expect(screen.getByText('Rename lane…')).toBeInTheDocument();
    expect(screen.getByText('Delete lane…')).toBeInTheDocument();
  });

  it('routes each item to its handler', () => {
    const handlers = open(true, true);
    fireEvent.click(screen.getByText('Move lane left'));
    expect(handlers.onMoveLeft).toHaveBeenCalled();
  });

  it('marks delete as the dangerous one', () => {
    open(true, true);
    expect(screen.getByText('Delete lane…')).toHaveClass('is-danger');
  });
});
