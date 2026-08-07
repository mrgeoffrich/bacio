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
//   3. the pinned CREATE row keeps its distance from the placement rows —
//      its label never reacts to the query, it closes the popover (the one
//      row that does), and the highlight ring reaches it from either end.
//
// The trigger's aria-label / title changed from "Add cards to {lane}" to
// "Add or create cards in {lane}" when the popover grew the create row:
// the trigger now carries two verbs, so it says both.

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

function open(onAdd = vi.fn(), onCreate = vi.fn()) {
  render(
    <AddCardsMenu laneName="Doing" candidates={candidates} onAdd={onAdd} onCreate={onCreate} />,
  );
  fireEvent.keyDown(screen.getByRole('button', { name: 'Add or create cards in Doing' }), { key: 'Enter' });
  return { onAdd, onCreate };
}

describe('AddCardsMenu', () => {
  it('renders only the trigger until it is opened', () => {
    render(
      <AddCardsMenu laneName="Doing" candidates={candidates} onAdd={vi.fn()} onCreate={vi.fn()} />,
    );
    expect(screen.getByRole('button', { name: 'Add or create cards in Doing' })).toBeInTheDocument();
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
    const { onAdd } = open();
    const input = screen.getByLabelText('Search issues to add to Doing');
    fireEvent.keyDown(input, { key: 'ArrowDown' });
    fireEvent.keyDown(input, { key: 'Enter' });
    expect(onAdd).toHaveBeenCalledWith('BACI-2');
  });

  it('adds the searched-for row on Enter without touching the arrows', () => {
    const { onAdd } = open();
    const input = screen.getByLabelText('Search issues to add to Doing');
    fireEvent.change(input, { target: { value: 'MINI' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    expect(onAdd).toHaveBeenCalledWith('MINI-9');
  });

  it('stays open after a pick so several cards can be placed in one visit', () => {
    const { onAdd } = open();
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

    fireEvent.keyDown(screen.getByRole('button', { name: 'Add or create cards in Doing' }), { key: 'Enter' });
    expect(screen.getByLabelText('Search issues to add to Doing')).toHaveValue('');
    expect(screen.getByText('BACI-2')).toBeInTheDocument();
  });

  it('pins a create row whose label names the lane and never echoes the query', () => {
    open();
    const label = 'New issue in \u201CDoing\u201D\u2026';
    expect(screen.getByText(label)).toBeInTheDocument();
    // Decision of record: the label is CONSTANT. Typing narrows the
    // placement list underneath and must not touch the create row.
    fireEvent.change(screen.getByLabelText('Search issues to add to Doing'), {
      target: { value: 'flaky test' },
    });
    expect(screen.getByText(label)).toBeInTheDocument();
    expect(screen.queryByText(/flaky test/)).not.toBeInTheDocument();
  });

  it('fires create and CLOSES, unlike every placement row', () => {
    const { onCreate } = open();
    fireEvent.click(screen.getByText('New issue in \u201CDoing\u201D\u2026'));
    expect(onCreate).toHaveBeenCalledTimes(1);
    expect(screen.queryByLabelText('Search issues to add to Doing')).not.toBeInTheDocument();
  });

  it('parks the highlight on the create row when ArrowUp leaves the first result', () => {
    const { onAdd, onCreate } = open();
    const input = screen.getByLabelText('Search issues to add to Doing');
    // The highlight starts on the first result; one ArrowUp reaches the
    // pinned row, and Enter there creates rather than places.
    fireEvent.keyDown(input, { key: 'ArrowUp' });
    fireEvent.keyDown(input, { key: 'Enter' });
    expect(onCreate).toHaveBeenCalledTimes(1);
    expect(onAdd).not.toHaveBeenCalled();
  });

  it('keeps the create row reachable when every issue is already placed', () => {
    const onCreate = vi.fn();
    render(<AddCardsMenu laneName="Doing" candidates={[]} onAdd={vi.fn()} onCreate={onCreate} />);
    fireEvent.keyDown(screen.getByRole('button', { name: 'Add or create cards in Doing' }), { key: 'Enter' });
    // Nothing to place, so the highlight rests on create and Enter fires
    // it — the zero-candidate case a footer-anchored row would strand.
    fireEvent.keyDown(screen.getByLabelText('Search issues to add to Doing'), { key: 'Enter' });
    expect(onCreate).toHaveBeenCalledTimes(1);
  });

  it('explains an empty candidate list instead of showing a blank popover', () => {
    render(<AddCardsMenu laneName="Doing" candidates={[]} onAdd={vi.fn()} onCreate={vi.fn()} />);
    fireEvent.keyDown(screen.getByRole('button', { name: 'Add or create cards in Doing' }), { key: 'Enter' });
    expect(
      screen.getByText('Every issue in this repository is already on the board.'),
    ).toBeInTheDocument();
  });
});
