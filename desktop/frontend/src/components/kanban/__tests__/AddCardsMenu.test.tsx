import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import AddCardsMenu from '../AddCardsMenu';
import type { BoardCard } from '../../../api';

// The "+" picker is the affordance the whole workstream exists for, and
// two of its behaviours are load-bearing enough to pin down here rather
// than trust to a screenshot:
//
//   1. the search input holds focus once the popover opens. It sits
//      INSIDE a Radix menu, whose focus scope grabs the content on mount
//      and whose typeahead grabs character keys — both of which would
//      make the input unusable if they won.
//   2. picking a card does NOT close the popover, because "add cards" is
//      plural: a fresh git repo needs several cards placed in one visit.

// Only the fields the rows render are populated; the cast keeps the
// fixture terse without redefining the wire shape.
function makeCard(key: string, title: string): BoardCard {
  return { key, title } as BoardCard;
}

const candidates = [
  makeCard('BACI-1', 'Fix login flow'),
  makeCard('BACI-2', 'Refresh board cards'),
  makeCard('MINI-9', 'Login banner copy'),
];

function open(onAdd = vi.fn()) {
  render(<AddCardsMenu laneName="Doing" candidates={candidates} onAdd={onAdd} />);
  fireEvent.keyDown(screen.getByRole('button', { name: 'Add cards to Doing' }), { key: 'Enter' });
  return onAdd;
}

describe('AddCardsMenu', () => {
  it('renders only the trigger until it is opened', () => {
    render(<AddCardsMenu laneName="Doing" candidates={candidates} onAdd={vi.fn()} />);
    expect(screen.getByRole('button', { name: 'Add cards to Doing' })).toBeInTheDocument();
    expect(screen.queryByLabelText('Search issues to add to Doing')).not.toBeInTheDocument();
  });

  it('opens onto a focused search input', () => {
    open();
    const input = screen.getByLabelText('Search issues to add to Doing');
    expect(input).toHaveFocus();
  });

  it('lists every off-board candidate', () => {
    open();
    expect(screen.getByText('BACI-1')).toBeInTheDocument();
    expect(screen.getByText('BACI-2')).toBeInTheDocument();
    expect(screen.getByText('MINI-9')).toBeInTheDocument();
  });

  it('narrows the list as the user types, without losing the caret', () => {
    open();
    const input = screen.getByLabelText('Search issues to add to Doing');
    fireEvent.change(input, { target: { value: 'login' } });
    expect(screen.getByText('BACI-1')).toBeInTheDocument();
    expect(screen.getByText('MINI-9')).toBeInTheDocument();
    expect(screen.queryByText('BACI-2')).not.toBeInTheDocument();
    expect(input).toHaveFocus();
  });

  it('keeps the caret in the input when a typed character matches a row', () => {
    // Radix's menu content runs a typeahead on every character key and
    // focuses the first row whose text matches — "B" would jump straight
    // onto BACI-1 and strand the user mid-word. The input's keydown
    // handler stops the event before the content ever sees it.
    open();
    const input = screen.getByLabelText('Search issues to add to Doing');
    fireEvent.keyDown(input, { key: 'B' });
    expect(input).toHaveFocus();
  });

  it('says so when nothing matches', () => {
    open();
    fireEvent.change(screen.getByLabelText('Search issues to add to Doing'), {
      target: { value: 'zzz' },
    });
    expect(screen.getByText('No matching issues.')).toBeInTheDocument();
  });

  it('adds the highlighted row on Enter', () => {
    const onAdd = open();
    const input = screen.getByLabelText('Search issues to add to Doing');
    fireEvent.keyDown(input, { key: 'ArrowDown' });
    fireEvent.keyDown(input, { key: 'Enter' });
    expect(onAdd).toHaveBeenCalledWith('BACI-2');
  });

  it('adds the searched-for row on Enter without touching the arrows', () => {
    const onAdd = open();
    const input = screen.getByLabelText('Search issues to add to Doing');
    fireEvent.change(input, { target: { value: 'MINI' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    expect(onAdd).toHaveBeenCalledWith('MINI-9');
  });

  it('stays open after a pick so several cards can be placed in one visit', () => {
    const onAdd = open();
    fireEvent.click(screen.getByText('BACI-1'));
    expect(onAdd).toHaveBeenCalledWith('BACI-1');
    expect(screen.getByLabelText('Search issues to add to Doing')).toBeInTheDocument();
  });

  it('closes on Escape and forgets the query, so the next open starts clean', () => {
    open();
    const input = screen.getByLabelText('Search issues to add to Doing');
    fireEvent.change(input, { target: { value: 'login' } });
    fireEvent.keyDown(input, { key: 'Escape' });
    expect(screen.queryByLabelText('Search issues to add to Doing')).not.toBeInTheDocument();

    fireEvent.keyDown(screen.getByRole('button', { name: 'Add cards to Doing' }), { key: 'Enter' });
    expect(screen.getByLabelText('Search issues to add to Doing')).toHaveValue('');
    expect(screen.getByText('BACI-2')).toBeInTheDocument();
  });

  it('explains an empty candidate list instead of showing a blank popover', () => {
    render(<AddCardsMenu laneName="Doing" candidates={[]} onAdd={vi.fn()} />);
    fireEvent.keyDown(screen.getByRole('button', { name: 'Add cards to Doing' }), { key: 'Enter' });
    expect(
      screen.getByText('Every issue in this repository is already on the board.'),
    ).toBeInTheDocument();
  });
});
